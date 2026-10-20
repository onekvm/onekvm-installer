package nanokvmpro

import (
	"bytes"
	"compress/bzip2"
	"context"
	"crypto/sha256"
	"embed"
	"encoding/binary"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"syscall"

	"github.com/onekvm/onekvm-installer/internal/installer"
)

const proSPLSize = 128 * 1024
const proSPLTargetSHA = "443d7f677a72d8123a51b9cb47955c7cac89d8170fcc69cb9a7bede6aac518a4"

//go:embed firmware/*
var proFirmware embed.FS

var proSPLPatches = map[string][2]string{
	"c1db005b2d61a5ed8627c70e659b5274a5d87a4d97daac356646d53b63dd44d6": {"stock-1.0.15.bsdiff", "22e3c1adeb1a2bc8a99e5a1fb7aaec4d7fcb2ef59c4c9225d8f5f0f245be0c26"},
	"cdd8f8efd712cd40fa400d782b54dbdaba0b393895b99bd9c030a582aa7e4775": {"stock-legacy.bsdiff", "5d8712e5f28651e24133007aaf5ffe70c740c3434d3220b090fa7ae405cf2f7d"},
}

func digest(data []byte) string { return fmt.Sprintf("%x", sha256.Sum256(data)) }

func (Machine) SolutionRepairs() []installer.RepairHook {
	return []installer.RepairHook{{ID: "pro.sd-boot.firmware", Prepare: prepareSPLRepair}}
}

type splSnapshot struct {
	cid, sdCID, sourceHash string
	source                 []byte
	protected              [3]string
}

func prepareSPLRepair(ctx context.Context, in installer.CheckContext) (installer.RepairPlan, error) {
	before, err := snapshotSPLRepair(ctx, in)
	if err != nil {
		return installer.RepairPlan{}, err
	}
	target, err := reconstructSPL(before.source)
	if err != nil {
		return installer.RepairPlan{}, err
	}
	return installer.RepairPlan{
		ID: "pro.sd-boot.firmware", TitleKey: "issue.pro.sd-boot.firmware.title", SummaryKey: "repair.pro.spl.summary",
		Facts: []installer.RepairFact{
			{LabelKey: "repair.device", Value: "/dev/mmcblk0"},
			{LabelKey: "repair.cid", Value: before.cid},
			{LabelKey: "repair.range", Value: "0–131071 (128 KiB)"},
			{LabelKey: "repair.source", Value: before.sourceHash},
			{LabelKey: "repair.target", Value: proSPLTargetSHA},
		},
		Apply: func(ctx context.Context, report installer.Reporter) error {
			if os.Geteuid() != 0 {
				return fmt.Errorf("firmware repair requires root")
			}
			return report("op.firmware_write", func(installer.Progress) error {
				current, err := snapshotSPLRepair(ctx, in)
				if err != nil {
					return err
				}
				if current.cid != before.cid || current.sdCID != before.sdCID || current.sourceHash != before.sourceHash || current.protected != before.protected {
					return fmt.Errorf("storage or firmware changed since repair review; run checks again")
				}
				if err := ctx.Err(); err != nil {
					return err
				}
				f, err := os.OpenFile(filepath.Join(in.Root, "dev/mmcblk0"), os.O_RDWR|syscall.O_SYNC|syscall.O_NOFOLLOW, 0)
				if err != nil {
					return err
				}
				defer f.Close()
				if err = writePrimarySPL(f, before.source, target); err != nil {
					return err
				}
				protected, err := protectedSPLRegions(in.Root)
				if err != nil {
					return err
				}
				if protected != before.protected {
					return fmt.Errorf("protected firmware region verification failed; keep the device powered on")
				}
				return nil
			})
		},
	}, nil
}

func snapshotSPLRepair(ctx context.Context, in installer.CheckContext) (splSnapshot, error) {
	var result splSnapshot
	root := in.Root
	if err := ctx.Err(); err != nil {
		return result, err
	}
	model := strings.ToLower(installer.ReadTrim(filepath.Join(root, "proc/device-tree/model")))
	if !proMatches(root) || strings.Contains(model, "atx") || strings.Contains(model, "pcie") ||
		installer.CmdlineValue(installer.ReadTrim(filepath.Join(root, "proc/cmdline")), "root") != "/dev/mmcblk0p17" ||
		installer.ReadTrim(filepath.Join(root, "sys/class/block/mmcblk0/device/type")) != "MMC" {
		return result, fmt.Errorf("this repair requires Pro Desk running the factory eMMC system")
	}
	result.cid = installer.ReadTrim(filepath.Join(root, "sys/class/block/mmcblk0/device/cid"))
	if len(result.cid) != 32 {
		return result, fmt.Errorf("cannot identify eMMC")
	}
	if in.Image.Profile != "pro-sd" || !proSDPresent(root) {
		return result, fmt.Errorf("insert an SD card before selecting a Pro SD image")
	}
	result.sdCID = installer.ReadTrim(filepath.Join(root, "sys/class/block/mmcblk1/device/cid"))
	if len(result.sdCID) != 32 || (in.Target.CID != "" && in.Target.CID != result.sdCID) {
		return result, fmt.Errorf("Pro storage identity changed")
	}
	f, err := os.Open(filepath.Join(root, "dev/mmcblk0"))
	if err != nil {
		return result, err
	}
	defer f.Close()
	stat, err := f.Stat()
	if err != nil || stat.Mode()&os.ModeDevice == 0 || stat.Mode()&os.ModeCharDevice != 0 {
		return result, fmt.Errorf("eMMC target is not a block device")
	}
	result.source = make([]byte, proSPLSize)
	if _, err = f.ReadAt(result.source, 0); err != nil {
		return result, err
	}
	result.sourceHash = digest(result.source)
	if _, known := proSPLPatches[result.sourceHash]; !known {
		return result, fmt.Errorf("unsupported factory SPL; firmware will not be written (SHA-256 %s)", result.sourceHash)
	}
	backup := make([]byte, proSPLSize)
	if _, err = f.ReadAt(backup, proSPLSize); err != nil {
		return result, err
	}
	if !bytes.Equal(result.source, backup) {
		return result, fmt.Errorf("factory backup SPL differs; firmware will not be written")
	}
	if err := checkSDFirmware(ctx, in); err != nil {
		return result, err
	}
	result.protected, err = protectedSPLRegions(root)
	return result, err
}

func checkSDFirmware(ctx context.Context, in installer.CheckContext) error {
	info, err := installer.VerifyContext(ctx, in.ImagePath, "pro-sd", 4*1024*installer.MiB, nil)
	if err != nil {
		return err
	}
	if info != in.Image {
		return fmt.Errorf("selected image changed since validation")
	}
	f, err := os.Open(in.ImagePath)
	if err != nil {
		return err
	}
	defer f.Close()
	boot := io.NewSectionReader(f, info.BootStart*512, info.BootSectors*512)
	for file, expected := range map[string][]string{
		"uboot.bin": {
			"0e00f8e4b9d612cd19eb6bea7387de7f237473ee4fc038d341ca02340107b6f6",
			// Same SD boot contract, with the OneKVM JD9853 boot artwork.
			"066092a33e9498912a89837d2ca75ba2f441a2b4b834e9a764eae9823888b634",
		},
		"atf.img": {"2b010615206d967f036f05f76fa2aa4bf6f884e3dac9c46cbc39dd0f96013390"},
	} {
		data, err := installer.FatFile(boot, file, 2*installer.MiB)
		if err != nil || !slices.Contains(expected, digest(data)) {
			return fmt.Errorf("selected SD image lacks matching %s; choose a complete matching OneKVM SD image", file)
		}
	}
	return ctx.Err()
}

func protectedSPLRegions(root string) ([3]string, error) {
	var result [3]string
	for i, region := range []struct {
		path         string
		offset, size int64
	}{
		{"dev/mmcblk0", proSPLSize, 24*installer.MiB - proSPLSize},
		{"dev/mmcblk0boot0", 0, 4 * installer.MiB},
		{"dev/mmcblk0boot1", 0, 4 * installer.MiB},
	} {
		f, err := os.Open(filepath.Join(root, region.path))
		if err != nil {
			return result, err
		}
		hash := sha256.New()
		n, err := io.Copy(hash, io.NewSectionReader(f, region.offset, region.size))
		f.Close()
		if err != nil || n != region.size {
			return result, fmt.Errorf("cannot verify protected firmware regions")
		}
		result[i] = fmt.Sprintf("%x", hash.Sum(nil))
	}
	return result, nil
}

type splDevice interface {
	io.ReaderAt
	io.WriterAt
	Sync() error
}

func writePrimarySPL(device splDevice, source, target []byte) error {
	if len(source) != proSPLSize || len(target) != proSPLSize || digest(target) != proSPLTargetSHA {
		return fmt.Errorf("invalid complete SPL package")
	}
	current := make([]byte, proSPLSize)
	if _, err := device.ReadAt(current, 0); err != nil {
		return err
	}
	if !bytes.Equal(source, current) {
		return fmt.Errorf("SPL changed immediately before write; stopped")
	}
	n, err := device.WriteAt(target, 0)
	if err != nil || n != len(target) {
		return fmt.Errorf("SPL write failed; keep power on and retry recovery: %w", errorsOrShortWrite(err))
	}
	if err = device.Sync(); err != nil {
		return err
	}
	if _, err = device.ReadAt(current, 0); err != nil {
		return err
	}
	if !bytes.Equal(current, target) {
		return fmt.Errorf("SPL readback mismatch; keep the device powered on")
	}
	return nil
}

func errorsOrShortWrite(err error) error {
	if err != nil {
		return err
	}
	return io.ErrShortWrite
}

func reconstructSPL(source []byte) ([]byte, error) {
	info, ok := proSPLPatches[digest(source)]
	if len(source) != proSPLSize || !ok {
		return nil, fmt.Errorf("unsupported factory SPL")
	}
	patch, err := proFirmware.ReadFile("firmware/" + info[0])
	if err != nil || digest(patch) != info[1] {
		return nil, fmt.Errorf("this installer lacks the verified firmware repair package")
	}
	target, err := applySPLDelta(source, patch)
	if err != nil {
		return nil, err
	}
	if digest(target) != proSPLTargetSHA || !bytes.Equal(source[0xcc00:], target[0xcc00:]) {
		return nil, fmt.Errorf("reconstructed SPL verification failed")
	}
	return target, nil
}

// BSDIFF40 sign-magnitude integers and three bounded bzip2 streams. Rebuild
// the entire signed target in memory; never apply delta records to the device.
func deltaInt(raw []byte) int64 {
	x := int64(binary.LittleEndian.Uint64(raw) & 0x7fffffffffffffff)
	if raw[7]&0x80 != 0 {
		return -x
	}
	return x
}

func applySPLDelta(source, patch []byte) ([]byte, error) {
	bad := func() ([]byte, error) { return nil, fmt.Errorf("invalid or incomplete SPL delta") }
	if len(patch) < 32 || len(patch) > proSPLSize || string(patch[:8]) != "BSDIFF40" {
		return bad()
	}
	controlSize, diffSize, size := deltaInt(patch[8:16]), deltaInt(patch[16:24]), deltaInt(patch[24:32])
	if controlSize < 0 || diffSize < 0 || controlSize > int64(len(patch)-32) || diffSize > int64(len(patch)-32)-controlSize || size != proSPLSize {
		return bad()
	}
	decode := func(data []byte, limit int64) ([]byte, error) {
		out, e := io.ReadAll(io.LimitReader(bzip2.NewReader(bytes.NewReader(data)), limit+1))
		if int64(len(out)) > limit {
			return nil, fmt.Errorf("oversized SPL delta stream")
		}
		return out, e
	}
	control, e1 := decode(patch[32:32+controlSize], 8*size)
	diff, e2 := decode(patch[32+controlSize:32+controlSize+diffSize], size)
	extra, e3 := decode(patch[32+controlSize+diffSize:], size)
	if e1 != nil || e2 != nil || e3 != nil {
		return bad()
	}
	target := make([]byte, size)
	var old, pos, di, ex int64
	for len(control) >= 24 && pos < size {
		x, y, seek := deltaInt(control[:8]), deltaInt(control[8:16]), deltaInt(control[16:24])
		control = control[24:]
		if x < 0 || y < 0 || x > size-pos || y > size-pos-x || x+y == 0 || x > int64(len(diff))-di || y > int64(len(extra))-ex || seek < -4*size || seek > 4*size {
			return bad()
		}
		for j := int64(0); j < x; j++ {
			target[pos+j] = diff[di+j]
			if old+j >= 0 && old+j < int64(len(source)) {
				target[pos+j] += source[old+j]
			}
		}
		pos += x
		old += x
		di += x
		copy(target[pos:pos+y], extra[ex:ex+y])
		pos += y
		ex += y
		old += seek
		if old < -8*size || old > 8*size {
			return bad()
		}
	}
	if pos != size || len(control) != 0 || di != int64(len(diff)) || ex != int64(len(extra)) {
		return bad()
	}
	return target, nil
}
