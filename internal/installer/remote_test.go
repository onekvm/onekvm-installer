package installer

import (
	"context"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"testing"
)

func TestValidateImageURL(t *testing.T) {
	for _, raw := range []string{"http://mirror.example/one.img", "https://mirror.example/one.img?token=test"} {
		if got, err := ValidateImageURL(raw); err != nil || got != raw {
			t.Fatalf("valid URL rejected: %q, %v", raw, err)
		}
	}
	for _, raw := range []string{"", "ftp://mirror.example/one.img", "https://user:pass@mirror.example/one.img", "/tmp/one.img"} {
		if _, err := ValidateImageURL(raw); err == nil {
			t.Fatalf("invalid URL accepted: %q", raw)
		}
	}
}

func TestDownloadURLVerifiesHTTPImageAndCleansUp(t *testing.T) {
	source := fixtureImage(t)
	data, err := os.ReadFile(source)
	if err != nil {
		t.Fatal(err)
	}
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/one.img" || r.Header.Get("User-Agent") == "" {
			t.Fatalf("unexpected request: %s %s", r.URL, r.Header.Get("User-Agent"))
		}
		_, _ = w.Write(data)
	}))
	defer server.Close()

	d := downloadDevice(t)
	image := &DownloadedImage{}
	var operations []string
	if err := DownloadURL(context.Background(), d, server.URL+"/one.img", image, func(label string, op func(Progress) error) error {
		operations = append(operations, label)
		return op(nil)
	}); err != nil {
		t.Fatal(err)
	}
	defer image.Close()
	if image.Image.Profile != "nanokvm" || image.Image.BuildID != "test-build" || !strings.HasSuffix(image.Source, "/image.img") {
		t.Fatalf("unexpected verified image: %+v, %s", image.Image, image.Source)
	}
	if strings.Join(operations, ",") != "op.download,op.verify" {
		t.Fatalf("unexpected operations: %v", operations)
	}
}

func TestDownloadURLRejectsOversizedResponse(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Length", "1048577")
		_, _ = w.Write(make([]byte, 1048577))
	}))
	defer server.Close()
	d := downloadDevice(t)
	d.Bytes = MiB
	image := &DownloadedImage{}
	if err := DownloadURL(context.Background(), d, server.URL, image, nil); err == nil || !strings.Contains(err.Error(), "capacity") {
		t.Fatalf("oversized URL response accepted: %v", err)
	}
	if image.Source == "" {
		t.Fatal("failed download did not record a temporary source")
	}
	if _, err := os.Stat(image.Source); !os.IsNotExist(err) {
		t.Fatalf("failed download retained temporary source: %s (%v)", image.Source, err)
	}
}
