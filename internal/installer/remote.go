package installer

import (
	"context"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"strings"
	"time"

	"github.com/onekvm/onekvm-installer/internal/buildinfo"
)

// ValidateImageURL accepts only explicit HTTP(S) image endpoints. The URL is
// supplied by the user and may point at any mirror, so it is not restricted to
// GitHub like release assets are.
func ValidateImageURL(raw string) (string, error) {
	raw = strings.TrimSpace(raw)
	u, err := url.Parse(raw)
	if err != nil || u.Scheme != "http" && u.Scheme != "https" || u.Host == "" || u.User != nil {
		return "", fmt.Errorf("image URL must be an HTTP(S) URL without embedded credentials")
	}
	return u.String(), nil
}

func imageURLClient() *http.Client {
	return &http.Client{Timeout: 2 * time.Hour, CheckRedirect: func(req *http.Request, via []*http.Request) error {
		if len(via) >= 8 || req.URL.Scheme != "http" && req.URL.Scheme != "https" {
			return fmt.Errorf("unsafe or excessive redirect")
		}
		return nil
	}}
}

func downloadImageURL(ctx context.Context, rawURL, destination string, maxBytes int64, progress Progress) error {
	request, err := http.NewRequestWithContext(ctx, http.MethodGet, rawURL, nil)
	if err != nil {
		return err
	}
	request.Header.Set("User-Agent", "onekvm-installer/"+buildinfo.Version)
	response, err := imageURLClient().Do(request)
	if err != nil {
		return err
	}
	defer response.Body.Close()
	if response.StatusCode != http.StatusOK {
		return fmt.Errorf("download: %s", response.Status)
	}
	if maxBytes <= 512 {
		return fmt.Errorf("image URL exceeds available staging capacity")
	}
	if response.ContentLength > maxBytes {
		return fmt.Errorf("image URL exceeds available staging capacity")
	}

	f, err := os.OpenFile(destination, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0600)
	if err != nil {
		return err
	}
	total := response.ContentLength
	if total < 0 {
		total = 0
	}
	n, copyErr := CopyProgress(f, io.LimitReader(response.Body, maxBytes+1), total, progress)
	closeErr := f.Close()
	if copyErr != nil {
		return copyErr
	}
	if closeErr != nil {
		return closeErr
	}
	if n > maxBytes {
		return fmt.Errorf("image URL exceeds available staging capacity")
	}
	if response.ContentLength >= 0 && n != response.ContentLength {
		return fmt.Errorf("incomplete image download")
	}
	if n <= 512 {
		return fmt.Errorf("image URL returned an empty or incomplete image")
	}
	return nil
}

// DownloadURL downloads and verifies a user-provided HTTP(S) image URL using
// the same temporary ownership and target-capacity checks as a release asset.
func DownloadURL(ctx context.Context, d Device, rawURL string, image *DownloadedImage, report Reporter) (err error) {
	if image == nil {
		return fmt.Errorf("missing downloaded image")
	}
	validatedURL, err := ValidateImageURL(rawURL)
	if err != nil {
		return err
	}
	if report == nil {
		report = Report
	}
	if err = ctx.Err(); err != nil {
		return err
	}
	limit := min(d.Bytes, 4*1024*MiB)
	parent, reserve, memoryField := d.DataMount, int64(256*MiB), "MemTotal"
	if DirectInstall(d) {
		parent, reserve, memoryField = "/run", RAMReserve, "MemAvailable"
	}
	if _, err = Memory(memoryField); err != nil {
		return err
	}
	free, err := freeBytes(parent)
	if err != nil {
		return err
	}
	maxBytes := min(limit, free-reserve)
	if maxBytes <= 512 {
		return fmt.Errorf("image download needs more free space")
	}
	if err = image.create(parent, d); err != nil {
		return err
	}
	defer func() {
		if err != nil {
			_ = image.Close()
		}
	}()
	err = report("op.download", func(p Progress) error {
		return downloadImageURL(ctx, validatedURL, image.Source, maxBytes, p)
	})
	if err != nil {
		return err
	}
	err = report("op.verify", func(p Progress) error {
		// The profile is selected from the verified metadata after download.
		image.Image, err = VerifyContext(ctx, image.Source, "", limit, p)
		return err
	})
	if err != nil {
		return err
	}
	return ctx.Err()
}
