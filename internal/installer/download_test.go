package installer

import (
	"context"
	"errors"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
)

type releaseTransport func(*http.Request) (*http.Response, error)

func (f releaseTransport) RoundTrip(r *http.Request) (*http.Response, error) { return f(r) }

type releaseTestBody struct {
	contextReader
	io.Closer
}

// Exercise the production HTTPS download path without depending on releases
// changing on GitHub. File-backed bodies also fit the target's small RAM.
func mockRelease(t *testing.T, path string) (*ReleaseAsset, *int) {
	t.Helper()
	stat, err := os.Stat(path)
	if err != nil {
		t.Fatal(err)
	}
	calls := new(int)
	previous := http.DefaultTransport
	http.DefaultTransport = releaseTransport(func(r *http.Request) (*http.Response, error) {
		*calls++
		if r.URL.Scheme != "https" || r.URL.Host != "github.com" {
			t.Fatal("unexpected release endpoint", r.URL)
		}
		f, err := os.Open(path)
		if err != nil {
			return nil, err
		}
		return &http.Response{StatusCode: http.StatusOK, Body: releaseTestBody{contextReader{r.Context(), f}, f}, Header: make(http.Header)}, nil
	})
	t.Cleanup(func() { http.DefaultTransport = previous })
	return &ReleaseAsset{Name: "onekvm-nanokvm.img", URL: "https://github.com/onekvm/test/releases/download/test/image.img", Size: stat.Size()}, calls
}

func downloadDevice(t *testing.T) Device {
	t.Helper()
	return Device{Profile: "nanokvm", Disk: "/dev/mmcblk0", CID: strings.Repeat("a", 32), Media: "SD", Bytes: 512 * MiB, DataMount: t.TempDir()}
}

func TestDownloadImageChecksIntegrityAndMovesWithoutCopy(t *testing.T) {
	source := fixtureImage(t)
	asset, calls := mockRelease(t, source)
	d := downloadDevice(t)
	image := &DownloadedImage{}
	t.Cleanup(func() { _ = image.Close() })
	var operations []string
	progress := map[string]int64{}
	report := func(label string, op func(Progress) error) error {
		operations = append(operations, label)
		return op(func(done, total int64) {
			if total != asset.Size || done < progress[label] || done > total {
				t.Errorf("invalid %s progress: %d/%d", label, done, total)
			}
			progress[label] = done
		})
	}
	if err := DownloadImage(context.Background(), d, *asset, image, report); err != nil {
		t.Fatal(err)
	}
	if *calls != 1 || !reflect.DeepEqual(operations, []string{"op.download", "op.verify"}) || image.Image.BuildID != "test-build" {
		t.Fatal("download was not validated exactly once", operations, image.Image)
	}
	for _, operation := range operations {
		if progress[operation] != asset.Size {
			t.Fatal("operation did not report complete file progress", operation)
		}
	}
	before, err := os.Stat(image.Source)
	if err != nil {
		t.Fatal(err)
	}
	if err = os.Mkdir(StageDir(d), 0700); err != nil {
		t.Fatal(err)
	}
	staged := filepath.Join(StageDir(d), "image.img")
	if err = image.moveTo(d, image.Image, staged); err != nil {
		t.Fatal(err)
	}
	after, err := os.Stat(staged)
	if err != nil || !os.SameFile(before, after) {
		t.Fatal("preparation did not reuse the original download inode", err)
	}
	if err = image.Close(); err != nil {
		t.Fatal(err)
	}
	if checked, err := Verify(staged, d.Profile, d.Bytes, nil); err != nil || checked != image.Image {
		t.Fatal("closing download ownership removed or changed the prepared image", err)
	}
	if _, err := os.Stat(image.Source); !os.IsNotExist(err) {
		t.Fatal("download cache was retained after promotion")
	}
}

func TestDownloadImageRejectsInvalidAssetsAndCleansUp(t *testing.T) {
	source := fixtureImage(t)
	for _, tc := range []struct {
		name string
		edit func(*ReleaseAsset, *Device)
		want string
	}{
		{"digest", func(a *ReleaseAsset, _ *Device) { a.Digest = "sha256:" + strings.Repeat("0", 64) }, "GitHub asset SHA-256 mismatch"},
		{"wrong-profile", func(_ *ReleaseAsset, d *Device) { d.Profile = "pro-emmc" }, "wrong image type"},
		{"truncated", func(a *ReleaseAsset, _ *Device) { a.Size++ }, "incomplete or oversized"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			asset, _ := mockRelease(t, source)
			d := downloadDevice(t)
			tc.edit(asset, &d)
			image := &DownloadedImage{}
			err := DownloadImage(context.Background(), d, *asset, image, nil)
			if err == nil || !strings.Contains(err.Error(), tc.want) {
				t.Fatal("invalid download was accepted", err)
			}
			entries, err := os.ReadDir(d.DataMount)
			if err != nil || len(entries) != 0 {
				t.Fatal("failed download left incomplete files", entries, err)
			}
		})
	}
}

func TestDownloadImageCancellationCleansUp(t *testing.T) {
	source := fixtureImage(t)
	for _, phase := range []string{"op.download", "op.verify"} {
		t.Run(phase, func(t *testing.T) {
			asset, _ := mockRelease(t, source)
			d := downloadDevice(t)
			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()
			image := &DownloadedImage{}
			err := DownloadImage(ctx, d, *asset, image, func(label string, op func(Progress) error) error {
				return op(func(done, _ int64) {
					if done > 0 && label == phase {
						cancel()
						if err := image.Close(); err != nil {
							t.Error(err)
						}
					}
				})
			})
			if !errors.Is(err, context.Canceled) {
				t.Fatal("download or verification ignored cancellation", err)
			}
			entries, err := os.ReadDir(d.DataMount)
			if err != nil || len(entries) != 0 {
				t.Fatal("canceled download left temporary files", entries, err)
			}
		})
	}
}
