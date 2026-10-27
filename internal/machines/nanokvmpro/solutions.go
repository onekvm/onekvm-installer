package nanokvmpro

import (
	"context"
	"os"
	"path/filepath"
	"strings"

	"github.com/onekvm/onekvm-installer/internal/installer"
)

// Checks follow the selected image, never the previous contents of the card.
func (Machine) SolutionHooks() []installer.SolutionHook {
	return []installer.SolutionHook{{ID: "pro.sd-boot", Check: checkSDBoot}}
}

func checkSDBoot(ctx context.Context, in installer.CheckContext) (installer.CheckResult, error) {
	var result installer.CheckResult
	if in.Image.Profile != "pro-sd" || in.ImagePath == "" {
		return result, nil
	}
	model := strings.ToLower(installer.ReadTrim(filepath.Join(in.Root, "proc/device-tree/model")))
	if !proMatches(in.Root) || strings.Contains(model, "atx") || strings.Contains(model, "pcie") {
		return result, nil
	}
	result.Supported = true
	if !proSDPresent(in.Root) {
		result.Issues = []installer.Issue{{
			ID: "pro.sd-missing", TitleKey: "issue.pro.sd-missing.title", DescriptionKey: "issue.pro.sd-missing.description", Blocking: true,
			Solutions: []installer.Solution{{ID: "pro.sd-missing.insert", TitleKey: "issue.pro.sd-missing.action", BodyKey: "issue.pro.sd-missing.solution"}},
		}}
		return result, nil
	}
	root := installer.CmdlineValue(installer.ReadTrim(filepath.Join(in.Root, "proc/cmdline")), "root")
	disk := installer.ParentDisk(root)
	if disk == "" || installer.ReadTrim(filepath.Join(in.Root, "sys/class/block", filepath.Base(disk), "device/type")) != "MMC" {
		return result, nil // Already on SD, NFS, or an unconfirmed root medium.
	}
	f, err := os.Open(filepath.Join(in.Root, "dev", filepath.Base(disk)))
	if err != nil {
		return result, err
	}
	defer f.Close()
	spl := make([]byte, proSPLSize)
	if _, err := f.ReadAt(spl, 0); err != nil {
		return result, err
	}
	if digest(spl) == proSPLTargetSHA {
		return result, nil
	}
	result.Issues = []installer.Issue{{
		ID: "pro.sd-boot", TitleKey: "issue.pro.sd-boot.title", DescriptionKey: "issue.pro.sd-boot.description",
		Solutions: []installer.Solution{
			{ID: "pro.sd-boot.card", TitleKey: "issue.pro.sd-boot.card.title", BodyKey: "issue.pro.sd-boot.solution"},
			{ID: "pro.sd-boot.firmware", TitleKey: "issue.pro.sd-boot.firmware.title", BodyKey: "issue.pro.sd-boot.firmware.solution", RepairID: "pro.sd-boot.firmware"},
		},
	}}
	return result, ctx.Err()
}

func proSDPresent(root string) bool {
	paths, _ := filepath.Glob(filepath.Join(root, "sys/class/block/mmcblk*/device/type"))
	for _, path := range paths {
		if installer.ReadTrim(path) != "SD" {
			continue
		}
		name := filepath.Base(filepath.Dir(filepath.Dir(path)))
		if _, err := os.Stat(filepath.Join(root, "dev", name)); err == nil {
			return true
		}
	}
	return false
}
