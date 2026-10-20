package installer

import (
	"os"
	"path/filepath"
	"testing"
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
