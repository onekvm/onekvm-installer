package nanokvmpro

import (
	"crypto/sha256"
	"encoding/binary"
	"encoding/hex"
	"fmt"
	"hash/crc32"
	"io"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"syscall"

	"github.com/onekvm/onekvm-installer/internal/installer"
)

func (m Machine) probeInstalled(root string) (installer.Device, error) {
	d, err := installer.ProbeMMC(root)
	if err != nil {
		return d, err
	}
	if !proMatches(root) {
		return d, fmt.Errorf("unrecognized NanoKVM Pro")
	}
	model := strings.ToLower(d.Model)
	if strings.Contains(model, "atx") || strings.Contains(model, "pcie") {
		return d, fmt.Errorf("Pro PCIe requires its own DTB; current installation images support Pro Desk")
	}
	d.Profile = m.Profile()
	d.HardwareModel = installer.ReadMachineIdentity(root, installer.MachineIdentity{Machine: "nanokvm-pro", Vendor: "Sipeed", Name: "NanoKVM Pro", Variant: "Desk"}).ModelLabel()
	d.Automatic = installer.RootDisk(root) == d.Disk
	return d, m.ValidateTarget(d)
}

func (m Machine) validateInstalled(d installer.Device) error {
	disk, media, start := d.Disk, "SD", int64(2048)
	if installer.ParentDisk(disk+"p1") != disk || disk == "" {
		return fmt.Errorf("invalid Pro reinstallation disk")
	}
	if m.media == "emmc" {
		media, start = "MMC", proBootStart
		if disk != "/dev/mmcblk0" {
			return fmt.Errorf("unsupported Pro eMMC reinstallation disk")
		}
	}
	if !d.Reinstall || d.Profile != m.Profile() || !d.Automatic || d.Disk != disk || d.Media != media || len(d.CID) != 32 ||
		d.Boot != disk+"p1" || d.Userdata != disk+"p4" || d.BootStart != start || d.BootSectors < 32768 || d.BootSectors > proBootSectors ||
		d.DataStart < d.BootStart+d.BootSectors || d.DataSectors <= 0 || d.DataStart > d.Bytes/512-d.DataSectors ||
		(d.DataType != "ext4" && d.DataType != "f2fs") {
		return fmt.Errorf("unsupported Pro reinstallation target; select an image for the current boot medium")
	}
	if m.media == "emmc" && (d.BootSectors != proBootSectors || d.DataStart != proRootStart+2*262144) {
		return fmt.Errorf("unsupported Pro eMMC A/B layout")
	}
	return nil
}

// Linux assigns SD block numbers independently of U-Boot's mmc device 1.
// Resolve the running root first, then confirm its kernel-reported medium.
func (m Machine) matchesInstalledMedium(root, disk string) bool {
	if disk == "" || installer.ParentDisk(disk+"p1") != disk {
		return false
	}
	want := "SD"
	if m.media == "emmc" {
		if disk != "/dev/mmcblk0" {
			return false
		}
		want = "MMC"
	}
	return installer.ReadTrim(filepath.Join(root, "sys/class/block", filepath.Base(disk), "device/type")) == want
}

func checkInstalledDisk(d installer.Device) error {
	m := Machine{media: strings.TrimPrefix(d.Profile, "pro-")}
	if err := m.validateInstalled(d); err != nil {
		return err
	}
	sys := "/sys/class/block/" + filepath.Base(d.Disk)
	sectors, err := installer.ReadNumber(sys + "/size")
	if err != nil || sectors*512 != d.Bytes || installer.ReadTrim(sys+"/device/cid") != d.CID || installer.ReadTrim(sys+"/device/type") != d.Media {
		return fmt.Errorf("Pro storage identity changed")
	}
	for part, geometry := range map[string][2]int64{d.Boot: {d.BootStart, d.BootSectors}, d.Userdata: {d.DataStart, d.DataSectors}} {
		sys := "/sys/class/block/" + filepath.Base(part)
		start, e1 := installer.ReadNumber(sys + "/start")
		size, e2 := installer.ReadNumber(sys + "/size")
		if e1 != nil || e2 != nil || start != geometry[0] || size != geometry[1] {
			return fmt.Errorf("Pro partition geometry changed")
		}
	}
	return nil
}

func (m Machine) PrepareReinstall(image, dir string, req installer.Request, p installer.Progress) error {
	f, err := os.Open(image)
	if err != nil {
		return err
	}
	defer f.Close()
	start, sectors := proBootStart, proBootSectors
	if m.media == "sd" {
		mbr := make([]byte, 512)
		if _, err := f.ReadAt(mbr, 0); err != nil {
			return err
		}
		start, sectors = int64(binary.LittleEndian.Uint32(mbr[454:])), int64(binary.LittleEndian.Uint32(mbr[458:]))
	}
	fat := io.NewSectionReader(f, start*512, sectors*512)
	for name, spec := range map[string]struct {
		file  string
		limit int64
	}{
		"Image": {"Image-a", 32 * installer.MiB}, "board.dtb": {"ax630c-a.dtb", 8 * installer.MiB},
	} {
		data, err := installer.FatFileProgress(fat, spec.file, spec.limit, p)
		if err != nil {
			return err
		}
		if err := validateBootstrapPayload(name, data); err != nil {
			return err
		}
		if err := installer.WritePrivate(filepath.Join(dir, name), data); err != nil {
			return err
		}
	}
	native, err := installer.FatFileProgress(fat, "initramfs-a.uimg", 32*installer.MiB, p)
	if err != nil {
		return err
	}
	if err := validateBootstrapPayload("initramfs.uimg", native); err != nil {
		return err
	}
	ramdisk, err := installer.RecoveryInitramfsWithBase(native[64:])
	if err != nil {
		return err
	}
	return installer.WritePrivate(filepath.Join(dir, "initramfs.uimg"), ramdiskImage(ramdisk))
}

func ramdiskImage(data []byte) []byte {
	header := make([]byte, 64)
	binary.BigEndian.PutUint32(header, 0x27051956)
	binary.BigEndian.PutUint32(header[12:], uint32(len(data)))
	binary.BigEndian.PutUint32(header[24:], crc32.ChecksumIEEE(data))
	header[28], header[29], header[30], header[31] = 5, 22, 3, 1
	copy(header[32:], "OneKVM reinstall recovery")
	binary.BigEndian.PutUint32(header[4:], crc32.ChecksumIEEE(header))
	return append(header, data...)
}

func bindReinstallBootstrap(dir string, req installer.Request) error {
	if len(req.Token) != 64 {
		return fmt.Errorf("invalid installer token")
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
	if req.Device.Profile == "pro-sd" {
		script.WriteString("mmc dev 1\nmmc rescan\n")
		fmt.Fprintf(&script, "fatload mmc 1:1 %s onekvm-install-Image\nfatload mmc 1:1 0x47000000 onekvm-install-dtb\nfatload mmc 1:1 0x49000000 onekvm-install-initrd\n", address)
	} else if req.Device.Profile == "pro-emmc" {
		script.WriteString("mmc dev 0\nmmc rescan\n")
		for _, entry := range []struct {
			name, address string
			offset, limit int64
		}{
			{"Image", address, 0, 32 * installer.MiB}, {"board.dtb", "0x47000000", 32 * installer.MiB, 8 * installer.MiB},
			{"initramfs.uimg", "0x49000000", 40 * installer.MiB, proSelector - (proBackupKernel + 40*installer.MiB)},
		} {
			stat, err := os.Stat(filepath.Join(dir, entry.name))
			if err != nil {
				return err
			}
			if stat.Size() <= 0 || stat.Size() > entry.limit {
				return fmt.Errorf("Pro recovery payload exceeds raw bootstrap range")
			}
			fmt.Fprintf(&script, "mmc read %s 0x%x 0x%x\n", entry.address, (proBackupKernel+entry.offset)/512, (stat.Size()+511)/512)
		}
	} else {
		return fmt.Errorf("unsupported Pro reinstall profile")
	}
	partitions := ""
	if req.Device.Profile == "pro-emmc" {
		partitions = " blkdevparts=mmcblk0:128M@157024256(pro-boot),128M@291241984(pro-rootfs-a),128M(pro-rootfs-b),-(pro-userdata)"
	}
	fmt.Fprintf(&script, "setenv bootargs root=%sp2 rootwait ro console=ttyS0,115200 earlycon=uart8250,mmio32,0x04880000 clk_ignore_unused%s onekvm.installer=%s\n", req.Device.Disk, partitions, req.Token)
	fmt.Fprintf(&script, "setenv initrd_high 0xffffffffffffffff\n%s %s 0x49000000 0x47000000\n", command, address)
	payload, err := proScript(script.String())
	if err != nil {
		return err
	}
	return installer.WritePrivate(filepath.Join(dir, "install.scr"), payload)
}

func activateSDReinstall(d installer.Device, req installer.Request, report installer.Reporter) error {
	dir := installer.StageDir(d)
	files := []struct{ source, dest string }{{"Image", "onekvm-install-Image"}, {"board.dtb", "onekvm-install-dtb"}, {"initramfs.uimg", "onekvm-install-initrd"}, {"install.scr", "boot.scr"}}
	if err := report("op.boot_check", func(installer.Progress) error {
		if err := checkInstalledDisk(d); err != nil {
			return err
		}
		var needed int64
		for _, file := range files {
			stat, err := os.Stat(filepath.Join(dir, file.source))
			if err != nil {
				return err
			}
			needed += stat.Size() + 4096
		}
		var fs syscall.Statfs_t
		if err := syscall.Statfs(d.BootMount, &fs); err != nil {
			return err
		}
		if int64(fs.Bavail)*int64(fs.Bsize) < needed+installer.MiB {
			return fmt.Errorf("boot partition has insufficient recovery space")
		}
		return nil
	}); err != nil {
		return err
	}
	return replaceSDReinstall(d, req, report)
}

func replaceSDReinstall(d installer.Device, req installer.Request, report installer.Reporter) error {
	dir := installer.StageDir(d)
	files := []struct{ source, dest string }{{"Image", "onekvm-install-Image"}, {"board.dtb", "onekvm-install-dtb"}, {"initramfs.uimg", "onekvm-install-initrd"}, {"install.scr", "boot.scr"}}
	req.State = "arming"
	if err := report("op.boot_cleanup", func(installer.Progress) error { return installer.WriteJSON(filepath.Join(dir, "request.json"), req) }); err != nil {
		return err
	}
	err := report("Installing recovery boot files (keep power connected)", func(p installer.Progress) error {
		for i, file := range files {
			if i == len(files)-1 {
				armed := req
				armed.State = "armed"
				body, err := installer.JsonBytes(armed)
				if err != nil {
					return err
				}
				hash := sha256.Sum256(body)
				if err := installer.WritePrivate(filepath.Join(dir, "request.json"), body); err != nil {
					return err
				}
				if err := installer.WriteJSON(filepath.Join(d.BootMount, installer.MarkerName), installer.Marker{Version: installer.RecoveryAPI, Token: req.Token, Device: d, RequestSHA256: hex.EncodeToString(hash[:])}); err != nil {
					return err
				}
			}
			payload, err := os.ReadFile(filepath.Join(dir, file.source))
			if err != nil {
				return err
			}
			hash := sha256.Sum256(payload)
			if hex.EncodeToString(hash[:]) != req.BootFiles[file.source] {
				return fmt.Errorf("staged recovery file changed")
			}
			path := filepath.Join(d.BootMount, file.dest)
			if err := installer.WritePrivate(path, payload); err != nil {
				return err
			}
			readback, err := installer.HashFile(path, p)
			if err != nil {
				return err
			}
			if readback != req.BootFiles[file.source] {
				return fmt.Errorf("recovery boot readback mismatch")
			}
		}
		return nil
	})
	if err != nil {
		if saveErr := installer.WriteJSON(filepath.Join(dir, "request.json"), req); saveErr != nil {
			return fmt.Errorf("%w (saving retry state: %v)", err, saveErr)
		}
	}
	return err
}

func (m Machine) SeedConfiguration(boot string, d installer.Device, req installer.Request, config, hook []byte) error {
	originalA, err := os.ReadFile(filepath.Join(boot, "initramfs-a.uimg"))
	if err != nil {
		return err
	}
	for i, name := range []string{"initramfs-a.uimg", "initramfs-b.uimg"} {
		path := filepath.Join(boot, name)
		raw, err := os.ReadFile(path)
		if os.IsNotExist(err) && i == 1 {
			raw, err = append([]byte(nil), originalA...), nil
			if m.media == "sd" {
				continue
			}
		}
		if err != nil {
			return err
		}
		if err := validateBootstrapPayload("initramfs.uimg", raw); err != nil {
			return err
		}
		data := append(append(raw[64:], 0, 0, 0, 0), hook...)
		raw = ramdiskImage(data)
		if err := installer.RewriteBootPayload(path, raw); err != nil {
			return err
		}
		if m.media == "emmc" {
			base := int64(proPrefix)
			if i == 1 {
				base = proBackupKernel
			}
			target, err := os.OpenFile(d.Disk, os.O_RDWR, 0)
			if err != nil {
				return err
			}
			hash := sha256.Sum256(raw)
			err = writeProRawFile(target, path, base+40*installer.MiB, proSelector-(proBackupKernel+40*installer.MiB), hex.EncodeToString(hash[:]), nil)
			closeErr := target.Close()
			if err != nil {
				return err
			}
			if closeErr != nil {
				return closeErr
			}
			// eMMC reads a fixed number of sectors instead of using fatload.
			// Extend each slot's script to include the appended restore hook.
			name := []string{"boot-a.scr", "boot-b.scr"}[i]
			script, err := os.ReadFile(filepath.Join(boot, name))
			if err != nil {
				return err
			}
			script, err = resizeInitrdLoad(script, (base+40*installer.MiB)/512, int64(len(raw)))
			if err != nil {
				return err
			}
			if err := installer.WritePrivate(filepath.Join(boot, name), script); err != nil {
				return err
			}
			if i == 0 {
				path := filepath.Join(boot, "boot.scr")
				if err := installer.WritePrivate(path, script); err != nil {
					return err
				}
				target, err := os.OpenFile(d.Disk, os.O_RDWR, 0)
				if err != nil {
					return err
				}
				hash := sha256.Sum256(script)
				err = writeProRawFile(target, path, proSelector, 65536, hex.EncodeToString(hash[:]), nil)
				closeErr := target.Close()
				if err != nil {
					return err
				}
				if closeErr != nil {
					return closeErr
				}
			}
		}
	}
	return installer.WritePrivate(filepath.Join(boot, installer.SystemConfigName), config)
}

func resizeInitrdLoad(raw []byte, sector, size int64) ([]byte, error) {
	invalid := func() ([]byte, error) { return nil, fmt.Errorf("unsupported Pro eMMC boot script") }
	if len(raw) < 72 || binary.BigEndian.Uint32(raw) != 0x27051956 ||
		int(binary.BigEndian.Uint32(raw[12:])) != len(raw)-64 || raw[29] != 22 || raw[30] != 6 || raw[31] != 0 ||
		int(binary.BigEndian.Uint32(raw[64:])) != len(raw)-72 || binary.BigEndian.Uint32(raw[68:]) != 0 {
		return invalid()
	}
	header := append([]byte(nil), raw[:64]...)
	clear(header[4:8])
	if crc32.ChecksumIEEE(header) != binary.BigEndian.Uint32(raw[4:]) ||
		crc32.ChecksumIEEE(raw[64:]) != binary.BigEndian.Uint32(raw[24:]) {
		return invalid()
	}
	lines, found := strings.Split(string(raw[72:]), "\n"), 0
	for i, line := range lines {
		fields := strings.Fields(line)
		if len(fields) != 5 || strings.Join(fields[:3], " ") != "mmc read 0x49000000" {
			continue
		}
		start, err := strconv.ParseInt(fields[3], 0, 64)
		if err != nil || start != sector {
			return invalid()
		}
		fields[4] = fmt.Sprintf("0x%x", (size+511)/512)
		lines[i] = strings.Join(fields, " ")
		found++
	}
	if found != 1 {
		return invalid()
	}
	return proScript(strings.Join(lines, "\n"))
}
