package installer

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"testing/iotest"
)

func TestRawCopyPreservesBytesAndLength(t *testing.T) {
	page := os.Getpagesize()
	data := make([]byte, 2*MiB+page+123)
	data[page+7] = 42
	data[MiB+page-1] = 99
	path := filepath.Join(t.TempDir(), "copy.img")
	f, err := os.Create(path)
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()
	last := int64(-1)
	hash, err := copyRawImage(f, iotest.HalfReader(bytes.NewReader(data)), int64(len(data)), func(done, total int64) {
		if done < last || done > total || total != int64(len(data)) {
			t.Errorf("invalid progress %d/%d after %d", done, total, last)
		}
		last = done
	})
	if err != nil {
		t.Fatal(err)
	}
	wantHash := sha256.Sum256(data)
	if hash != hex.EncodeToString(wantHash[:]) || last != int64(len(data)) {
		t.Fatal("copy hash or progress differs")
	}
	copied, err := os.ReadFile(path)
	if err != nil || !bytes.Equal(data, copied) {
		t.Fatal("raw bytes or trailing length changed", err)
	}
	// A readback must include every byte, including the trailing partial page.
	if _, err := f.Seek(0, io.SeekStart); err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(copied[:page], make([]byte, page)) {
		t.Fatal("leading zeros changed")
	}
}

func TestRawCopyRejectsSizeChanges(t *testing.T) {
	for _, tc := range []struct {
		name, want string
		data       []byte
		size       int64
	}{
		{"short", "incomplete raw image", []byte{1}, 512},
		{"long", "length changed", []byte{1, 2}, 1},
	} {
		t.Run(tc.name, func(t *testing.T) {
			f, err := os.CreateTemp(t.TempDir(), "copy")
			if err != nil {
				t.Fatal(err)
			}
			defer f.Close()
			_, err = copyRawImage(f, bytes.NewReader(tc.data), tc.size, nil)
			if err == nil || !strings.Contains(err.Error(), tc.want) {
				t.Fatalf("got %v, want %s", err, tc.want)
			}
		})
	}
}

func TestStageRawImageUnchanged(t *testing.T) {
	source := fixtureImage(t)
	info, err := Verify(source, "nanokvm", 512*MiB, nil)
	if err != nil {
		t.Fatal(err)
	}
	destination := filepath.Join(t.TempDir(), "image.img")
	if err := stageLocalImage(source, destination, info.PayloadSize+512, nil); err != nil {
		t.Fatal(err)
	}
	want, err := HashFile(source, nil)
	if err != nil {
		t.Fatal(err)
	}
	got, err := HashFile(destination, nil)
	if err != nil || got != want {
		t.Fatal("staging changed release file hash", err)
	}
	copied, err := Verify(destination, "nanokvm", 512*MiB, nil)
	if err != nil || copied != info {
		t.Fatal("staging changed image metadata", err)
	}
}
