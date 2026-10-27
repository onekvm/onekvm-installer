package nanokvmpro

import (
	"fmt"
	"io"
	"path/filepath"
	"strings"
	"time"

	"github.com/onekvm/onekvm-installer/internal/installer"
)

const proPrefix = int64(22806528)
const proBootStart = int64(306688)
const proBootSectors = int64(262144)
const proRootStart = int64(568832)
const proSelector = int64(156958720)
const proBackupKernel = int64(89915392)

type Machine struct{ media string }

func (m Machine) Profile() string               { return "pro-" + m.media }
func (Machine) Architectures() []string         { return []string{"arm64"} }
func (Machine) ImageRepository() string         { return "onekvm/onekvm-release-nanokvm" }
func (Machine) InstallationTime() time.Duration { return 5 * time.Minute }
func proMatches(root string) bool {
	identity := strings.ToLower(installer.ReadTrim(filepath.Join(root, "proc/device-tree/model")) + " " + installer.ReadTrim(filepath.Join(root, "proc/device-tree/compatible")))
	return strings.Contains(identity, "nanokvm-pro") || strings.Contains(identity, "nanokvm pro")
}
func (m Machine) Matches(root string) bool {
	if !proMatches(root) {
		return false
	}
	installed, _ := installer.InstalledSystem(root)
	if installed {
		return m.matchesInstalledMedium(root, installer.RootDisk(root))
	}
	return m.media == "emmc"
}

func (m Machine) Probe(root string) (installer.Device, error) {
	installed, err := installer.InstalledSystem(root)
	if err != nil {
		return installer.Device{}, err
	}
	if installed {
		return m.probeInstalled(root)
	}

	d := installer.Device{Profile: m.Profile(), Boot: "/dev/mmcblk0p16", BootMount: "/boot", BootStart: proBootStart, BootSectors: proBootSectors,
		Userdata: "/dev/mmcblk0p17", DataMount: "/", DataType: "ext4"}
	if !proMatches(root) {
		return d, fmt.Errorf("unrecognized NanoKVM Pro")
	}
	d.Model = installer.ReadTrim(filepath.Join(root, "proc/device-tree/model"))
	variant := "Desk"
	if strings.Contains(strings.ToLower(d.Model), "atx") || strings.Contains(strings.ToLower(d.Model), "pcie") {
		return d, fmt.Errorf("Pro PCIe requires its own DTB; current installation images support Pro Desk")
	}
	d.HardwareModel = installer.ReadMachineIdentity(root, installer.MachineIdentity{Machine: "nanokvm-pro", Vendor: "Sipeed", Name: "NanoKVM Pro", Variant: variant}).ModelLabel()
	mounts, err := installer.Mounts(root)
	if err != nil {
		return d, err
	}
	rootOK, bootOK := false, false
	for _, mount := range mounts {
		rootOK = rootOK || (mount.Path == "/" && mount.Device == d.Userdata && mount.Type == "ext4")
		bootOK = bootOK || (mount.Path == d.BootMount && mount.Device == d.Boot && mount.Type == "vfat")
	}
	if !rootOK || !bootOK || installer.CmdlineValue(installer.ReadTrim(filepath.Join(root, "proc/cmdline")), "root") != d.Userdata {
		return d, fmt.Errorf("Pro installation requires the stock eMMC system and mounted /boot")
	}
	p := func(name string) string { return filepath.Join(root, "sys/class/block", name) }
	if installer.ReadTrim(p("mmcblk0")+"/device/type") != "MMC" {
		return d, fmt.Errorf("Pro system medium is not eMMC")
	}
	d.SystemCID = installer.ReadTrim(p("mmcblk0") + "/device/cid")
	sectors, err := installer.ReadNumber(p("mmcblk0") + "/size")
	if err != nil {
		return d, err
	}
	d.SystemBytes = sectors * 512
	// Validate the vendor cmdline partition layout before touching any boot data.
	for name, want := range map[string]int64{"mmcblk0p7/start": 9728, "mmcblk0p7/size": 2048, "mmcblk0p16/start": proBootStart, "mmcblk0p16/size": proBootSectors, "mmcblk0p17/start": proRootStart} {
		got, e := installer.ReadNumber(p(name))
		if e != nil || got != want {
			return d, fmt.Errorf("unsupported Pro factory partition: %s", name)
		}
	}
	d.DataStart = proRootStart
	d.DataSectors, err = installer.ReadNumber(p("mmcblk0p17/size"))
	if err != nil {
		return d, err
	}
	d.Disk = "/dev/mmcblk0"
	d.Media = "MMC"
	d.CID = d.SystemCID
	d.Bytes = d.SystemBytes
	if m.media == "sd" {
		d.Disk = "/dev/mmcblk1"
		d.Media = "SD"
		if installer.ReadTrim(p("mmcblk1")+"/device/type") != "SD" {
			return d, fmt.Errorf("insert an SD card before selecting a Pro SD image")
		}
		d.CID = installer.ReadTrim(p("mmcblk1") + "/device/cid")
		sectors, err = installer.ReadNumber(p("mmcblk1") + "/size")
		if err != nil {
			return d, err
		}
		d.Bytes = sectors * 512
	}
	d.Automatic = true
	return d, m.ValidateTarget(d)
}

func (m Machine) ValidateTarget(d installer.Device) error {
	if d.Reinstall {
		return m.validateInstalled(d)
	}
	wantDisk, wantMedia := "/dev/mmcblk0", "MMC"
	if m.media == "sd" {
		wantDisk, wantMedia = "/dev/mmcblk1", "SD"
	}
	if d.Profile != m.Profile() || d.Disk != wantDisk || d.Media != wantMedia || !d.Automatic || len(d.CID) != 32 || len(d.SystemCID) != 32 ||
		d.Boot != "/dev/mmcblk0p16" || d.Userdata != "/dev/mmcblk0p17" || d.BootStart != proBootStart || d.BootSectors != proBootSectors || d.DataStart != proRootStart ||
		d.DataSectors <= 0 || d.SystemBytes/512 != d.DataStart+d.DataSectors || d.Bytes < 900*installer.MiB {
		return fmt.Errorf("unsupported Pro installation target/layout")
	}
	return nil
}
func (m Machine) CheckDisk(d installer.Device) error {
	if err := m.ValidateTarget(d); err != nil {
		return err
	}
	// Also works before mounting anything in the installation initramfs.
	return checkProDisk(d)
}
func checkProDisk(d installer.Device) error {
	if d.Reinstall {
		return checkInstalledDisk(d)
	}

	for _, identity := range []struct {
		disk, cid, media string
		bytes            int64
	}{{d.Disk, d.CID, d.Media, d.Bytes}, {"/dev/mmcblk0", d.SystemCID, "MMC", d.SystemBytes}} {
		p := "/sys/class/block/" + filepath.Base(identity.disk)
		count, err := installer.ReadNumber(p + "/size")
		if err != nil || count*512 != identity.bytes || installer.ReadTrim(p+"/device/cid") != identity.cid || installer.ReadTrim(p+"/device/type") != identity.media {
			return fmt.Errorf("Pro storage identity changed")
		}
	}
	for part, want := range map[string][2]int64{d.Boot: {d.BootStart, d.BootSectors}, d.Userdata: {d.DataStart, d.DataSectors}, "/dev/mmcblk0p7": {9728, 2048}} {
		p := "/sys/class/block/" + filepath.Base(part)
		start, e := installer.ReadNumber(p + "/start")
		if e != nil {
			return e
		}
		count, e := installer.ReadNumber(p + "/size")
		if e != nil || start != want[0] || count != want[1] {
			return fmt.Errorf("Pro factory partition geometry changed")
		}
	}
	return nil
}
func (m Machine) MatchesImage(name string) bool {
	name = strings.ToLower(filepath.Base(name))
	prefix := "onekvm-nanokvm-pro-" + m.media
	return name == prefix+".img" || (strings.HasPrefix(name, prefix+"-") && strings.HasSuffix(name, ".img"))
}
func (Machine) ImportNetwork(root, iface string) (installer.Network, bool, error) {
	return installer.Network{Interface: iface, Hostname: installer.CurrentHostname(root)}, false, nil
}
func (m Machine) ExtractBootstrap(image, destination string, p installer.Progress) error {
	return extractProBootstrap(image, destination, m.Profile(), p)
}
func (m Machine) ActivateRecovery(d installer.Device, req installer.Request, report installer.Reporter) error {
	return activateProRecovery(d, req, report)
}
func (m Machine) Recover(report installer.Reporter) (bool, error) {
	if !proMatches("/") {
		return false, nil
	}
	root := installer.CmdlineValue(installer.ReadTrim("/proc/cmdline"), "root")
	if m.media == "emmc" && root == "/dev/mmcblk0p17" {
		return installer.RecoverMMC("/dev/mmcblk0p16", report)
	}
	disk := installer.ParentDisk(root)
	if !m.matchesInstalledMedium("/", disk) {
		return false, nil
	}
	return installer.RecoverMMC(disk+"p1", report)
}
func (m Machine) WritePayload(target io.ReadWriteSeeker, source io.Reader, info installer.Image, p installer.Progress) error {
	if info.Profile != m.Profile() {
		return fmt.Errorf("wrong image profile for Pro writer")
	}
	skip := int64(0)
	if m.media == "emmc" {
		skip = proPrefix
	}
	return installer.WriteVerifiedRange(target, source, info, skip, p)
}

// Probe a saved target again without inferring it from whichever disk mounted /boot.
func (m Machine) PrepareBootstrap(dir string, req installer.Request) error {
	return bindProBootstrap(dir, req)
}

// Machines returns the two image targets of the same Pro hardware family.
func Machines() []installer.Machine {
	return []installer.Machine{Machine{media: "emmc"}, Machine{media: "sd"}}
}

func (Machine) Family() string { return "nanokvm-pro" }
func (m Machine) Direct() bool { return m.media == "sd" }

func (m Machine) FinalizeRecovery(d installer.Device) error {
	if d.Reinstall {
		return nil
	}
	if m.media != "emmc" {
		return fmt.Errorf("unexpected recovery medium")
	}
	env, err := proReadEnvironment()
	if err != nil {
		return err
	}
	return proSavePriority(d, env)
}

func (Machine) BootstrapFiles() []string {
	return []string{"Image", "board.dtb", "initramfs.uimg", "install.scr"}
}
