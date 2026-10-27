package nanokvmpro

import (
	"bytes"
	"compress/gzip"
	"crypto/sha256"
	"encoding/binary"
	"encoding/hex"
	"fmt"
	"hash/crc32"
	"io"
	"os"
	"path/filepath"
	"strings"
	"syscall"

	"github.com/onekvm/onekvm-installer/internal/installer"
)

const proFactoryPartitions = "blkdevparts=mmcblk0:768K(spl),512K(ddrinit),256K(atf),256K(atf_b),1536K(uboot),1536K(uboot_b),1M(env),6M(logo),6M(logo_b),1M(optee),1M(optee_b),1M(dtb),1M(dtb_b),64M(kernel),64M(kernel_b),128M(boot),-(rootfs)"

func extractProBootstrap(image, dir, profile string, p installer.Progress) error {
	r, err := os.Open(image)
	if err != nil {
		return err
	}
	defer r.Close()
	// Only eMMC requires an installation initramfs. SD is written directly.
	if profile != "pro-emmc" {
		return fmt.Errorf("Pro SD does not require a recovery bootstrap")
	}
	fat := io.NewSectionReader(r, proBootStart*512, proBootSectors*512)
	api, err := installer.FatFile(fat, "instapi", 16)
	if err != nil {
		return err
	}
	if string(api) != fmt.Sprintf("%d\n", installer.RecoveryAPI) {
		return fmt.Errorf("unsupported installation recovery API")
	}
	for name, spec := range map[string]struct {
		file  string
		limit int64
	}{"Image": {"Image-a", 32 * installer.MiB}, "board.dtb": {"ax630c-a.dtb", 8 * installer.MiB}, "initramfs.uimg": {"initramfs-a.uimg", proSelector - (proBackupKernel + 40*installer.MiB)}} {
		payload, err := installer.FatFileProgress(fat, spec.file, spec.limit, p)
		if err != nil {
			return err
		}
		if err = validateBootstrapPayload(name, payload); err != nil {
			return err
		}
		if err = installer.WritePrivate(filepath.Join(dir, name), payload); err != nil {
			return err
		}
	}
	return nil
}

// U-Boot legacy script format, including the script length table and both CRCs.
func proScript(text string) ([]byte, error) {
	for _, line := range strings.Split(text, "\n") {
		if len(strings.Fields(line)) > 16 {
			return nil, fmt.Errorf("Pro boot script exceeds the stock parser argument limit")
		}
	}
	data := make([]byte, 8)
	binary.BigEndian.PutUint32(data, uint32(len(text)))
	data = append(data, []byte(text)...)
	header := make([]byte, 64)
	binary.BigEndian.PutUint32(header, 0x27051956)
	binary.BigEndian.PutUint32(header[12:], uint32(len(data)))
	binary.BigEndian.PutUint32(header[24:], crc32.ChecksumIEEE(data))
	header[28], header[29], header[30] = 5, 22, 6
	copy(header[32:], "OneKVM Pro installation")
	binary.BigEndian.PutUint32(header[4:], crc32.ChecksumIEEE(header))
	return append(header, data...), nil
}
func bindProBootstrap(dir string, req installer.Request) error {
	if req.Device.Reinstall {
		return bindReinstallBootstrap(dir, req)
	}
	if req.Device.Profile != "pro-emmc" || len(req.Token) != 64 {
		return fmt.Errorf("invalid Pro installation bootstrap")
	}
	if _, err := hex.DecodeString(req.Token); err != nil {
		return err
	}
	kernel, err := os.ReadFile(filepath.Join(dir, "Image"))
	if err != nil {
		return err
	}
	address, command, err := proKernelBoot(kernel)
	if err != nil {
		return err
	}
	var script strings.Builder
	script.WriteString("mmc dev 0\nmmc rescan\n")
	for _, entry := range []struct {
		name, address string
		offset, limit int64
	}{{"Image", address, 0, 32 * installer.MiB}, {"board.dtb", "0x47000000", 32 * installer.MiB, 8 * installer.MiB}, {"initramfs.uimg", "0x49000000", 40 * installer.MiB, proSelector - (proBackupKernel + 40*installer.MiB)}} {
		stat, err := os.Stat(filepath.Join(dir, entry.name))
		if err != nil {
			return err
		}
		if stat.Size() <= 0 || stat.Size() > entry.limit {
			return fmt.Errorf("Pro recovery payload exceeds raw bootstrap range")
		}
		fmt.Fprintf(&script, "mmc read %s 0x%x 0x%x\n", entry.address, (proBackupKernel+entry.offset)/512, (stat.Size()+511)/512)
	}
	fmt.Fprintf(&script, "setenv bootargs root=/dev/mmcblk0p17 rootwait ro console=ttyS0,115200 earlycon=uart8250,mmio32,0x04880000 clk_ignore_unused %s onekvm.installer=%s\n", proFactoryPartitions, req.Token)
	fmt.Fprintf(&script, "setenv initrd_high 0xffffffffffffffff\n%s %s 0x49000000 0x47000000\n", command, address)
	payload, err := proScript(script.String())
	if err != nil {
		return err
	}
	return installer.WritePrivate(filepath.Join(dir, "install.scr"), payload)
}

func activateProRecovery(d installer.Device, req installer.Request, report installer.Reporter) error {
	if d.Profile == "pro-sd" && d.Reinstall {
		return activateSDReinstall(d, req, report)
	}
	if d.Profile != "pro-emmc" {
		return fmt.Errorf("SD installation must use the direct writer")
	}
	var env []byte
	err := report("op.boot_check", func(installer.Progress) error {
		if err := checkProDisk(d); err != nil {
			return err
		}
		var err error
		if d.Reinstall {
			return nil
		}
		env, err = proReadEnvironment()
		return err
	})
	if err != nil {
		return err
	}
	dir := installer.StageDir(d)
	original := req
	incomplete := func(cause error) error {
		original.State = "arming"
		if e := installer.WriteJSON(filepath.Join(dir, "request.json"), original); e != nil {
			return fmt.Errorf("Pro recovery activation failed: %w (saving retry state: %v)", cause, e)
		}
		return cause
	}
	err = report("op.boot_cleanup", func(installer.Progress) error {
		original.State = "arming"
		return installer.WriteJSON(filepath.Join(dir, "request.json"), original)
	})
	if err != nil {
		return err
	}
	err = report("Installing recovery boot files (keep power connected)", func(p installer.Progress) error {
		// Factory slot A remains bootable until the final environment switch.
		f, err := os.OpenFile("/dev/mmcblk0", os.O_RDWR, 0)
		if err != nil {
			return err
		}
		defer f.Close()
		if err = syscall.Flock(int(f.Fd()), syscall.LOCK_EX|syscall.LOCK_NB); err != nil {
			return err
		}
		for _, entry := range []struct {
			name          string
			offset, limit int64
		}{{"Image", proBackupKernel, 32 * installer.MiB}, {"board.dtb", proBackupKernel + 32*installer.MiB, 8 * installer.MiB}, {"initramfs.uimg", proBackupKernel + 40*installer.MiB, proSelector - (proBackupKernel + 40*installer.MiB)}} {
			if err = writeProRawFile(f, filepath.Join(dir, entry.name), entry.offset, entry.limit, req.BootFiles[entry.name], p); err != nil {
				return err
			}
		}
		req.State = "armed"
		body, err := installer.JsonBytes(req)
		if err != nil {
			return err
		}
		hash := sha256.Sum256(body)
		marker := installer.Marker{Version: installer.RecoveryAPI, Token: req.Token, Device: d, RequestSHA256: hex.EncodeToString(hash[:])}
		if err = installer.WritePrivate(filepath.Join(dir, "request.json"), body); err != nil {
			return err
		}
		if err = installer.WriteJSON(filepath.Join(d.BootMount, installer.MarkerName), marker); err != nil {
			return err
		}
		return writeProRawFile(f, filepath.Join(dir, "install.scr"), proSelector, 65536, req.BootFiles["install.scr"], p)
	})
	if err != nil {
		return incomplete(err)
	}
	if d.Reinstall {
		return nil
	}
	err = report("op.boot_activate", func(installer.Progress) error { return proSavePolicy(d, env, true) })
	if err != nil {
		return incomplete(err)
	}
	return nil
}

func writeProRawFile(f interface {
	io.ReaderAt
	io.WriterAt
	Sync() error
}, path string, offset, limit int64, expected string, p installer.Progress) error {
	payload, err := os.ReadFile(path)
	if err != nil {
		return err
	}
	hash := sha256.Sum256(payload)
	if len(payload) == 0 || int64(len(payload)) > limit || hex.EncodeToString(hash[:]) != expected {
		return fmt.Errorf("Pro raw bootstrap changed: %s", filepath.Base(path))
	}
	// Round reads in the stock mmc command to full sectors, with deterministic padding.
	padded := append(bytes.Clone(payload), make([]byte, (512-len(payload)%512)%512)...)
	if int64(len(padded)) > limit {
		return fmt.Errorf("Pro raw bootstrap exceeds reserved range")
	}
	n, err := f.WriteAt(padded, offset)
	if err != nil {
		return err
	}
	if n != len(padded) {
		return io.ErrShortWrite
	}
	if err = f.Sync(); err != nil {
		return err
	}
	readback := make([]byte, len(padded))
	if _, err = f.ReadAt(readback, offset); err != nil {
		return err
	}
	if !bytes.Equal(padded, readback) {
		return fmt.Errorf("Pro raw bootstrap readback mismatch")
	}
	if p != nil {
		p(int64(len(payload)), int64(len(payload)))
	}
	return nil
}

func validateBootstrapPayload(name string, p []byte) error {
	switch name {
	case "Image":
		_, _, err := proKernelBoot(p)
		return err
	case "board.dtb":
		if len(p) < 40 || binary.BigEndian.Uint32(p) != 0xd00dfeed || int(binary.BigEndian.Uint32(p[4:])) != len(p) {
			return fmt.Errorf("invalid Pro recovery DTB")
		}
	case "initramfs.uimg":
		if len(p) < 64 || binary.BigEndian.Uint32(p) != 0x27051956 || int(binary.BigEndian.Uint32(p[12:])) != len(p)-64 || p[29] != 22 || p[30] != 3 || p[31] != 1 {
			return fmt.Errorf("invalid Pro recovery ramdisk")
		}
		header := bytes.Clone(p[:64])
		want := binary.BigEndian.Uint32(header[4:])
		clear(header[4:8])
		if crc32.ChecksumIEEE(header) != want || crc32.ChecksumIEEE(p[64:]) != binary.BigEndian.Uint32(p[24:]) {
			return fmt.Errorf("Pro recovery ramdisk CRC mismatch")
		}
	}
	return nil
}

// Older images carry a raw Image; new images use a gzip legacy uImage.
func proKernelBoot(p []byte) (address, command string, err error) {
	invalid := func() (string, string, error) {
		return "", "", fmt.Errorf("invalid ARM64 Pro recovery kernel")
	}
	if len(p) < 64 || len(p) > 32*installer.MiB {
		return invalid()
	}
	if string(p[56:60]) == "ARM\x64" {
		return "0x41000000", "booti", nil
	}
	if len(p) > 18*installer.MiB || binary.BigEndian.Uint32(p) != 0x27051956 ||
		int(binary.BigEndian.Uint32(p[12:])) != len(p)-64 ||
		binary.BigEndian.Uint32(p[16:]) != 0x41000000 ||
		binary.BigEndian.Uint32(p[20:]) != 0x41000000 ||
		p[28] != 5 || p[29] != 22 || p[30] != 2 || p[31] != 1 {
		return invalid()
	}
	header := bytes.Clone(p[:64])
	want := binary.BigEndian.Uint32(header[4:])
	clear(header[4:8])
	if crc32.ChecksumIEEE(header) != want || crc32.ChecksumIEEE(p[64:]) != binary.BigEndian.Uint32(p[24:]) {
		return invalid()
	}
	z, err := gzip.NewReader(bytes.NewReader(p[64:]))
	if err != nil {
		return invalid()
	}
	defer z.Close()
	raw, err := io.ReadAll(io.LimitReader(z, 32*installer.MiB+1))
	if err != nil || len(raw) < 64 || len(raw) > 32*installer.MiB ||
		string(raw[56:60]) != "ARM\x64" || binary.LittleEndian.Uint64(raw[8:]) != 0 ||
		binary.LittleEndian.Uint64(raw[16:]) == 0 || binary.LittleEndian.Uint64(raw[16:]) > 32*installer.MiB {
		return invalid()
	}
	return "0x43000000", "bootm", nil
}

func (Machine) RecoverySteps() []string {
	return []string{"op.recheck", "op.boot_check", "op.boot_cleanup", "op.arm", "op.boot_activate"}
}
