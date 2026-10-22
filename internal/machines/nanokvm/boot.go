package nanokvm

import (
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"syscall"

	"github.com/onekvm/onekvm-installer/internal/installer"
)

type bootstrapPlan struct {
	device       installer.Device
	request      installer.Request
	requestBytes []byte
	markerBytes  []byte
	fipName      string
}

// Keep the current FIP until its replacement has been synced and renamed.
// Account for that peak allocation, rounding each file to FAT clusters.
func bootstrapFits(total, cluster, fip int64, sizes ...int64) bool {
	if cluster <= 0 || total <= 0 || fip <= 0 {
		return false
	}
	round := func(n int64) int64 { return (n + cluster - 1) / cluster * cluster }
	needed := round(fip) + 2*cluster
	for _, size := range sizes {
		if size <= 0 || size > total {
			return false
		}
		needed += round(size)
	}
	return needed <= total
}

func planBootstrap(d installer.Device, req installer.Request) (bootstrapPlan, error) {
	p := bootstrapPlan{device: d, request: req}
	if _, err := os.Lstat(filepath.Join(d.BootMount, "onekvm-install.started.json")); !os.IsNotExist(err) {
		return p, fmt.Errorf("installation has already started; manual recovery is required")
	}
	markerPath := filepath.Join(d.BootMount, installer.MarkerName)
	if _, err := os.Lstat(markerPath); err == nil {
		var marker installer.Marker
		if req.State != "arming" {
			return p, fmt.Errorf("installation marker already exists")
		}
		if err := installer.ReadJSON(markerPath, &marker); err != nil {
			return p, err
		}
		if marker.Version != installer.RecoveryAPI || marker.Token != req.Token || !installer.SameDisk(marker.Device, d) {
			return p, fmt.Errorf("boot marker does not match installation")
		}
	} else if !os.IsNotExist(err) {
		return p, err
	}
	entries, err := os.ReadDir(d.BootMount)
	if err != nil {
		return p, err
	}
	var fipSize int64
	for _, entry := range entries {
		if !strings.EqualFold(entry.Name(), "fip.bin") {
			continue
		}
		info, err := entry.Info()
		if err != nil {
			return p, err
		}
		if p.fipName != "" || !info.Mode().IsRegular() || info.Size() <= 0 {
			return p, fmt.Errorf("boot FIP must be one nonempty regular file")
		}
		p.fipName, fipSize = entry.Name(), info.Size()
	}
	if p.fipName == "" {
		return p, fmt.Errorf("boot FIP is missing; refusing to clear boot files")
	}
	p.request.State = "armed"
	p.requestBytes, err = installer.JsonBytes(p.request)
	if err != nil {
		return p, err
	}
	hash := sha256.Sum256(p.requestBytes)
	p.markerBytes, err = installer.JsonBytes(installer.Marker{Version: installer.RecoveryAPI, Token: req.Token, Device: d, RequestSHA256: hex.EncodeToString(hash[:])})
	if err != nil {
		return p, err
	}
	var sizes []int64
	for _, name := range (Machine{}).BootstrapFiles() {
		info, err := os.Lstat(filepath.Join(installer.StageDir(d), name))
		if err != nil {
			return p, err
		}
		if !info.Mode().IsRegular() || info.Size() <= 0 {
			return p, fmt.Errorf("invalid recovery boot file: %s", name)
		}
		sizes = append(sizes, info.Size())
	}
	var fs syscall.Statfs_t
	if err = syscall.Statfs(d.BootMount, &fs); err != nil {
		return p, err
	}
	sizes = append(sizes, int64(len(p.markerBytes)))
	if !bootstrapFits(int64(fs.Blocks)*int64(fs.Bsize), int64(fs.Bsize), fipSize, sizes...) {
		return p, fmt.Errorf("boot partition cannot fit recovery files even after cleanup")
	}
	return p, nil
}

func replaceBootstrap(p bootstrapPlan, report installer.Reporter) error {
	d, req := p.device, p.request
	dir, boot := installer.StageDir(d), d.BootMount
	req.State = "arming"
	incomplete := func(cause error) error {
		// Invalidate a marker if its write succeeded but its directory sync failed.
		stateErr := installer.WriteJSON(filepath.Join(dir, "request.json"), req)
		if stateErr != nil {
			return fmt.Errorf("recovery boot replacement incomplete; keep power connected and rerun onekvm-installer arm: %w (saving retry state: %v)", cause, stateErr)
		}
		return fmt.Errorf("recovery boot replacement incomplete; keep power connected and rerun onekvm-installer arm: %w", cause)
	}
	err := report("op.boot_cleanup", func(installer.Progress) error {
		// This is the first write and the point where Ctrl+C must wait.
		if err := installer.WriteJSON(filepath.Join(dir, "request.json"), req); err != nil {
			return err
		}
		entries, err := os.ReadDir(boot)
		if err != nil {
			return incomplete(err)
		}
		for _, entry := range entries {
			if entry.Name() != p.fipName {
				if err := os.RemoveAll(filepath.Join(boot, entry.Name())); err != nil {
					return incomplete(err)
				}
			}
		}
		if err := installer.SyncDir(boot); err != nil {
			return incomplete(err)
		}
		return nil
	})
	if err != nil {
		return err
	}
	return report("Installing recovery boot files (keep power connected)", func(progress installer.Progress) error {
		files := []struct{ source, dest, hash string }{
			{"kernel.itb", "boot.sd", req.BootFiles["kernel.itb"]},
			{"uEnv.txt", "uEnv.txt", req.BootFiles["uEnv.txt"]},
			// Replace firmware last: Recovery must use the matching OneKVM
			// bootloader, independently of the previously installed system.
			{"fip.bin", p.fipName, req.BootFiles["fip.bin"]},
		}
		var total, offset int64
		for _, file := range files {
			info, err := os.Stat(filepath.Join(dir, file.source))
			if err != nil {
				return incomplete(err)
			}
			total += info.Size() * 2 // Write and readback.
		}
		if progress != nil {
			progress(0, total)
		}
		for _, file := range files {
			path := filepath.Join(boot, file.dest)
			step := func(done, _ int64) {
				if progress != nil {
					progress(offset+done, total)
				}
			}
			n, err := writeBootstrapFile(filepath.Join(dir, file.source), path, step)
			if err != nil {
				return incomplete(err)
			}
			offset += n
			hash, err := installer.HashFile(path, step)
			if err != nil {
				return incomplete(err)
			}
			if hash != file.hash {
				return incomplete(fmt.Errorf("bootstrap readback mismatch: %s", file.dest))
			}
			offset += n
		}
		if err := installer.WritePrivate(filepath.Join(dir, "request.json"), p.requestBytes); err != nil {
			return incomplete(err)
		}
		// Publish only after every boot file has been synced and read back.
		if err := installer.WritePrivate(filepath.Join(boot, installer.MarkerName), p.markerBytes); err != nil {
			return incomplete(err)
		}
		return nil
	})
}

func writeBootstrapFile(source, dest string, progress installer.Progress) (int64, error) {
	f, err := os.Open(source)
	if err != nil {
		return 0, err
	}
	defer f.Close()
	info, err := f.Stat()
	if err != nil {
		return 0, err
	}
	g, err := os.OpenFile(dest+".tmp", os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0600)
	if err != nil {
		return 0, err
	}
	defer os.Remove(dest + ".tmp")
	n, err := installer.CopyProgress(g, f, info.Size(), progress)
	if err == nil {
		err = g.Sync()
	}
	closeErr := g.Close()
	if err == nil {
		err = closeErr
	}
	if err == nil {
		err = os.Rename(dest+".tmp", dest)
	}
	if err == nil {
		err = installer.SyncDir(filepath.Dir(dest))
	}
	return n, err
}

func (Machine) BootstrapFiles() []string { return []string{"kernel.itb", "uEnv.txt", "fip.bin"} }
func (Machine) PrepareBootstrap(dir string, req installer.Request) error {
	path := filepath.Join(dir, "uEnv.txt")
	env, err := os.ReadFile(path)
	if err != nil {
		return err
	}
	env, err = bindEnvironment(env, req.Token)
	if err != nil {
		return err
	}
	return installer.WritePrivate(path, env)
}
func bindEnvironment(env []byte, token string) ([]byte, error) {
	lines := strings.Split(string(env), "\n")
	bound := 0
	for i, line := range lines {
		if strings.Contains(line, "onekvm.installer=") {
			return nil, fmt.Errorf("bootstrap environment already contains an installer token")
		}
		if strings.HasPrefix(line, "othbootargs=") {
			if strings.ContainsRune(line, '\x00') {
				return nil, fmt.Errorf("invalid bootstrap environment")
			}
			lines[i] = line + " onekvm.installer=" + token
			bound++
		}
	}
	if bound != 1 {
		return nil, fmt.Errorf("bootstrap environment requires one othbootargs entry")
	}
	return []byte(strings.Join(lines, "\n")), nil
}
