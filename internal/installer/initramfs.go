package installer

import (
	"bytes"
	"compress/gzip"
	"debug/elf"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"runtime"
	"syscall"
	"time"
)

// RecoveryInitramfs carries this exact installer as PID 1. Filesystem and MMC
// drivers are built into the supported OneKVM kernels. No target userspace,
// shell, modules, or image-embedded installer version is needed to erase it.
func RecoveryInitramfs() ([]byte, error) { return RecoveryInitramfsWithBase(nil) }

// A verified image's native archive can supply its matching display drivers
// and artwork. Our current installer always replaces its /init and controls
// installation; none of the image's boot scripts run in this environment.
func RecoveryInitramfsWithBase(base []byte) ([]byte, error) {
	path, err := os.Executable()
	if err != nil {
		return nil, err
	}
	f, err := elf.Open(path)
	if err != nil {
		return nil, err
	}
	defer f.Close()
	want := map[string]elf.Machine{"riscv64": elf.EM_RISCV, "arm64": elf.EM_AARCH64}[runtime.GOARCH]
	if want == elf.EM_NONE || f.Machine != want {
		return nil, fmt.Errorf("unsupported recovery executable architecture")
	}
	for _, p := range f.Progs {
		if p.Type == elf.PT_INTERP {
			return nil, fmt.Errorf("recovery requires a static installer executable")
		}
	}
	payload, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	archive, err := recoveryArchive(payload)
	if err != nil || len(base) == 0 {
		return archive, err
	}
	return appendRecoveryArchive(base, archive)
}

func appendRecoveryArchive(base, overlay []byte) ([]byte, error) {
	z, err := gzip.NewReader(bytes.NewReader(base))
	if err != nil {
		return nil, fmt.Errorf("invalid native recovery archive: %w", err)
	}
	defer z.Close()
	// Bound decompression before giving the native archive to the kernel.
	n, err := io.Copy(io.Discard, io.LimitReader(z, 64*MiB+1))
	if err != nil || n > 64*MiB {
		return nil, fmt.Errorf("invalid or oversized native recovery archive: %v", err)
	}
	result := make([]byte, 0, len(base)+4+len(overlay))
	result = append(result, base...)
	result = append(result, 0, 0, 0, 0)
	return append(result, overlay...), nil
}

type archiveFile struct {
	name string
	mode uint32
	data []byte
}

func recoveryArchive(payload []byte) ([]byte, error) {
	return compressedArchive([]archiveFile{{"init", syscall.S_IFREG | 0755, payload}})
}
func compressedArchive(files []archiveFile) ([]byte, error) {
	var raw bytes.Buffer
	entry := func(name string, mode uint32, data []byte) {
		fmt.Fprintf(&raw, "070701%08x%08x%08x%08x%08x%08x%08x%08x%08x%08x%08x%08x%08x",
			1, mode, 0, 0, 1, 0, len(data), 0, 0, 0, 0, len(name)+1, 0)
		raw.WriteString(name)
		raw.WriteByte(0)
		for raw.Len()%4 != 0 {
			raw.WriteByte(0)
		}
		raw.Write(data)
		for raw.Len()%4 != 0 {
			raw.WriteByte(0)
		}
	}
	for _, file := range files {
		entry(file.name, file.mode, file.data)
	}
	entry("TRAILER!!!", 0, nil)
	var compressed bytes.Buffer
	g, err := gzip.NewWriterLevel(&compressed, gzip.BestCompression)
	if err != nil {
		return nil, err
	}
	if _, err = g.Write(raw.Bytes()); err != nil {
		return nil, err
	}
	if err = g.Close(); err != nil {
		return nil, err
	}
	return compressed.Bytes(), nil
}

// InitramfsMain never returns on failure: PID 1 must keep the recovery kernel
// alive, with the request and source medium available for diagnosis.
func InitramfsMain() {
	var show func(string)
	err := initramfsInstall(&show)
	if show != nil {
		show("error")
	}
	fmt.Fprintln(os.Stderr, "OneKVM installation stopped; keep power connected:", err)
	for {
		time.Sleep(time.Hour)
	}
}

func initramfsInstall(display *func(string)) error {
	for _, m := range []struct{ source, path, kind string }{
		{"proc", "/proc", "proc"}, {"sysfs", "/sys", "sysfs"},
		{"devtmpfs", "/dev", "devtmpfs"}, {"tmpfs", "/run", "tmpfs"},
	} {
		if err := os.MkdirAll(m.path, 0755); err != nil {
			return err
		}
		if err := syscall.Mount(m.source, m.path, m.kind, 0, ""); err != nil {
			return err
		}
	}
	// The minimal archive has no device nodes for the kernel to open before
	// starting PID 1. Attach the console after devtmpfs supplies them.
	console, err := os.OpenFile("/dev/console", os.O_RDWR, 0)
	if err != nil {
		return err
	}
	os.Stdin, os.Stdout, os.Stderr = console, console, console
	if err := os.Setenv("ONEKVM_INITRAMFS", "1"); err != nil {
		return err
	}
	for _, machine := range Machines() {
		if screen, ok := machine.(interface{ RecoveryDisplay() func(string) }); ok {
			if *display = screen.RecoveryDisplay(); *display != nil {
				break
			}
		}
	}
	show := func(state string) {
		if *display != nil {
			(*display)(state)
		}
	}
	show("preparing")
	// An initramfs bypasses the kernel's rootwait handling. MMC probing may
	// still be asynchronous when machine recovery checks its medium in sysfs.
	if err := waitRecoveryMMC("/", 10*time.Second); err != nil {
		return err
	}
	unlock, err := Lock(LockPath)
	if err != nil {
		return err
	}
	defer unlock()
	installed, err := Recover(func(label string, op func(Progress) error) error {
		fmt.Fprintln(os.Stderr, label)
		if label == "Writing SD image and verifying disk readback" {
			show("writing")
		}
		err := op(nil)
		if err == nil && label == "Writing SD image and verifying disk readback" {
			show("finishing")
		}
		return err
	})
	if err != nil {
		return err
	}
	if !installed {
		return fmt.Errorf("no bound installation request")
	}
	fmt.Fprintln(os.Stderr, "OneKVM image installed and verified. Rebooting for first boot.")
	show("finishing")
	syscall.Sync()
	return syscall.Reboot(syscall.LINUX_REBOOT_CMD_RESTART)
}

func waitRecoveryMMC(root string, timeout time.Duration) error {
	partition := CmdlineValue(ReadTrim(filepath.Join(root, "proc/cmdline")), "root")
	disk := ParentDisk(partition)
	if disk == "" || partition != "/dev/"+filepath.Base(partition) {
		return fmt.Errorf("invalid MMC recovery root: %q", partition)
	}
	deadline := time.Now().Add(timeout)
	for {
		media := ReadTrim(filepath.Join(root, "sys/class/block", filepath.Base(disk), "device/type"))
		if _, err := os.Stat(filepath.Join(root, partition)); err == nil && (media == "SD" || media == "MMC") {
			return nil
		}
		if !time.Now().Before(deadline) {
			return fmt.Errorf("timed out waiting for MMC recovery root %s; target has not been erased", partition)
		}
		time.Sleep(min(100*time.Millisecond, time.Until(deadline)))
	}
}
