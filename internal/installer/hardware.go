package installer

import (
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
)

type Mount struct{ Device, Path, Type string }
type Device struct {
	Profile       string `json:"profile"`
	Reinstall     bool   `json:"reinstall,omitempty"`
	Model         string `json:"model"`
	HardwareModel string `json:"hardware_model"`
	Disk          string `json:"disk"`
	CID           string `json:"cid"`
	Media         string `json:"media"`
	Bytes         int64  `json:"bytes"`
	Boot          string `json:"boot_device"`
	BootMount     string `json:"boot_mount"`
	BootStart     int64  `json:"boot_start"`
	BootSectors   int64  `json:"boot_sectors"`
	Userdata      string `json:"userdata_device"`
	DataMount     string `json:"userdata_mount"`
	DataRoot      string `json:"userdata_root,omitempty"`
	DataType      string `json:"userdata_type"`
	DataStart     int64  `json:"userdata_start"`
	DataSectors   int64  `json:"userdata_sectors"`
	Automatic     bool   `json:"automatic_bootstrap"`
	SystemCID     string `json:"system_cid,omitempty"`
	SystemBytes   int64  `json:"system_bytes,omitempty"`
}

func unescapeMount(s string) string {
	return strings.NewReplacer(`\040`, " ", `\011`, "\t", `\012`, "\n", `\134`, `\`).Replace(s)
}
func Mounts(root string) ([]Mount, error) {
	b, err := os.ReadFile(filepath.Join(root, "proc/mounts"))
	if err != nil {
		return nil, err
	}
	var out []Mount
	for _, line := range strings.Split(string(b), "\n") {
		v := strings.Fields(line)
		if len(v) >= 3 {
			out = append(out, Mount{unescapeMount(v[0]), unescapeMount(v[1]), v[2]})
		}
	}
	return out, nil
}
func ReadTrim(path string) string {
	b, _ := os.ReadFile(path)
	return strings.TrimSpace(strings.TrimRight(string(b), "\x00"))
}
func ReadNumber(path string) (int64, error) { return strconv.ParseInt(ReadTrim(path), 10, 64) }

var mmcPartition = regexp.MustCompile(`^(mmcblk[0-9]+)p([1-9][0-9]*)$`)

func ParentDisk(device string) string {
	m := mmcPartition.FindStringSubmatch(filepath.Base(device))
	if m == nil {
		return ""
	}
	return "/dev/" + m[1]
}

func CmdlineValue(cmdline, key string) string {
	for _, v := range strings.Fields(cmdline) {
		if strings.HasPrefix(v, key+"=") {
			return strings.TrimPrefix(v, key+"=")
		}
	}
	return ""
}

func Probe(root string) (Device, error) {
	for _, machine := range Machines() {
		if machine.Matches(root) {
			return machine.Probe(root)
		}
	}
	model := ReadTrim(filepath.Join(root, "proc/device-tree/model"))
	return Device{}, fmt.Errorf("unrecognized machine: %s", strings.ReplaceAll(model, "\x00", ", "))
}

// Shared MMC inventory. Root is / on a device or a synthetic test filesystem.
// Machine implementations decide whether this layout is suitable to install.
func ProbeMMC(root string) (Device, error) {
	var d Device
	p := func(s string) string { return filepath.Join(root, s) }
	mounts, err := Mounts(root)
	if err != nil {
		return d, err
	}
	for _, m := range mounts {
		if m.Path == "/boot" && m.Type == "vfat" {
			d.Boot = m.Device
			d.BootMount = m.Path
		}
	}
	d.Disk = ParentDisk(d.Boot)
	if d.Disk == "" {
		return d, fmt.Errorf("cannot identify MMC boot disk from mounted /boot; refusing to guess")
	}
	if filepath.Base(d.Boot) != filepath.Base(d.Disk)+"p1" {
		return d, fmt.Errorf("unsupported boot partition")
	}
	sys := p("sys/class/block/" + filepath.Base(d.Disk))
	d.CID = ReadTrim(sys + "/device/cid")
	d.Media = ReadTrim(sys + "/device/type")
	sectors, err := ReadNumber(sys + "/size")
	if err != nil {
		return d, err
	}
	d.Bytes = sectors * 512
	if len(d.CID) != 32 {
		return d, fmt.Errorf("missing MMC CID")
	}
	compatible := ReadTrim(p("proc/device-tree/compatible"))
	model := ReadTrim(p("proc/device-tree/model"))
	if model == "" {
		model = compatible
	}
	d.Model = strings.ReplaceAll(model, "\x00", ", ")
	d.Reinstall, err = InstalledSystem(root)
	if err != nil {
		return d, err
	}
	d.BootStart, err = ReadNumber(p("sys/class/block/" + filepath.Base(d.Boot) + "/start"))
	if err != nil {
		return d, err
	}
	d.BootSectors, err = ReadNumber(p("sys/class/block/" + filepath.Base(d.Boot) + "/size"))
	if err != nil {
		return d, err
	}
	// Prefer the established userdata mountpoints. Multiple candidates mean
	// the user must resolve the layout; never choose the largest partition.
	var candidates []Mount
	for _, m := range mounts {
		if (m.Path == "/data" || m.Path == "/mnt/storage") && ParentDisk(m.Device) == d.Disk && m.Device != d.Boot &&
			(m.Type == "ext4" || m.Type == "f2fs" || m.Type == "exfat") {
			candidates = append(candidates, m)
		}
	}
	if len(candidates) != 1 {
		return d, fmt.Errorf("expected one userdata partition mounted at /data or /mnt/storage, found %d", len(candidates))
	}
	m := candidates[0]
	if RootDevice(root) == m.Device {
		return d, fmt.Errorf("userdata resolves to the running root partition")
	}
	d.Userdata = m.Device
	d.DataMount = m.Path
	d.DataType = m.Type
	// Installed OneKVM exposes /storage through a bind mount. Recovery mounts
	// the whole filesystem, so retain the bind root when locating the stage.
	if info, e := os.ReadFile(p("proc/self/mountinfo")); e == nil {
		for _, line := range strings.Split(string(info), "\n") {
			fields := strings.Fields(line)
			if len(fields) < 6 || unescapeMount(fields[4]) != m.Path {
				continue
			}
			d.DataRoot = unescapeMount(fields[3])
			if !filepath.IsAbs(d.DataRoot) || filepath.Clean(d.DataRoot) != d.DataRoot {
				return d, fmt.Errorf("invalid userdata mount root")
			}
			if d.DataRoot == "/" {
				d.DataRoot = ""
			}
		}
	} else if !os.IsNotExist(e) {
		return d, e
	}
	d.DataStart, err = ReadNumber(p("sys/class/block/" + filepath.Base(d.Userdata) + "/start"))
	if err != nil {
		return d, err
	}
	d.DataSectors, err = ReadNumber(p("sys/class/block/" + filepath.Base(d.Userdata) + "/size"))
	if err != nil {
		return d, err
	}
	return d, nil
}

func (d Device) CanArm() error {
	machine, err := MachineFor(d.Profile)
	if err != nil {
		return err
	}
	return machine.ValidateTarget(d)
}

func SameDisk(a, b Device) bool {
	return a.Reinstall == b.Reinstall && a.Profile == b.Profile && a.Disk == b.Disk && a.CID == b.CID && a.Media == b.Media && a.Bytes == b.Bytes && a.Boot == b.Boot && a.BootStart == b.BootStart &&
		a.BootSectors == b.BootSectors && a.Userdata == b.Userdata && a.DataRoot == b.DataRoot && a.DataStart == b.DataStart && a.DataSectors == b.DataSectors && a.SystemCID == b.SystemCID && a.SystemBytes == b.SystemBytes
}

// ProbeTarget binds an image profile to hardware; a filename cannot select an
// arbitrary disk. Pro can select either medium from its stock eMMC system.
func ProbeTarget(root string, current Device, profile string) (Device, error) {
	previous, err := MachineFor(current.Profile)
	if err != nil {
		return Device{}, err
	}
	machine, err := MachineFor(profile)
	if err != nil {
		return Device{}, err
	}
	if previous.Family() != machine.Family() {
		return Device{}, fmt.Errorf("wrong image family: %s", profile)
	}
	return machine.Probe(root)
}

func DirectInstall(d Device) bool {
	m, err := MachineFor(d.Profile)
	return err == nil && m.Direct() && !d.Reinstall
}

// Resolve the actual mounted root, or the boot parameter's kernel partition
// identity before root is mounted. Never infer it from /boot.
func RootDisk(root string) string { return ParentDisk(RootDevice(root)) }

func RootDevice(root string) string {
	value := CmdlineValue(ReadTrim(filepath.Join(root, "proc/cmdline")), "root")
	if disk := ParentDisk(value); disk != "" {
		return value
	}
	key, want, ok := strings.Cut(value, "=")
	if !ok || (key != "PARTUUID" && key != "PARTLABEL") {
		return ""
	}
	// Firstboot can add partitions with BLKPG before rebooting. Those sysfs
	// uevents have no PARTUUID metadata; the mounted root remains authoritative.
	if mounts, err := Mounts(root); err == nil {
		for _, mount := range mounts {
			if mount.Path != "/" {
				continue
			}
			if ParentDisk(mount.Device) != "" {
				return mount.Device
			}
			if mount.Type == "overlay" {
				for _, lower := range mounts {
					if lower.Path == "/mnt/.rofs" && (lower.Type == "squashfs" || lower.Type == "erofs") && ParentDisk(lower.Device) != "" {
						return lower.Device
					}
				}
			}
		}
	}
	if key == "PARTLABEL" {
		key = "PARTNAME"
	}
	paths, _ := filepath.Glob(filepath.Join(root, "sys/class/block/mmcblk*p*/uevent"))
	found := ""
	for _, path := range paths {
		for _, line := range strings.Split(ReadTrim(path), "\n") {
			if line == key+"="+want {
				device := "/dev/" + filepath.Base(filepath.Dir(path))
				if found != "" && found != device {
					return ""
				}
				found = device
			}
		}
	}
	return found
}
