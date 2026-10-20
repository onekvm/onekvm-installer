package nanokvmpro

import (
	"bytes"
	"context"
	"os"
	"path/filepath"
	"testing"

	"github.com/onekvm/onekvm-installer/internal/installer"
)

func TestProSolutionsFollowSelectedImageAndRequireSD(t *testing.T) {
	for _, state := range []string{"sd", "blank-card", "data-card", "broken-old-card", "missing-card", "missing-device", "emmc", "emmc-without-sd", "no-image", "no-path", "sd-boot", "nfs", "pcie", "unreadable-firmware"} {
		t.Run(state, func(t *testing.T) {
			root := proFixture(t)
			put(t, root, "dev/mmcblk0", string(make([]byte, proSPLSize)))
			put(t, root, "dev/mmcblk1", "old card contents")
			in := installer.CheckContext{Root: root, Phase: installer.CheckImageReady,
				Image: installer.Image{Profile: "pro-sd"}, ImagePath: "/selected/OneKVM.img",
				Target: installer.Device{Profile: "pro-emmc"}}
			want, blocking, failed := "pro.sd-boot", false, false
			switch state {
			case "blank-card":
				put(t, root, "dev/mmcblk1", "")
			case "data-card":
				put(t, root, "dev/mmcblk1", "ordinary data, no OneKVM marker")
			case "broken-old-card":
				put(t, root, "dev/mmcblk1", "broken previous OneKVM boot files")
			case "missing-card", "emmc-without-sd":
				if err := os.RemoveAll(filepath.Join(root, "sys/class/block/mmcblk1")); err != nil {
					t.Fatal(err)
				}
				want, blocking = "pro.sd-missing", true
			case "missing-device":
				if err := os.Remove(filepath.Join(root, "dev/mmcblk1")); err != nil {
					t.Fatal(err)
				}
				want, blocking = "pro.sd-missing", true
			case "sd-boot":
				put(t, root, "proc/cmdline", "root=/dev/mmcblk1p2 rootwait")
				want = ""
			case "nfs":
				put(t, root, "proc/cmdline", "root=/dev/nfs")
				want = ""
			case "pcie":
				put(t, root, "proc/device-tree/model", "NanoKVM Pro ATX")
				want = ""
			case "no-image":
				in.Image = installer.Image{}
				want = ""
			case "no-path":
				in.ImagePath = ""
				want = ""
			case "unreadable-firmware":
				put(t, root, "dev/mmcblk0", "short")
				want, failed = "", true
			}
			if state == "emmc" || state == "emmc-without-sd" {
				in.Image.Profile, in.Target.Profile = "pro-emmc", "pro-sd"
				want, blocking = "", false
			}
			before, err := os.ReadFile(filepath.Join(root, "dev/mmcblk0"))
			if err != nil {
				t.Fatal(err)
			}
			result, err := checkSDBoot(context.Background(), in)
			if (err != nil) != failed {
				t.Fatal("wrong error result", result, err)
			}
			if want == "" {
				if len(result.Issues) != 0 {
					t.Fatal("irrelevant image prompted", result)
				}
			} else if len(result.Issues) != 1 || result.Issues[0].ID != want || result.Blocking() != blocking {
				t.Fatal("wrong image-specific result", result, err)
			}
			if want == "pro.sd-boot" && result.Issues[0].Solutions[1].RepairID != "pro.sd-boot.firmware" {
				t.Fatal("missing direct repair", result)
			}
			after, err := os.ReadFile(filepath.Join(root, "dev/mmcblk0"))
			if err != nil || !bytes.Equal(before, after) {
				t.Fatal("check wrote firmware", err)
			}
		})
	}
}

func TestProSolutionClearsAfterSDPriorityFirmwareUpdate(t *testing.T) {
	path := os.Getenv("ONEKVM_TEST_STOCK_SPL")
	if path == "" {
		t.Skip("set ONEKVM_TEST_STOCK_SPL to the verified factory SPL")
	}
	stock, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	target, err := reconstructSPL(stock)
	if err != nil {
		t.Fatal(err)
	}
	root := proFixture(t)
	put(t, root, "dev/mmcblk0", string(target))
	put(t, root, "dev/mmcblk1", "empty card ready for the selected image")
	in := installer.CheckContext{Root: root, Image: installer.Image{Profile: "pro-sd"}, ImagePath: "/selected.img"}
	result, err := checkSDBoot(context.Background(), in)
	if err != nil || !result.Supported || len(result.Issues) != 0 {
		t.Fatal("repaired firmware still warns", result, err)
	}
	if err := os.RemoveAll(filepath.Join(root, "sys/class/block/mmcblk1")); err != nil {
		t.Fatal(err)
	}
	result, err = checkSDBoot(context.Background(), in)
	if err != nil || !result.Blocking() || len(result.Issues) != 1 || result.Issues[0].ID != "pro.sd-missing" {
		t.Fatal("repaired firmware concealed a removed SD card", result, err)
	}
}
