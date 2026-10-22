package nanokvm

import (
	"bytes"
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/onekvm/onekvm-installer/internal/installer"
)

func TestBootstrapFitsStock16MiBFAT(t *testing.T) {
	// A 16 MiB FAT with 2 KiB clusters must temporarily hold both FIPs.
	if !bootstrapFits(16334*1024, 2048, 440832, 15*installer.MiB, 646, 440832, 1024) {
		t.Fatal("rejected recovery that fits the stock 16 MiB FAT")
	}
	for _, tt := range []struct {
		total, cluster, fip int64
		sizes               []int64
	}{
		{16334 * 1024, 2048, 440832, []int64{16208716, 646, 440832, 1024}}, // Both FIPs exceed the available capacity.
		{16334 * 1024, 2048, 440832, []int64{16 * installer.MiB, 646, 1024}},
		{6 * 4096, 4096, 1, []int64{4097, 1, 1}}, // Per-file cluster rounding.
		{100000, 0, 1, []int64{1}},
		{100000, 4096, 0, []int64{1}},
	} {
		if bootstrapFits(tt.total, tt.cluster, tt.fip, tt.sizes...) {
			t.Fatal("accepted boot files without enough space", tt)
		}
	}
}

func bootstrapFixture(t *testing.T) (installer.Device, installer.Request) {
	t.Helper()
	d := stagingDevice(t)
	d.Profile = "nanokvm"
	if err := os.Mkdir(installer.StageDir(d), 0700); err != nil {
		t.Fatal(err)
	}
	put(t, d.BootMount, "fip.bin", "essential boot firmware")
	put(t, d.BootMount, "boot.sd", "old kernel")
	put(t, d.BootMount, "uEnv.txt", "old environment")
	put(t, d.BootMount, "wifi.sta", "")
	put(t, d.BootMount, "System Volume Information/old", "old metadata")
	put(t, installer.StageDir(d), "kernel.itb", strings.Repeat("new kernel", 200000))
	put(t, installer.StageDir(d), "uEnv.txt", "new environment")
	put(t, installer.StageDir(d), "fip.bin", "OneKVM boot firmware")
	put(t, installer.StageDir(d), "image.img", "image")
	put(t, installer.StageDir(d), "firstboot.txt", "firstboot")
	req := installer.Request{Version: installer.RecoveryAPI, Token: strings.Repeat("a", 64), State: "prepared", Device: d,
		Image: installer.Image{PayloadSize: 5}, ImageBytes: 517}
	if err := installer.RequestHashes(&req, installer.StageDir(d), false, nil); err != nil {
		t.Fatal(err)
	}
	if err := installer.WriteJSON(filepath.Join(installer.StageDir(d), "request.json"), req); err != nil {
		t.Fatal(err)
	}
	return d, req
}

func TestReplaceBootstrapClearsOldFilesWithoutBackup(t *testing.T) {
	d, req := bootstrapFixture(t)
	outside := filepath.Join(t.TempDir(), "keep")
	if err := os.WriteFile(outside, []byte("keep"), 0600); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(filepath.Dir(outside), filepath.Join(d.BootMount, "link")); err != nil {
		t.Fatal(err)
	}
	plan, err := planBootstrap(d, req)
	if err != nil {
		t.Fatal(err)
	}
	var progressCalls int
	var previous int64
	report := func(_ string, op func(installer.Progress) error) error {
		return op(func(done, total int64) {
			if total <= 0 || done < previous || done > total {
				t.Fatalf("invalid progress: %d/%d (previous %d)", done, total, previous)
			}
			previous = done
			progressCalls++
		})
	}
	if err := replaceBootstrap(plan, report); err != nil {
		t.Fatal(err)
	}
	if progressCalls < 3 || previous == 0 {
		t.Fatal("boot replacement did not report write/readback progress")
	}
	entries, err := os.ReadDir(d.BootMount)
	if err != nil || len(entries) != 4 {
		t.Fatal("expected only FIP, recovery kernel, environment and marker", entries, err)
	}
	for _, pair := range [][2]string{{"boot.sd", "kernel.itb"}, {"uEnv.txt", "uEnv.txt"}, {"fip.bin", "fip.bin"}} {
		got, err := os.ReadFile(filepath.Join(d.BootMount, pair[0]))
		want, readErr := os.ReadFile(filepath.Join(installer.StageDir(d), pair[1]))
		if err != nil || readErr != nil || !bytes.Equal(got, want) {
			t.Fatal("replacement boot file differs", pair, err, readErr)
		}
	}
	if got, err := os.ReadFile(filepath.Join(d.BootMount, "fip.bin")); err != nil || string(got) != "OneKVM boot firmware" {
		t.Fatal("did not replace the previous boot firmware", err)
	}
	if _, err := os.Stat(filepath.Join(installer.StageDir(d), "backup")); !os.IsNotExist(err) {
		t.Fatal("old boot files were backed up", err)
	}
	if got, err := os.ReadFile(outside); err != nil || string(got) != "keep" {
		t.Fatal("cleanup followed a symlink", err)
	}
	loaded, err := installer.LoadPrepared(d, nil)
	if err != nil || loaded.State != "armed" {
		t.Fatal("completed request cannot be recovered", err)
	}
	var marker installer.Marker
	if err := installer.ReadJSON(filepath.Join(d.BootMount, installer.MarkerName), &marker); err != nil {
		t.Fatal(err)
	}
	hash, err := installer.HashFile(filepath.Join(installer.StageDir(d), "request.json"), nil)
	if err != nil || marker.RequestSHA256 != hash || marker.Token != req.Token {
		t.Fatal("boot marker is not bound to the final request", err)
	}
}

func TestBootstrapFailureKeepsRetryStateWithoutRestoringOldBoot(t *testing.T) {
	d, req := bootstrapFixture(t)
	plan, err := planBootstrap(d, req)
	if err != nil {
		t.Fatal(err)
	}
	kernel := filepath.Join(installer.StageDir(d), "kernel.itb")
	original, err := os.ReadFile(kernel)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(kernel, []byte("changed after preflight"), 0600); err != nil {
		t.Fatal(err)
	}
	err = replaceBootstrap(plan, installer.Report)
	if err == nil || !strings.Contains(err.Error(), "rerun onekvm-installer arm") {
		t.Fatal("failed readback did not explain how to retry", err)
	}
	if err := installer.ReadJSON(filepath.Join(installer.StageDir(d), "request.json"), &req); err != nil || req.State != "arming" {
		t.Fatal("failure did not persist retry state", req.State, err)
	}
	if _, err := os.Stat(filepath.Join(d.BootMount, installer.MarkerName)); !os.IsNotExist(err) {
		t.Fatal("published a recovery marker before validating boot files")
	}
	if boot, err := os.ReadFile(filepath.Join(d.BootMount, "boot.sd")); err != nil || string(boot) == "old kernel" {
		t.Fatal("unexpected restore of original kernel", err)
	}
	if err := installer.ResetStaging(d, ""); !errors.Is(err, installer.ErrActiveInstallation) {
		t.Fatal("retry files could be deleted after boot cleanup", err)
	}
	if err := os.WriteFile(kernel, original, 0600); err != nil {
		t.Fatal(err)
	}
	req, err = installer.LoadPrepared(d, nil)
	if err != nil {
		t.Fatal(err)
	}
	plan, err = planBootstrap(d, req)
	if err != nil {
		t.Fatal(err)
	}
	if err := replaceBootstrap(plan, installer.Report); err != nil {
		t.Fatal("cannot retry interrupted replacement", err)
	}
}

func TestBootstrapPreflightAndCancellationPreserveOriginalBoot(t *testing.T) {
	d, req := bootstrapFixture(t)
	plan, err := planBootstrap(d, req)
	if err != nil {
		t.Fatal(err)
	}
	err = replaceBootstrap(plan, func(string, func(installer.Progress) error) error { return context.Canceled })
	if !errors.Is(err, context.Canceled) {
		t.Fatal("pre-write cancellation was ignored", err)
	}
	if err := os.Remove(filepath.Join(d.BootMount, "fip.bin")); err != nil {
		t.Fatal(err)
	}
	if _, err := planBootstrap(d, req); err == nil {
		t.Fatal("allowed cleanup without FIP")
	}
	got, err := os.ReadFile(filepath.Join(d.BootMount, "boot.sd"))
	if err != nil || string(got) != "old kernel" {
		t.Fatal("preflight changed old boot files", err)
	}
	var unchanged installer.Request
	if err := installer.ReadJSON(filepath.Join(installer.StageDir(d), "request.json"), &unchanged); err != nil || unchanged.State != "prepared" {
		t.Fatal("preflight changed the prepared request", err)
	}
}

func stagingDevice(t *testing.T) installer.Device {
	t.Helper()
	return installer.Device{DataMount: t.TempDir(), BootMount: t.TempDir()}
}
func put(t *testing.T, root, path, data string) {
	t.Helper()
	path = filepath.Join(root, path)
	if err := os.MkdirAll(filepath.Dir(path), 0755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(data), 0600); err != nil {
		t.Fatal(err)
	}
}
