package nanokvm

import (
	"fmt"
	"io"
	"path/filepath"
	"strings"
	"time"

	"github.com/onekvm/onekvm-installer/internal/installer"
)

type Machine struct{}

func (Machine) Profile() string                 { return "nanokvm" }
func (Machine) Architectures() []string         { return []string{"riscv64"} }
func (Machine) ImageRepository() string         { return "onekvm/onekvm-release-nanokvm" }
func (Machine) InstallationTime() time.Duration { return 5 * time.Minute }

func (Machine) Matches(root string) bool {
	p := func(path string) string { return filepath.Join(root, path) }
	compatible := installer.ReadTrim(p("proc/device-tree/compatible"))
	model := installer.ReadTrim(p("proc/device-tree/model"))
	hw := installer.ReadTrim(p("etc/kvm/hw"))
	one := installer.ReadTrim(p("usr/share/onekvm/machine"))
	osRelease := installer.ReadTrim(p("etc/os-release"))
	// Early NanoKVM Cube images predate the OneKVM identity files and may
	// leave /etc/kvm/hw absent during Recovery.  The board's DT model is the
	// stable hardware identity in that state.
	legacyModel := strings.EqualFold(strings.TrimSpace(model), "LicheeRv Nano")
	evidence := hw == "alpha" || hw == "beta" || hw == "pcie" || hw == "lite" ||
		strings.Contains(one, "nanokvm") || strings.Contains(osRelease, "OneKVM") ||
		strings.Contains(strings.ToLower(model), "nanokvm") || (legacyModel && hw == "")
	sg200x := false
	for _, value := range strings.Split(compatible, "\x00") {
		if value == "cvitek,cv181x" || value == "sophgo,sg2002" {
			sg200x = true
		}
	}
	identity := strings.ToLower(compatible + model)
	return sg200x && evidence && !strings.Contains(identity, "nanokvm-pro") && !strings.Contains(identity, "nanokvm pro")
}

func (m Machine) Probe(root string) (installer.Device, error) {
	d, err := installer.ProbeMMC(root)
	if err != nil {
		return d, err
	}
	if !m.Matches(root) || d.Media != "SD" {
		return d, fmt.Errorf("unrecognized NanoKVM machine/media: %s / %s", d.Model, d.Media)
	}
	d.Profile = m.Profile()
	d.HardwareModel = HardwareModel(root)
	// The verified SG200x ROM/SPL chain boots mmc0. An external card or
	// a mounted FAT under NFS does not establish the source of the kernel.
	d.Automatic = d.Disk == "/dev/mmcblk0" && installer.RootDisk(root) == d.Disk
	return d, nil
}

func (m Machine) ValidateTarget(d installer.Device) error {
	if d.Profile != m.Profile() || !d.Automatic {
		return fmt.Errorf("automatic bootstrap unavailable for %s: a verified persistent boot entry is required", d.Profile)
	}
	// Recovery checks the actual FAT capacity against its kernel, environment
	// and FIP replacement. The old partition need not match a stock image size.
	if d.BootStart != 1 || d.BootSectors <= 0 || d.BootSectors > d.Bytes/512-d.BootStart {
		return fmt.Errorf("unsupported SG200x boot geometry %d:%d", d.BootStart, d.BootSectors)
	}
	if (d.Userdata != d.Disk+"p3" && d.Userdata != d.Disk+"p4") ||
		d.DataStart < d.BootStart+d.BootSectors || d.DataSectors <= 0 ||
		d.DataStart > d.Bytes/512-d.DataSectors || d.Bytes < (790528+65536)*512 {
		return fmt.Errorf("userdata / target capacity is incompatible with installation")
	}
	return nil
}

func (Machine) MatchesImage(name string) bool {
	name = strings.ToLower(name)
	return strings.HasSuffix(name, ".img") && strings.Contains(name, "nanokvm") &&
		!strings.Contains(name, "pro") && !strings.Contains(name, "emmc")
}

func (Machine) ExtractBootstrap(image, destination string, progress installer.Progress) error {
	return extractBootstrap(image, destination, progress)
}

func (Machine) ActivateRecovery(d installer.Device, req installer.Request, report installer.Reporter) error {
	var plan bootstrapPlan
	err := report("op.boot_check", func(installer.Progress) error {
		var err error
		plan, err = planBootstrap(d, req)
		return err
	})
	if err != nil {
		return err
	}
	return replaceBootstrap(plan, report)
}

func (Machine) CheckDisk(d installer.Device) error {
	if d.Profile != "nanokvm" || d.Disk != "/dev/mmcblk0" || d.Boot != d.Disk+"p1" ||
		(d.Userdata != d.Disk+"p3" && d.Userdata != d.Disk+"p4") {
		return fmt.Errorf("unsupported installation target identity")
	}
	sys := "/sys/class/block/" + filepath.Base(d.Disk)
	if installer.ReadTrim(sys+"/device/cid") != d.CID || installer.ReadTrim(sys+"/device/type") != "SD" {
		return fmt.Errorf("boot medium CID/media changed")
	}
	size, err := installer.ReadNumber(sys + "/size")
	if err != nil || size*512 != d.Bytes {
		return fmt.Errorf("boot medium capacity changed")
	}
	for part, geometry := range map[string][2]int64{d.Boot: {d.BootStart, d.BootSectors}, d.Userdata: {d.DataStart, d.DataSectors}} {
		path := "/sys/class/block/" + filepath.Base(part)
		start, e := installer.ReadNumber(path + "/start")
		if e != nil {
			return e
		}
		count, e := installer.ReadNumber(path + "/size")
		if e != nil {
			return e
		}
		if start != geometry[0] || count != geometry[1] {
			return fmt.Errorf("partition geometry changed for %s", part)
		}
	}
	return d.CanArm()
}

func (Machine) WritePayload(target io.ReadWriteSeeker, source io.Reader, info installer.Image, progress installer.Progress) error {
	if info.Profile != "nanokvm" {
		return fmt.Errorf("wrong image profile for NanoKVM SD writer")
	}
	return installer.WriteVerifiedRange(target, source, info, 0, progress)
}

func (Machine) Recover(report installer.Reporter) (bool, error) {
	cmdline := installer.ReadTrim("/proc/cmdline")
	if installer.ParentDisk(installer.CmdlineValue(cmdline, "root")) != "/dev/mmcblk0" || !(Machine{}).Matches("/") {
		return false, nil
	}
	return installer.RecoverMMC("/dev/mmcblk0p1", report)
}

func (Machine) Family() string { return "nanokvm" }
func (m Machine) Direct() bool { return false }
