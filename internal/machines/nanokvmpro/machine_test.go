package nanokvmpro

import (
	"bytes"
	"crypto/sha256"
	"encoding/binary"
	"encoding/hex"
	"hash/crc32"
	"io"
	"os"
	"path/filepath"
	"reflect"
	"strconv"
	"strings"
	"testing"

	"github.com/onekvm/onekvm-installer/internal/installer"
)

func put(t *testing.T, root, path, value string) {
	t.Helper()
	p := filepath.Join(root, path)
	if err := os.MkdirAll(filepath.Dir(p), 0700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(p, []byte(value), 0600); err != nil {
		t.Fatal(err)
	}
}
func proFixture(t *testing.T) string {
	t.Helper()
	root := t.TempDir()
	for p, v := range map[string]string{
		"proc/device-tree/model": "NanoKVM-Pro\x00", "proc/device-tree/compatible": "sipeed,nanokvm-pro\x00axera,ax630c\x00",
		"proc/mounts":                        "/dev/mmcblk0p17 / ext4 rw 0 0\n/dev/mmcblk0p16 /boot vfat rw 0 0\n",
		"proc/cmdline":                       "root=/dev/mmcblk0p17 rootwait rw",
		"sys/class/block/mmcblk0/device/cid": strings.Repeat("1", 32), "sys/class/block/mmcblk0/device/type": "MMC", "sys/class/block/mmcblk0/size": "16777216",
		"sys/class/block/mmcblk0p7/start": "9728", "sys/class/block/mmcblk0p7/size": "2048",
		"sys/class/block/mmcblk0p16/start": "306688", "sys/class/block/mmcblk0p16/size": "262144",
		"sys/class/block/mmcblk0p17/start": "568832", "sys/class/block/mmcblk0p17/size": "16208384",
		"sys/class/block/mmcblk1/device/cid": strings.Repeat("2", 32), "sys/class/block/mmcblk1/device/type": "SD", "sys/class/block/mmcblk1/size": "61067264",
	} {
		put(t, root, p, v)
	}
	return root
}
func TestProTargetSelectionAndIdentity(t *testing.T) {
	root := proFixture(t)
	emmc, err := (Machine{"emmc"}).Probe(root)
	if err != nil {
		t.Fatal(err)
	}
	sd, err := (Machine{"sd"}).Probe(root)
	if err != nil {
		t.Fatal(err)
	}
	if emmc.Disk != "/dev/mmcblk0" || sd.Disk != "/dev/mmcblk1" || sd.Boot != emmc.Boot || sd.CID == emmc.CID || sd.SystemCID != emmc.CID || installer.SameDisk(sd, emmc) {
		t.Fatalf("wrong independent targets: %+v %+v", emmc, sd)
	}
	if sd.HardwareModel != "Sipeed NanoKVM Pro Desk" || (Machine{"sd"}).Matches(root) || !(Machine{"emmc"}).Matches(root) {
		t.Fatal("default hardware probe must not prefer an inserted SD card")
	}
	if err := os.RemoveAll(filepath.Join(root, "sys/class/block/mmcblk1")); err != nil {
		t.Fatal(err)
	}
	if _, err := (Machine{"sd"}).Probe(root); err == nil {
		t.Fatal("accepted missing SD")
	}
	if _, err := (Machine{"emmc"}).Probe(root); err != nil {
		t.Fatal("missing SD must not block eMMC", err)
	}
	for path, value := range map[string]string{"proc/cmdline": "root=/dev/nfs", "sys/class/block/mmcblk0p7/start": "0", "sys/class/block/mmcblk0p16/start": "1", "sys/class/block/mmcblk0/device/cid": "invalid", "proc/device-tree/model": "NanoKVM Pro ATX"} {
		r := proFixture(t)
		put(t, r, path, value)
		if _, err := (Machine{"emmc"}).Probe(r); err == nil {
			t.Fatal("accepted unsafe factory identity", path)
		}
	}
}
func TestProFilenameProfile(t *testing.T) {
	for _, media := range []string{"sd", "emmc"} {
		m := Machine{media}
		prefix := "onekvm-nanokvm-pro-" + media
		for _, name := range []string{prefix + ".img", prefix + "-0.2.0-onekvm-nanokvm-pro-r1.img"} {
			if !m.MatchesImage(name) {
				t.Fatal("missed asset", name)
			}
		}
		for _, name := range []string{"onekvm-nanokvm-1.img", prefix + ".img.gz", prefix + ".img.sha256", "bad-" + prefix + ".img", "onekvm-nanokvm-pro-other.img"} {
			if m.MatchesImage(name) {
				t.Fatal("matched wrong asset", name)
			}
		}
	}
}
func envFixture(entries map[string]string) []byte {
	raw := make([]byte, proEnvSize)
	at := 4
	for k, v := range entries {
		at += copy(raw[at:], k+"="+v+"\x00")
	}
	binary.LittleEndian.PutUint32(raw, crc32.ChecksumIEEE(raw[4:]))
	return raw
}
func TestProEnvironmentPreservesFactoryAndSwitchesPriority(t *testing.T) {
	original := map[string]string{"bootcmd": "sd_update", "bootsystem": "A", "ethaddr": "02:34:56:78:ab:cd", "serial#": "factory-123", "custom": "kept=exactly"}
	raw := envFixture(original)
	install, err := proPriorityEnvironment(raw, true)
	if err != nil {
		t.Fatal(err)
	}
	during, err := parseProEnvironment(install)
	if err != nil || during["bootcmd"] != proInstallBootCommand {
		t.Fatal("SD can intercept eMMC installation", err)
	}
	next, err := proPriorityEnvironment(install, false)
	if err != nil {
		t.Fatal(err)
	}
	parsed, err := parseProEnvironment(next)
	if err != nil || parsed["bootcmd"] != proBootCommand || parsed["onekvm_sd_boot"] != proSDBoot || parsed["onekvm_emmc_boot"] != proEMMCBoot {
		t.Fatal(err)
	}
	for k, v := range original {
		if k != "bootcmd" && parsed[k] != v {
			t.Fatal("changed factory key", k)
		}
	}
	again, err := proPriorityEnvironment(next, false)
	if err != nil || !bytes.Equal(again, next) {
		t.Fatal("priority update not idempotent", err)
	}
	f, err := os.CreateTemp(t.TempDir(), "emmc")
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()
	if err = f.Truncate(proEnvOffset + proEnvSize + 8); err != nil {
		t.Fatal(err)
	}
	f.WriteAt([]byte("BEFORE"), proEnvOffset-6)
	f.WriteAt(raw, proEnvOffset)
	f.WriteAt([]byte("AFTER"), proEnvOffset+proEnvSize)
	if err = writeProEnvironment(f, raw, false); err != nil {
		t.Fatal(err)
	}
	before, after := make([]byte, 6), make([]byte, 5)
	f.ReadAt(before, proEnvOffset-6)
	f.ReadAt(after, proEnvOffset+proEnvSize)
	if string(before) != "BEFORE" || string(after) != "AFTER" {
		t.Fatal("wrote outside environment")
	}
	if err = writeProEnvironment(f, raw, false); err == nil {
		t.Fatal("accepted stale environment")
	}
	for _, changes := range []map[string]string{{"bootcmd": "run custom", "bootsystem": "A"}, {"bootcmd": "sd_update", "bootsystem": "B"}} {
		if _, err := proPriorityEnvironment(envFixture(changes), false); err == nil {
			t.Fatal("accepted unsupported policy")
		}
	}
	raw[50] ^= 1
	if _, err := proPriorityEnvironment(raw, false); err == nil {
		t.Fatal("accepted invalid CRC")
	}
}
func TestProBootstrapScriptCRCsAndParser(t *testing.T) {
	dir := t.TempDir()
	for _, name := range []string{"Image", "board.dtb", "initramfs.uimg"} {
		put(t, dir, name, "payload")
	}
	req := installer.Request{Device: installer.Device{Profile: "pro-emmc"}, Token: strings.Repeat("a", 64)}
	if err := bindProBootstrap(dir, req); err != nil {
		t.Fatal(err)
	}
	script, err := os.ReadFile(filepath.Join(dir, "install.scr"))
	if err != nil {
		t.Fatal(err)
	}
	header := bytes.Clone(script[:64])
	want := binary.BigEndian.Uint32(header[4:])
	clear(header[4:8])
	if binary.BigEndian.Uint32(header) != 0x27051956 || crc32.ChecksumIEEE(header) != want || crc32.ChecksumIEEE(script[64:]) != binary.BigEndian.Uint32(header[24:]) || int(binary.BigEndian.Uint32(script[64:])) != len(script)-72 {
		t.Fatal("invalid legacy script header")
	}
	text := string(script[72:])
	if !strings.Contains(text, "onekvm.installer="+req.Token) || !strings.Contains(text, "initrd_high 0xffffffffffffffff") || !strings.Contains(text, "root=/dev/mmcblk0p17") {
		t.Fatal(text)
	}
	if _, err = proScript(strings.Repeat("word ", 17)); err == nil {
		t.Fatal("accepted script too wide for factory parser")
	}
}
func TestProEMMCWritePreservesVendorPrefix(t *testing.T) {
	source, err := os.CreateTemp(t.TempDir(), "source")
	if err != nil {
		t.Fatal(err)
	}
	defer source.Close()
	size := proPrefix + 4096
	source.Truncate(size)
	payload := bytes.Repeat([]byte{42}, 4096)
	source.WriteAt(payload, proPrefix)
	hash := sha256.New()
	io.Copy(hash, io.NewSectionReader(source, 0, size))
	info := installer.Image{Profile: "pro-emmc", PayloadSize: size, PayloadSHA256: hex.EncodeToString(hash.Sum(nil))}
	target, err := os.CreateTemp(t.TempDir(), "target")
	if err != nil {
		t.Fatal(err)
	}
	defer target.Close()
	target.Truncate(size + 8)
	target.WriteAt([]byte("VENDOR"), proPrefix-6)
	target.WriteAt([]byte("KEEP"), size)
	if err = (Machine{"emmc"}).WritePayload(target, source, info, nil); err != nil {
		t.Fatal(err)
	}
	b, a := make([]byte, 6), make([]byte, 4)
	target.ReadAt(b, proPrefix-6)
	target.ReadAt(a, size)
	if string(b) != "VENDOR" || string(a) != "KEEP" {
		t.Fatal("overwrote vendor prefix or trailer bounds")
	}
	check := make([]byte, 4096)
	target.ReadAt(check, proPrefix)
	if !bytes.Equal(check, payload) {
		t.Fatal("wrong payload")
	}
	if err = (Machine{"sd"}).WritePayload(target, source, info, nil); err == nil {
		t.Fatal("accepted eMMC as SD")
	}
}
func TestProBootstrapHashInventoryFailsClosed(t *testing.T) {
	// Registration belongs to application composition; only register this test's machine once.
	if _, err := installer.MachineFor("pro-emmc"); err != nil {
		installer.RegisterMachine(Machine{"emmc"})
	}
	dir := t.TempDir()
	for _, name := range []string{"image.img", "firstboot.txt", "Image", "board.dtb", "initramfs.uimg", "install.scr"} {
		put(t, dir, name, name)
	}
	req := installer.Request{Device: installer.Device{Profile: "pro-emmc"}}
	if err := installer.RequestHashes(&req, dir, true, nil); err == nil {
		t.Fatal("accepted absent bootstrap inventory")
	}
	if err := installer.RequestHashes(&req, dir, false, nil); err != nil {
		t.Fatal(err)
	}
	copyReq := req
	if err := installer.RequestHashes(&copyReq, dir, true, nil); err != nil || !reflect.DeepEqual(req, copyReq) {
		t.Fatal(err)
	}
	put(t, dir, "Image", "changed")
	if err := installer.RequestHashes(&copyReq, dir, true, nil); err == nil {
		t.Fatal("accepted changed kernel")
	}
}
func TestProFixedOffsets(t *testing.T) {
	if proSelector/512 != 0x4ad80 || proBootStart*512 != 157024256 || proRootStart*512 != 291241984 || proEnvOffset/512 != 9728 {
		t.Fatal(strconv.FormatInt(proSelector, 16))
	}
}
