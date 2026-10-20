package installer

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func stagingDevice(t *testing.T) Device {
	t.Helper()
	return Device{DataMount: t.TempDir(), BootMount: t.TempDir()}
}

func TestRetryRebuildsStagingAndPreservesExternalFiles(t *testing.T) {
	d := stagingDevice(t)
	dir := StageDir(d)
	if err := os.Mkdir(dir, 0700); err != nil {
		t.Fatal(err)
	}
	outside := filepath.Join(d.DataMount, "original.img")
	if err := os.WriteFile(outside, []byte("keep"), 0600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "image.img"), []byte("stale"), 0600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "request.json"), []byte("incomplete JSON"), 0600); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(outside, filepath.Join(dir, "external-link")); err != nil {
		t.Fatal(err)
	}
	for i := 0; i < 2; i++ {
		if err := ResetStaging(d, outside); err != nil {
			t.Fatal(err)
		}
		files, err := os.ReadDir(dir)
		if err != nil || len(files) != 0 {
			t.Fatal("staging was not recreated empty", files, err)
		}
		if data, err := os.ReadFile(outside); err != nil || string(data) != "keep" {
			t.Fatal("selected image outside staging was changed", err)
		}
	}
}

func TestRetryPreservesArmedOrStartedInstallation(t *testing.T) {
	for _, marker := range []string{MarkerName, "onekvm-install.started.json", "armed-request", "arming-request", "writing-request"} {
		t.Run(marker, func(t *testing.T) {
			d := stagingDevice(t)
			if err := os.Mkdir(StageDir(d), 0700); err != nil {
				t.Fatal(err)
			}
			cached := filepath.Join(StageDir(d), "image.img")
			if err := os.WriteFile(cached, []byte("required for recovery"), 0600); err != nil {
				t.Fatal(err)
			}
			if strings.HasSuffix(marker, "-request") {
				if err := WriteJSON(filepath.Join(StageDir(d), "request.json"), Request{State: strings.TrimSuffix(marker, "-request")}); err != nil {
					t.Fatal(err)
				}
			} else if err := os.WriteFile(filepath.Join(d.BootMount, marker), []byte("{}"), 0600); err != nil {
				t.Fatal(err)
			}
			if err := ResetStaging(d, ""); !errors.Is(err, ErrActiveInstallation) {
				t.Fatal("armed installation was not protected", err)
			}
			if _, err := os.Stat(cached); err != nil {
				t.Fatal("deleted the active recovery image", err)
			}
		})
	}
}

func TestRetryCannotDeleteSelectedImageOrFollowDirectorySymlink(t *testing.T) {
	d := stagingDevice(t)
	if err := os.Mkdir(StageDir(d), 0700); err != nil {
		t.Fatal(err)
	}
	source := filepath.Join(StageDir(d), "image.img")
	if err := os.WriteFile(source, []byte("selected image"), 0600); err != nil {
		t.Fatal(err)
	}
	link := filepath.Join(d.DataMount, "image-link")
	if err := os.Symlink(source, link); err != nil {
		t.Fatal(err)
	}
	for _, image := range []string{source, link} {
		if err := ResetStaging(d, image); !errors.Is(err, ErrStagedSource) {
			t.Fatal("selected image in staging was not protected", err)
		}
	}
	if err := os.RemoveAll(StageDir(d)); err != nil {
		t.Fatal(err)
	}
	outside := t.TempDir()
	keep := filepath.Join(outside, "keep")
	if err := os.WriteFile(keep, []byte("keep"), 0600); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(outside, StageDir(d)); err != nil {
		t.Fatal(err)
	}
	if err := ResetStaging(d, ""); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(keep); err != nil {
		t.Fatal("followed a staging directory symlink during cleanup", err)
	}
}
