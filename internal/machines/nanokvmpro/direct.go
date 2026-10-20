package nanokvmpro

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"syscall"
	"time"

	"github.com/onekvm/onekvm-installer/internal/installer"
	"golang.org/x/sys/unix"
)

// Hold the metadata trailer back while streaming the image to the SD card.
type payloadWriter struct {
	target  io.Writer
	tail    []byte
	written int64
}

func (w *payloadWriter) Write(p []byte) (int, error) {
	n := len(p)
	combined := append(w.tail, p...)
	if len(combined) > 512 {
		data := combined[:len(combined)-512]
		count, err := w.target.Write(data)
		w.written += int64(count)
		if err != nil {
			return 0, err
		}
		if count != len(data) {
			return 0, io.ErrShortWrite
		}
	}
	w.tail = bytes.Clone(combined[max(0, len(combined)-512):])
	return n, nil
}

func streamSD(target io.ReadWriteSeeker, source io.Reader, size int64, digest string, progress installer.Progress) (installer.Image, error) {
	if _, err := target.Seek(0, io.SeekStart); err != nil {
		return installer.Image{}, err
	}
	writer := &payloadWriter{target: target}
	hash := sha256.New()
	reader := installer.WithProgress(io.TeeReader(io.LimitReader(source, size+1), io.MultiWriter(writer, hash)), size, progress)
	info, err := installer.VerifyStream(reader, "pro-sd", size)
	if err != nil {
		return installer.Image{}, err
	}
	if info.PayloadSize+512 != size || writer.written != info.PayloadSize {
		return installer.Image{}, fmt.Errorf("incomplete or oversized download")
	}
	if digest != "" && digest != "sha256:"+hex.EncodeToString(hash.Sum(nil)) {
		return installer.Image{}, fmt.Errorf("GitHub asset SHA-256 mismatch")
	}
	if f, ok := target.(interface{ Sync() error }); ok {
		if err = f.Sync(); err != nil {
			return installer.Image{}, err
		}
	}
	if _, err = target.Seek(0, io.SeekStart); err != nil {
		return installer.Image{}, err
	}
	readback := io.MultiReader(io.LimitReader(target, info.PayloadSize), bytes.NewReader(writer.tail))
	checked, err := installer.VerifyStream(installer.WithProgress(readback, size, progress), "pro-sd", size)
	if err != nil {
		return installer.Image{}, fmt.Errorf("SD readback: %w", err)
	}
	if checked != info {
		return installer.Image{}, fmt.Errorf("disk readback metadata mismatch")
	}
	return info, nil
}

func (m Machine) InstallDirect(ctx context.Context, d installer.Device, source string, asset *installer.ReleaseAsset, firstboot []byte, report installer.Reporter) (installer.AccessInfo, error) {
	var access installer.AccessInfo
	if m.media != "sd" {
		return access, fmt.Errorf("eMMC installation requires recovery")
	}
	if err := installer.CheckInstallTarget("/"); err != nil {
		return access, err
	}
	var local installer.Image
	var localFile *os.File
	defer func() {
		if localFile != nil {
			localFile.Close()
		}
	}()
	err := report("op.direct_check", func(p installer.Progress) error {
		if err := ctx.Err(); err != nil {
			return err
		}
		current, err := m.Probe("/")
		if err != nil {
			return err
		}
		if !installer.SameDisk(d, current) {
			return fmt.Errorf("Pro target changed")
		}
		if err = m.CheckDisk(d); err != nil {
			return err
		}
		if _, err = installer.AccessFromFirstboot(firstboot); err != nil {
			return err
		}
		if asset == nil {
			localFile, err = openLocalImage(source, d.Disk)
			if err != nil {
				return err
			}
			st, err := localFile.Stat()
			if err != nil {
				return err
			}
			local, err = installer.VerifyStream(installer.WithProgress(localFile, st.Size(), p), d.Profile, min(d.Bytes, 4*1024*installer.MiB))
			if err != nil {
				return err
			}
		} else if asset.Size <= 512 || asset.Size > d.Bytes {
			return fmt.Errorf("release asset exceeds target capacity")
		}
		return ctx.Err()
	})
	if err != nil {
		return access, err
	}
	// The first potentially destructive step is reported before unmounting.
	err = report("op.direct_write", func(p installer.Progress) error {
		if err := m.CheckDisk(d); err != nil {
			return err
		}
		if err := detachSD(d.Disk); err != nil {
			return err
		}
		target, err := os.OpenFile(d.Disk, os.O_RDWR|syscall.O_EXCL, 0)
		if err != nil {
			return err
		}
		defer target.Close()
		stat, err := target.Stat()
		if err != nil {
			return err
		}
		if stat.Mode()&os.ModeDevice == 0 || stat.Mode()&os.ModeCharDevice != 0 {
			return fmt.Errorf("target is not a whole block device")
		}
		if err = syscall.Flock(int(target.Fd()), syscall.LOCK_EX|syscall.LOCK_NB); err != nil {
			return err
		}
		if asset != nil {
			r, err := installer.OpenReleaseStream(context.WithoutCancel(ctx), *asset)
			if err != nil {
				return err
			}
			defer r.Close()
			local, err = streamSD(target, r, asset.Size, asset.Digest, p)
			return err
		}
		if _, err = localFile.Seek(0, io.SeekStart); err != nil {
			return err
		}
		return m.WritePayload(target, localFile, local, p)
	})
	if err != nil {
		return access, err
	}
	err = report("op.direct_config", func(installer.Progress) error {
		if _, err := installer.Run("blockdev", "--rereadpt", d.Disk); err != nil {
			return err
		}
		part := d.Disk + "p1"
		sys := "/sys/class/block/" + filepath.Base(part)
		found := false
		for i := 0; i < 100; i++ {
			start, e := installer.ReadNumber(sys + "/start")
			sectors, e2 := installer.ReadNumber(sys + "/size")
			if e == nil && e2 == nil && start == local.BootStart && sectors == local.BootSectors {
				found = true
				break
			}
			time.Sleep(100 * time.Millisecond)
		}
		if !found {
			return fmt.Errorf("new SD boot partition did not appear")
		}
		mount, err := os.MkdirTemp("/run", "onekvm-sd-boot-")
		if err != nil {
			return err
		}
		defer os.Remove(mount)
		if err = syscall.Mount(part, mount, "vfat", syscall.MS_NODEV|syscall.MS_NOSUID|syscall.MS_NOEXEC, ""); err != nil {
			return err
		}
		mounted := true
		defer func() {
			if mounted {
				_ = syscall.Unmount(mount, 0)
			}
		}()
		if err = installer.WritePrivate(filepath.Join(mount, "firstboot.txt"), firstboot); err != nil {
			return err
		}
		if err = validateProSDCard(mount); err != nil {
			return err
		}
		if err = syscall.Unmount(mount, 0); err != nil {
			return err
		}
		mounted = false
		return nil
	})
	if err != nil {
		return access, err
	}
	// SD-first selection belongs to the primary SPL. Writing an SD image must
	// not replace the factory eMMC U-Boot policy or its kernel arguments.
	return installer.AccessFromFirstboot(firstboot)
}

func detachSD(disk string) error {
	mounts, err := installer.Mounts("/")
	if err != nil {
		return err
	}
	sort.Slice(mounts, func(i, j int) bool { return len(mounts[i].Path) > len(mounts[j].Path) })
	for _, m := range mounts {
		device, _ := filepath.EvalSymlinks(m.Device)
		if device == "" {
			device = m.Device
		}
		if device != disk && installer.ParentDisk(device) != disk {
			continue
		}
		switch m.Path {
		case "/", "/boot", "/usr", "/var", "/home":
			return fmt.Errorf("SD contains an active system mount: %s", m.Path)
		}
		if strings.HasPrefix(m.Path, "/proc/") || strings.HasPrefix(m.Path, "/sys/") {
			return fmt.Errorf("unsafe SD mountpoint")
		}
		if err = syscall.Unmount(m.Path, 0); err != nil {
			return fmt.Errorf("unmount SD %s: %w", m.Path, err)
		}
	}
	return installer.TargetIdle(disk)
}

func validateProSDCard(boot string) error {
	for _, name := range []string{"boot.scr", "boot-a.scr", "boot-b.scr", "Image-a", "ax630c-a.dtb", "initramfs-a.uimg", "bootgrow"} {
		st, err := os.Stat(filepath.Join(boot, name))
		if err != nil {
			return err
		}
		if !st.Mode().IsRegular() || st.Size() == 0 {
			return fmt.Errorf("missing Pro SD boot file: %s", name)
		}
	}
	if installer.ReadTrim(filepath.Join(boot, "bootgrow")) != "128" {
		return fmt.Errorf("image lacks the 128 MiB installed boot layout marker")
	}
	return nil
}

func openLocalImage(path, disk string) (*os.File, error) {
	if !strings.EqualFold(filepath.Ext(path), ".img") {
		return nil, fmt.Errorf("only raw .img images are supported")
	}
	f, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	fail := func(e error) (*os.File, error) { f.Close(); return nil, e }
	st, err := f.Stat()
	if err != nil {
		return fail(err)
	}
	if !st.Mode().IsRegular() {
		return fail(fmt.Errorf("image must be a regular .img file"))
	}
	stat, ok := st.Sys().(*syscall.Stat_t)
	if !ok {
		return fail(fmt.Errorf("cannot identify local image filesystem"))
	}
	backing, err := filepath.EvalSymlinks(fmt.Sprintf("/sys/dev/block/%d:%d", unix.Major(uint64(stat.Dev)), unix.Minor(uint64(stat.Dev))))
	// Non-block filesystems (tmpfs) have no sysfs entry.
	if err != nil && !os.IsNotExist(err) {
		return fail(err)
	}
	device := "/dev/" + filepath.Base(backing)
	if device == disk || installer.ParentDisk(device) == disk {
		return fail(fmt.Errorf("local image is stored on the target SD card; move it to eMMC or use GitHub"))
	}
	return f, nil
}
