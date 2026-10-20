package machines_test

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	. "github.com/onekvm/onekvm-installer/internal/installer"
	_ "github.com/onekvm/onekvm-installer/internal/machines"
)

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
func vendorFixture(t *testing.T) string {
	t.Helper()
	if _, err := MachineFor("nanokvm"); err != nil {
		t.Skip("NanoKVM fixtures require riscv64 or -tags=onekvm_test")
	}
	r := t.TempDir()
	for p, v := range map[string]string{
		"proc/mounts":                 "/dev/mmcblk0p2 / ext4 rw 0 0\n/dev/mmcblk0p1 /boot vfat rw 0 0\n/dev/mmcblk0p3 /data exfat rw 0 0\n",
		"proc/cmdline":                "root=/dev/mmcblk0p2 rootwait rw",
		"proc/device-tree/compatible": "cvitek,cv181x\x00", "proc/device-tree/model": "LicheeRv Nano\x00", "etc/kvm/hw": "beta",
		"sys/class/block/mmcblk0/device/cid": "02544d5341333247832b819dfc018500", "sys/class/block/mmcblk0/device/type": "SD",
		"sys/class/block/mmcblk0/size": "61067264", "sys/class/block/mmcblk0p1/start": "1", "sys/class/block/mmcblk0p1/size": "32768",
		"sys/class/block/mmcblk0p3/start": "16001024", "sys/class/block/mmcblk0p3/size": "45066240",
	} {
		put(t, r, p, v)
	}
	return r
}
func TestProbeStockExfat(t *testing.T) {
	r := vendorFixture(t)
	d, err := Probe(r)
	if err != nil {
		t.Fatal(err)
	}
	if d.Profile != "nanokvm" || d.DataType != "exfat" || !d.Automatic || d.CanArm() != nil {
		t.Fatal(d)
	}
	put(t, r, "proc/cmdline", "root=/dev/nfs nfsroot=server:/root")
	d, err = Probe(r)
	if err != nil {
		t.Fatal(err)
	}
	if d.CanArm() == nil {
		t.Fatal("inferred kernel source from NFS root")
	}
}
func TestProbeUnknownBoard(t *testing.T) {
	r := vendorFixture(t)
	put(t, r, "etc/kvm/hw", "unknown")
	if _, err := Probe(r); err == nil {
		t.Fatal("treated a generic cv181x board as NanoKVM")
	}
}
func TestProbeAmbiguousUserdata(t *testing.T) {
	r := vendorFixture(t)
	b, _ := os.ReadFile(filepath.Join(r, "proc/mounts"))
	put(t, r, "proc/mounts", string(b)+"/dev/mmcblk0p4 /mnt/storage ext4 rw 0 0\n")
	if _, err := Probe(r); err == nil {
		t.Fatal("guessed userdata")
	}
}
func TestDetectCardReplacement(t *testing.T) {
	r := vendorFixture(t)
	a, _ := Probe(r)
	b := a
	b.CID = strings.Repeat("0", 32)
	if SameDisk(a, b) {
		t.Fatal("ignored card replacement")
	}
}
func TestRecoveryGuard(t *testing.T) {
	t.Setenv("ONEKVM_INITRAMFS", "")
	if _, err := Recover(nil); err == nil {
		t.Fatal("recovery allowed in a live system")
	}
}
