package nanokvmpro

import (
	"bytes"
	"context"
	"encoding/binary"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"

	"github.com/onekvm/onekvm-installer/internal/installer"
)

func TestEMMCConfigurationSeedUpdatesRawLoadsAndChecksums(t *testing.T) {
	boot := t.TempDir()
	disk := filepath.Join(t.TempDir(), "emmc")
	target, err := os.Create(disk)
	if err != nil {
		t.Fatal(err)
	}
	defer target.Close()
	if err := target.Truncate(proSelector + 65536); err != nil {
		t.Fatal(err)
	}
	original := ramdiskImage([]byte("original archive"))
	for i, slot := range []string{"a", "b"} {
		if err := os.WriteFile(filepath.Join(boot, "initramfs-"+slot+".uimg"), original, 0600); err != nil {
			t.Fatal(err)
		}
		base := []int64{proPrefix, proBackupKernel}[i]
		script, err := proScript(fmt.Sprintf("mmc dev 0\nmmc read 0x49000000 0x%x 0x1\nsetenv bootargs root=PARTLABEL=pro-rootfs-%s\n", (base+40*installer.MiB)/512, slot))
		if err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(boot, "boot-"+slot+".scr"), script, 0600); err != nil {
			t.Fatal(err)
		}
	}
	hook := bytes.Repeat([]byte("restore hook"), 100)
	if err := (Machine{media: "emmc"}).SeedConfiguration(boot, installer.Device{Disk: disk}, installer.Request{}, []byte("system settings"), hook); err != nil {
		t.Fatal(err)
	}
	for i, slot := range []string{"a", "b"} {
		raw, err := os.ReadFile(filepath.Join(boot, "initramfs-"+slot+".uimg"))
		if err != nil || !bytes.HasSuffix(raw, hook) || validateBootstrapPayload("initramfs.uimg", raw) != nil {
			t.Fatal("invalid seeded ramdisk", err)
		}
		base := []int64{proPrefix, proBackupKernel}[i]
		readback := make([]byte, len(raw))
		if _, err := target.ReadAt(readback, base+40*installer.MiB); err != nil || !bytes.Equal(raw, readback) {
			t.Fatal("raw ramdisk differs from FAT", err)
		}
		script, err := os.ReadFile(filepath.Join(boot, "boot-"+slot+".scr"))
		if err != nil || !strings.Contains(string(script[72:]), fmt.Sprintf("mmc read 0x49000000 0x%x 0x%x", (base+40*installer.MiB)/512, (len(raw)+511)/512)) {
			t.Fatal("truncated initramfs load", err)
		}
		if _, err := resizeInitrdLoad(script, (base+40*installer.MiB)/512, int64(len(raw))); err != nil {
			t.Fatal("rewritten script checksum invalid", err)
		}
		if i == 0 {
			readback = make([]byte, len(script))
			if _, err := target.ReadAt(readback, proSelector); err != nil || !bytes.Equal(script, readback) {
				t.Fatal("raw selector was not updated", err)
			}
		}
		bad := bytes.Clone(script)
		bad[len(bad)-1] ^= 1
		if _, err := resizeInitrdLoad(bad, (base+40*installer.MiB)/512, int64(len(raw))); err == nil {
			t.Fatal("accepted corrupt script")
		}
		if int(binary.BigEndian.Uint32(raw[12:])) != len(raw)-64 {
			t.Fatal("stale ramdisk length")
		}
	}
}

func init() {
	for _, m := range Machines() {
		installer.RegisterMachine(m)
	}
}

func installedFixture(t *testing.T, media, slot, fs string) string {
	disk := "mmcblk2"
	if media == "emmc" {
		disk = "mmcblk0"
	}
	return installedFixtureOnDisk(t, media, slot, fs, disk)
}

func installedFixtureOnDisk(t *testing.T, media, slot, fs, disk string) string {
	t.Helper()
	root := proFixture(t)
	start, data, kind := int64(2048), int64(788480), "SD"
	if media == "emmc" {
		disk, start, data, kind = "mmcblk0", proBootStart, proRootStart+2*262144, "MMC"
	}
	size := int64(16777216)
	for path, value := range map[string]string{
		"sys/class/block/" + disk + "/device/cid": strings.Repeat("2", 32),
		"etc/os-release": "ID=onekvm\n", "proc/cmdline": "root=PARTUUID=12345678-0" + slot + " ro",
		"proc/mounts":         "overlay / overlay rw 0 0\n/dev/" + disk + "p" + slot + " /mnt/.rofs squashfs ro 0 0\n/dev/" + disk + "p1 /boot vfat rw 0 0\n/dev/" + disk + "p4 /mnt/storage " + fs + " rw 0 0\n",
		"proc/self/mountinfo": "28 27 179:4 /storage /mnt/storage rw - " + fs + " /dev/" + disk + "p4 rw\n",
		"sys/class/block/" + disk + "/device/type": kind, "sys/class/block/" + disk + "/size": strconv.FormatInt(size, 10),
		"sys/class/block/" + disk + "p1/start": strconv.FormatInt(start, 10), "sys/class/block/" + disk + "p1/size": "262144",
		"sys/class/block/" + disk + "p4/start": strconv.FormatInt(data, 10), "sys/class/block/" + disk + "p4/size": strconv.FormatInt(size-data, 10),
		"sys/class/block/" + disk + "p" + slot + "/uevent": "PARTUUID=12345678-0" + slot + "\n",
	} {
		put(t, root, path, value)
	}
	return root
}

func TestInstalledSDResolvesActualLinuxBlockNumber(t *testing.T) {
	for _, disk := range []string{"mmcblk1", "mmcblk2", "mmcblk3"} {
		t.Run(disk, func(t *testing.T) {
			root := installedFixtureOnDisk(t, "sd", "2", "f2fs", disk)
			put(t, root, "dev/"+disk, "SD card")
			machine := Machine{media: "sd"}
			d, err := machine.Probe(root)
			if err != nil || d.Disk != "/dev/"+disk || !machine.Matches(root) || !proSDPresent(root) {
				t.Fatal("SD numbering changed the boot medium", d, err)
			}
			if !(machine.matchesInstalledMedium(root, d.Disk)) || (Machine{media: "emmc"}).Matches(root) {
				t.Fatal("recovery would choose the wrong medium")
			}
			put(t, root, "sys/class/block/"+disk+"/device/type", "MMC")
			if machine.Matches(root) || machine.matchesInstalledMedium(root, d.Disk) {
				t.Fatal("accepted eMMC as SD")
			}
		})
	}
}

func TestInstalledProAfterFirstbootWithoutPartitionUUIDMetadata(t *testing.T) {
	root := installedFixture(t, "sd", "2", "f2fs")
	if err := os.Remove(filepath.Join(root, "sys/class/block/mmcblk2p2/uevent")); err != nil {
		t.Fatal(err)
	}
	// Another medium may have the image's same MBR ID. Use the real mounted
	// root even when only that other medium exports UUID metadata.
	put(t, root, "sys/class/block/mmcblk0p2/uevent", "PARTUUID=12345678-02\n")
	machine := Machine{media: "sd"}
	d, err := machine.Probe(root)
	if err != nil || d.Disk != "/dev/mmcblk2" || !machine.Matches(root) {
		t.Fatal("freshly expanded SD was not recognized", d, err)
	}
	put(t, root, "proc/mounts", "overlay / overlay rw 0 0\n/dev/mmcblk0p2 /mnt/.rofs squashfs ro 0 0\n/dev/mmcblk2p1 /boot vfat rw 0 0\n/dev/mmcblk2p4 /mnt/storage f2fs rw 0 0\n")
	if _, err := machine.Probe(root); err == nil {
		t.Fatal("boot mount overrode the actual running root disk")
	}
}

func TestInstalledProTargetsUseRecovery(t *testing.T) {
	for _, media := range []string{"sd", "emmc"} {
		for _, slot := range []string{"2", "3"} {
			for _, fs := range []string{"ext4", "f2fs"} {
				t.Run(media+slot+fs, func(t *testing.T) {
					root := installedFixture(t, media, slot, fs)
					machine := Machine{media: media}
					d, err := machine.Probe(root)
					if err != nil || !d.Reinstall || !machine.Matches(root) || installer.DirectInstall(d) || d.DataRoot != "/storage" {
						t.Fatalf("unsafe installed target: %+v %v", d, err)
					}
					other := "sd"
					if media == "sd" {
						other = "emmc"
					}
					if (Machine{media: other}).Matches(root) {
						t.Fatal("selected another boot medium")
					}
					if _, err := (Machine{media: other}).Probe(root); err == nil {
						t.Fatal("accepted cross-medium reinstall")
					}
					put(t, root, "proc/cmdline", "root=/dev/nfs")
					if _, err := machine.Probe(root); err == nil {
						t.Fatal("accepted NFS as a persistent boot source")
					}
				})
			}
		}
	}
}

func TestProReinstallBootScriptsBindTargetWithoutChangingFirmware(t *testing.T) {
	for _, media := range []string{"sd", "emmc"} {
		root := installedFixture(t, media, "3", "f2fs")
		d, err := (Machine{media: media}).Probe(root)
		if err != nil {
			t.Fatal(err)
		}
		dir := t.TempDir()
		for _, name := range []string{"Image", "board.dtb", "initramfs.uimg"} {
			put(t, dir, name, "payload")
		}
		if err := os.WriteFile(filepath.Join(dir, "Image"), proTestRawKernel(), 0600); err != nil {
			t.Fatal(err)
		}
		req := installer.Request{Device: d, Token: strings.Repeat("a", 64)}
		if err := bindReinstallBootstrap(dir, req); err != nil {
			t.Fatal(err)
		}
		raw, err := os.ReadFile(filepath.Join(dir, "install.scr"))
		if err != nil {
			t.Fatal(err)
		}
		script := string(raw[72:])
		if !strings.Contains(script, "root="+d.Disk+"p2") || !strings.Contains(script, "onekvm.installer="+req.Token) || strings.Contains(script, "saveenv") || strings.Contains(script, "mmc write") {
			t.Fatal(script)
		}
		if media == "sd" && !strings.Contains(script, "fatload mmc 1:1") {
			t.Fatal(script)
		}
		if media == "emmc" && !strings.Contains(script, "blkdevparts=mmcblk0:128M@157024256") {
			t.Fatal(script)
		}
		for _, line := range strings.Split(script, "\n") {
			if len(strings.Fields(line)) > 16 {
				t.Fatal("script exceeds parser limit", line)
			}
		}
		if err := validateBootstrapPayload("initramfs.uimg", ramdiskImage([]byte("payload"))); err != nil {
			t.Fatal(err)
		}
	}
}

func TestSDReinstallPublishesVerifiedRequestAndCanCancel(t *testing.T) {
	d := installer.Device{Profile: "pro-sd", Reinstall: true, DataMount: t.TempDir(), BootMount: t.TempDir()}
	dir := installer.StageDir(d)
	for _, name := range []string{"Image", "board.dtb", "initramfs.uimg", "install.scr", "image.img", "firstboot.txt"} {
		put(t, dir, name, "new "+name)
	}
	put(t, d.BootMount, "boot.scr", "old boot")
	req := installer.Request{Version: installer.RecoveryAPI, State: "prepared", Token: strings.Repeat("a", 64), Device: d}
	if err := installer.RequestHashes(&req, dir, false, nil); err != nil {
		t.Fatal(err)
	}
	if err := installer.WriteJSON(filepath.Join(dir, "request.json"), req); err != nil {
		t.Fatal(err)
	}
	err := replaceSDReinstall(d, req, func(string, func(installer.Progress) error) error { return context.Canceled })
	if !errors.Is(err, context.Canceled) {
		t.Fatal(err)
	}
	if old, _ := os.ReadFile(filepath.Join(d.BootMount, "boot.scr")); string(old) != "old boot" {
		t.Fatal("cancellation changed boot")
	}
	if err := replaceSDReinstall(d, req, installer.Report); err != nil {
		t.Fatal(err)
	}
	var marker installer.Marker
	if err := installer.ReadJSON(filepath.Join(d.BootMount, installer.MarkerName), &marker); err != nil {
		t.Fatal(err)
	}
	hash, err := installer.HashFile(filepath.Join(dir, "request.json"), nil)
	if err != nil || hash != marker.RequestSHA256 || marker.Token != req.Token {
		t.Fatal("unbound request", err)
	}
	var armed installer.Request
	if err := installer.ReadJSON(filepath.Join(dir, "request.json"), &armed); err != nil || armed.State != "armed" {
		t.Fatal("request not armed", err)
	}
	if boot, _ := os.ReadFile(filepath.Join(d.BootMount, "boot.scr")); string(boot) != "new install.scr" {
		t.Fatal("wrong boot script")
	}
	if err := installer.ResetStaging(d, ""); !errors.Is(err, installer.ErrActiveInstallation) {
		t.Fatal("armed source was removable", err)
	}
}
