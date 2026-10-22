package nanokvm

import (
	"testing"

	"github.com/onekvm/onekvm-installer/internal/installer"
)

func init() { installer.RegisterMachine(Machine{}) }

func TestSG2002Identity(t *testing.T) {
	for _, tc := range []struct {
		model, compatible string
		want              bool
	}{
		{"Sipeed NanoKVM Cube Full", "sipeed,nanokvm\x00sophgo,sg2002\x00", true},
		{"LicheeRv Nano", "cvitek,cv181x\x00", true},
		{"Sipeed NanoKVM Pro", "sophgo,sg2002\x00", false},
		{"Other SG2002 board", "sophgo,sg2002\x00", false},
		{"NanoKVM Cube", "sophgo,sg2002-other\x00", false},
	} {
		root := t.TempDir()
		put(t, root, "proc/device-tree/model", tc.model+"\x00")
		put(t, root, "proc/device-tree/compatible", tc.compatible)
		if got := (Machine{}).Matches(root); got != tc.want {
			t.Errorf("%q / %q: got %v, want %v", tc.model, tc.compatible, got, tc.want)
		}
	}
}

func TestBootstrapTargetUsesCapacity(t *testing.T) {
	d := installer.Device{Profile: "nanokvm", Automatic: true, Disk: "/dev/mmcblk0", Userdata: "/dev/mmcblk0p3",
		Bytes: 32 * 1024 * installer.MiB, BootStart: 1, DataStart: 1703936, DataSectors: 50000000}
	for _, mib := range []int64{16, 32, 48, 64, 128, 256} {
		d.BootSectors = mib * 2048
		if err := (Machine{}).ValidateTarget(d); err != nil {
			t.Errorf("boot size %d MiB should be checked against the recovery files: %v", mib, err)
		}
	}
	for _, sectors := range []int64{0, -1, d.DataStart, d.Bytes / 512} {
		d.BootSectors = sectors
		if err := (Machine{}).ValidateTarget(d); err == nil {
			t.Errorf("accepted invalid/overlapping boot size %d", sectors)
		}
	}
}
