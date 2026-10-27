package installer

import (
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"syscall"
	"time"

	"golang.org/x/sys/unix"
)

type configurationSeeder interface {
	SeedConfiguration(string, Device, Request, []byte, []byte) error
}

func checkDisk(d Device) error {
	machine, err := MachineFor(d.Profile)
	if err != nil {
		return err
	}
	return machine.CheckDisk(d)
}

func TargetIdle(disk string) error {
	mounts, err := Mounts("/")
	if err != nil {
		return err
	}
	for _, m := range mounts {
		device, e := filepath.EvalSymlinks(m.Device)
		if e != nil {
			device = m.Device
		}
		if device == disk || ParentDisk(device) == disk {
			return fmt.Errorf("target still mounted: %s at %s", m.Device, m.Path)
		}
	}
	swaps, err := os.ReadFile("/proc/swaps")
	if err != nil {
		return err
	}
	for _, line := range strings.Split(string(swaps), "\n")[1:] {
		if strings.TrimSpace(line) != "" {
			return fmt.Errorf("disable swap before full-disk installation")
		}
	}
	// Ordinary Recovery's writable USB mass storage must never run alongside
	// the installer. Reject all active LUN backing files, including aliases.
	luns, _ := filepath.Glob("/sys/kernel/config/usb_gadget/*/functions/mass_storage.*/lun.*/file")
	for _, path := range luns {
		if ReadTrim(path) != "" {
			return fmt.Errorf("USB mass storage is active")
		}
	}
	return nil
}

func RecoverMMC(bootDevice string, report Reporter) (installed bool, err error) {
	if os.Getenv("ONEKVM_INITRAMFS") != "1" || os.Geteuid() != 0 {
		return false, fmt.Errorf("recover is only available in the installation initramfs")
	}
	if report == nil {
		report = Report
	}
	cmdline := ReadTrim("/proc/cmdline")
	disk := ParentDisk(bootDevice)
	boot := "/run/onekvm-install-boot"
	data := "/run/onekvm-install-data"
	ram := "/run/onekvm-install-ram"
	for _, path := range []string{boot, data, ram} {
		if err = os.MkdirAll(path, 0700); err != nil {
			return false, err
		}
	}
	for i := 0; i < 100; i++ {
		if _, e := os.Stat(bootDevice); e == nil {
			break
		}
		time.Sleep(100 * time.Millisecond)
	}
	if _, err = mountRecovery(bootDevice, boot, "vfat", syscall.MS_RDONLY|syscall.MS_NODEV|syscall.MS_NOSUID|syscall.MS_NOEXEC, ""); err != nil {
		return false, err
	}
	bootMounted := true
	defer func() {
		if bootMounted {
			_, _ = unmountRecovery(boot)
		}
	}()
	if _, e := os.Stat(filepath.Join(boot, "onekvm-install.started.json")); e == nil {
		return false, fmt.Errorf("previous installation was interrupted; manual recovery is required")
	}
	markerPath := filepath.Join(boot, MarkerName)
	if _, e := os.Stat(markerPath); os.IsNotExist(e) {
		if CmdlineValue(cmdline, "onekvm.installer") != "" {
			return false, fmt.Errorf("installation token is present but boot marker is missing; refusing normal startup")
		}
		return false, nil
	}
	var marker Marker
	if err = ReadJSON(markerPath, &marker); err != nil {
		return false, err
	}
	if marker.Version != RecoveryAPI || len(marker.Token) != 64 {
		return false, fmt.Errorf("invalid installer marker")
	}
	if CmdlineValue(cmdline, "onekvm.installer") != marker.Token {
		return false, fmt.Errorf("boot source is not bound to the installation request; kernel command-line token missing or different")
	}
	d := marker.Device
	if d.Boot != bootDevice || d.Disk != disk {
		return false, fmt.Errorf("installation marker targets another boot disk")
	}
	if err = checkDisk(d); err != nil {
		return false, err
	}
	if d.DataType != "exfat" && d.DataType != "ext4" && d.DataType != "f2fs" {
		return false, fmt.Errorf("unsupported userdata filesystem")
	}
	if _, err = mountRecovery(d.Userdata, data, d.DataType, syscall.MS_RDONLY|syscall.MS_NODEV|syscall.MS_NOSUID|syscall.MS_NOEXEC, ""); err != nil {
		return false, err
	}
	dataMounted := true
	defer func() {
		if dataMounted {
			_, _ = unmountRecovery(data)
		}
	}()
	staged := recoveryStagingDevice(d, data)
	path := StageDir(staged)
	hash, e := HashFile(filepath.Join(path, "request.json"), nil)
	if e != nil {
		return false, e
	}
	if hash != marker.RequestSHA256 {
		return false, fmt.Errorf("installation request changed")
	}
	var req Request
	err = report("Rechecking staged image and configuration", func(p Progress) error { var e error; req, e = LoadPrepared(staged, p); return e })
	if err != nil {
		return false, err
	}
	if req.Token != marker.Token || req.State != "armed" {
		return false, fmt.Errorf("installation is not armed")
	}
	mem, e := Memory("MemAvailable")
	if e != nil {
		return false, e
	}
	imageFile, e := os.Stat(filepath.Join(path, "image.img"))
	if e != nil {
		return false, e
	}
	if imageFile.Size() != req.ImageBytes || req.ImageBytes <= 0 || req.ImageBytes+RAMReserve > mem {
		return false, fmt.Errorf("not enough available RAM for complete image; target has not been erased")
	}
	if _, err = mountRecovery("tmpfs", ram, "tmpfs", 0, fmt.Sprintf("mode=0700,size=%d", req.ImageBytes+MiB)); err != nil {
		return false, err
	}
	defer func() { _, _ = unmountRecovery(ram) }()
	cache := filepath.Join(ram, "image.img")
	err = report("Loading complete image from userdata into RAM", func(p Progress) error {
		s, e := os.Open(filepath.Join(path, "image.img"))
		if e != nil {
			return e
		}
		defer s.Close()
		t, e := os.OpenFile(cache, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0600)
		if e != nil {
			return e
		}
		defer t.Close()
		hash, e := copyRawImage(t, s, req.ImageBytes, p)
		if e != nil {
			return e
		}
		if hash != req.FileSHA256 {
			return fmt.Errorf("RAM copy checksum mismatch")
		}
		return nil
	})
	if err != nil {
		return false, err
	}
	var verified Image
	err = report("Validating complete RAM image", func(p Progress) error {
		var e error
		verified, e = Verify(cache, d.Profile, min(d.Bytes, 4*1024*MiB), p)
		return e
	})
	if err != nil {
		return false, err
	}
	if verified != req.Image {
		return false, fmt.Errorf("RAM image differs from installation request")
	}
	firstboot, e := os.ReadFile(filepath.Join(path, "firstboot.txt"))
	if e != nil {
		return false, e
	}
	if len(firstboot) > 65536 {
		return false, fmt.Errorf("oversized firstboot configuration")
	}
	fhash := sha256.Sum256(firstboot)
	if hex.EncodeToString(fhash[:]) != req.FirstbootSHA256 {
		return false, fmt.Errorf("firstboot configuration changed")
	}
	var config, hook []byte
	var seed configurationSeeder
	if req.ConfigSHA256 != "" {
		config, e = loadSystemConfig(path, req.ConfigSHA256)
		if e != nil {
			return false, e
		}
		defer clear(config)
		hook, e = ConfigurationHook(req)
		if e != nil {
			return false, e
		}
		machine, e := MachineFor(d.Profile)
		if e != nil {
			return false, e
		}
		var ok bool
		if seed, ok = machine.(configurationSeeder); !ok {
			return false, fmt.Errorf("configuration restoration unavailable for target")
		}
	}
	// Mark as started only after everything needed is in RAM. An interrupted
	// write must stop instead of replaying the request or normal firstboot.
	if _, err = mountRecovery(d.Userdata, data, d.DataType, syscall.MS_REMOUNT, ""); err != nil {
		return false, err
	}
	req.State = "writing"
	if err = WriteJSON(filepath.Join(path, "request.json"), req); err != nil {
		return false, err
	}
	if _, err = mountRecovery(bootDevice, boot, "vfat", syscall.MS_REMOUNT, ""); err != nil {
		return false, err
	}
	if err = os.Rename(markerPath, filepath.Join(boot, "onekvm-install.started.json")); err != nil {
		return false, err
	}
	if err = SyncDir(boot); err != nil {
		return false, err
	}
	if _, err = unmountRecovery(data); err != nil {
		return false, err
	}
	dataMounted = false
	if _, err = unmountRecovery(boot); err != nil {
		return false, err
	}
	bootMounted = false
	if err = TargetIdle(disk); err != nil {
		return false, err
	}
	if err = checkDisk(d); err != nil {
		return false, err
	}
	target, e := os.OpenFile(disk, os.O_RDWR|syscall.O_EXCL, 0)
	if e != nil {
		return false, e
	}
	defer target.Close()
	s, e := target.Stat()
	if e != nil {
		return false, e
	}
	if s.Mode()&os.ModeDevice == 0 || s.Mode()&os.ModeCharDevice != 0 {
		return false, fmt.Errorf("target is not a whole block device")
	}
	if err = syscall.Flock(int(target.Fd()), syscall.LOCK_EX|syscall.LOCK_NB); err != nil {
		return false, err
	}
	// eMMC's cmdline partition map survives the image write. Invalidate its
	// old userdata filesystem so firstboot formats it instead of retaining
	// extensions and personal files in either reinstallation mode.
	if d.Reinstall && d.Profile == "pro-emmc" {
		if _, err = target.Seek(d.DataStart*512, 0); err != nil {
			return false, err
		}
		if _, err = target.Write(make([]byte, MiB)); err != nil {
			return false, err
		}
		if err = target.Sync(); err != nil {
			return false, err
		}
	}
	r, e := OpenImage(cache)
	if e != nil {
		return false, e
	}
	defer r.Close()
	err = report("Writing SD image and verifying disk readback", func(p Progress) error { return WritePayload(target, r, verified, p) })
	if err != nil {
		return false, err
	}
	// Close the exclusive descriptor before rereading and mounting partitions.
	if err = target.Close(); err != nil {
		return false, err
	}
	if verified.BootStart == d.BootStart && verified.BootSectors == d.BootSectors {
		// Factory cmdline partitions remain visible until reboot.
	} else {
		if _, err = rereadRecovery(disk); err != nil {
			return false, err
		}
		for i := 0; i < 100; i++ {
			count, _ := ReadNumber("/sys/class/block/" + filepath.Base(bootDevice) + "/size")
			if count == verified.BootSectors {
				break
			}
			time.Sleep(100 * time.Millisecond)
		}
		count, e := ReadNumber("/sys/class/block/" + filepath.Base(bootDevice) + "/size")
		if e != nil || count != verified.BootSectors {
			return false, fmt.Errorf("new boot partition did not appear")
		}
	}
	if _, err = mountRecovery(bootDevice, boot, "vfat", syscall.MS_NODEV|syscall.MS_NOSUID|syscall.MS_NOEXEC, ""); err != nil {
		return false, err
	}
	bootMounted = true
	if req.ConfigSHA256 != "" {
		if err = seed.SeedConfiguration(boot, d, req, config, hook); err != nil {
			return false, err
		}
	} else if err = WritePrivate(filepath.Join(boot, "firstboot.txt"), firstboot); err != nil {
		return false, err
	}
	if _, err = unmountRecovery(boot); err != nil {
		return false, err
	}
	bootMounted = false
	machine, err := MachineFor(d.Profile)
	if err != nil {
		return false, err
	}
	if finalizer, ok := machine.(interface{ FinalizeRecovery(Device) error }); ok {
		if err = report("op.boot_priority", func(Progress) error { return finalizer.FinalizeRecovery(d) }); err != nil {
			return false, err
		}
	}
	return true, nil
}

func recoveryStagingDevice(d Device, mount string) Device {
	d.DataMount = filepath.Join(mount, strings.TrimPrefix(d.DataRoot, "/"))
	return d
}

// These syscalls also work in the self-contained reinstallation initramfs.
func mountRecovery(source, target, kind string, flags uintptr, data string) (string, error) {
	return "", syscall.Mount(source, target, kind, flags, data)
}
func unmountRecovery(target string) (string, error) { return "", syscall.Unmount(target, 0) }
func rereadRecovery(disk string) (string, error) {
	f, err := os.OpenFile(disk, os.O_RDWR, 0)
	if err != nil {
		return "", err
	}
	defer f.Close()
	return "", unix.IoctlSetInt(int(f.Fd()), unix.BLKRRPART, 0)
}
