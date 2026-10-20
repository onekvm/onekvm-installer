package installer

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"sync"
)

// DownloadedImage owns one wizard session's verified download. Recovery images
// live on userdata until PrepareDownloaded moves them into the final request;
// direct SD images live in RAM and never create a recovery staging directory.
type DownloadedImage struct {
	Source string
	Image  Image
	device Device
	mu     sync.Mutex
	dir    string
	closed bool
}

// Close can also cancel ownership before a worker has created its directory.
// This lets Ctrl+C remove incomplete downloads without waiting for network I/O.
func (image *DownloadedImage) Close() error {
	image.mu.Lock()
	defer image.mu.Unlock()
	image.closed = true
	if image.dir == "" {
		return nil
	}
	err := os.RemoveAll(image.dir)
	if err == nil {
		image.dir = ""
	}
	return err
}

func (image *DownloadedImage) create(parent string, d Device) error {
	image.mu.Lock()
	defer image.mu.Unlock()
	if image.closed {
		return context.Canceled
	}
	if image.dir != "" {
		return fmt.Errorf("download already started")
	}
	dir, err := os.MkdirTemp(parent, ".onekvm-download-")
	if err != nil {
		return err
	}
	image.dir, image.Source, image.device = dir, filepath.Join(dir, "image.img"), d
	return nil
}

func (image *DownloadedImage) moveTo(d Device, verified Image, destination string) error {
	image.mu.Lock()
	defer image.mu.Unlock()
	if image.closed {
		return context.Canceled
	}
	if !SameDisk(d, image.device) || verified != image.Image {
		return fmt.Errorf("download does not match the verified target and image")
	}
	if err := os.Rename(image.Source, destination); err != nil {
		return err
	}
	return SyncDir(filepath.Dir(destination))
}

func DownloadImage(ctx context.Context, d Device, asset ReleaseAsset, image *DownloadedImage, report Reporter) (err error) {
	defer func() {
		if err != nil {
			_ = image.Close()
		}
	}()
	if report == nil {
		report = Report
	}
	if err = ctx.Err(); err != nil {
		return err
	}
	limit := min(d.Bytes, 4*1024*MiB)
	if asset.Size <= 512 || asset.Size > limit {
		return fmt.Errorf("release asset exceeds target capacity")
	}
	parent, reserve := d.DataMount, int64(256*MiB)
	memoryField := "MemTotal"
	if DirectInstall(d) {
		parent, reserve, memoryField = "/run", RAMReserve, "MemAvailable"
	}
	memory, err := Memory(memoryField)
	if err != nil {
		return err
	}
	if asset.Size+RAMReserve > memory {
		return fmt.Errorf("insufficient RAM for the selected image")
	}
	free, err := freeBytes(parent)
	if err != nil {
		return err
	}
	if free < asset.Size+reserve {
		return fmt.Errorf("image download needs at least %d MiB free", (asset.Size+reserve+MiB-1)/MiB)
	}
	if err = image.create(parent, d); err != nil {
		return err
	}
	err = report("op.download", func(p Progress) error {
		return Download(ctx, asset, image.Source, min(limit, free-reserve), p)
	})
	if err != nil {
		return err
	}
	err = report("op.verify", func(p Progress) error {
		image.Image, err = VerifyContext(ctx, image.Source, d.Profile, limit, p)
		return err
	})
	if err != nil {
		return err
	}
	return ctx.Err()
}
