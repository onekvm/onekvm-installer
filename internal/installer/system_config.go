package installer

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"
)

const SystemConfigName = "onekvm-system-config.json"
const maxSystemConfig = 4 * MiB

type SystemSnapshot struct {
	Data              []byte
	Access            AccessInfo
	Timezone, Version string
	SSHEnabled        bool
}

// Copy the canonical system configuration, including password hashes, MFA and
// keys. Extension login bindings belong to removed extensions and are excluded.
func SnapshotSystem(root string) (SystemSnapshot, error) {
	var result SystemSnapshot
	data, err := os.ReadFile(filepath.Join(root, "etc/onekvm/config.json"))
	if os.IsNotExist(err) {
		data, err = os.ReadFile(filepath.Join(root, "var/lib/onekvm/config.json"))
	}
	if err != nil {
		return result, err
	}
	defer clear(data)
	result.Data, result.Access, result.Timezone, err = systemConfiguration(data)
	if err != nil {
		return SystemSnapshot{}, err
	}
	if result.Timezone == "" {
		result.Timezone = ReadTrim(filepath.Join(root, "etc/timezone"))
		if result.Timezone == "" {
			link, _ := os.Readlink(filepath.Join(root, "etc/localtime"))
			if _, zone, ok := strings.Cut(link, "zoneinfo/"); ok {
				result.Timezone = zone
			}
		}
		if result.Timezone != "" {
			if _, err := (SystemSettings{Language: "en", Timezone: result.Timezone}).Normalize(); err != nil {
				return SystemSnapshot{}, err
			}
		}
	}
	_, err = os.Lstat(filepath.Join(root, "etc/systemd/system/sockets.target.wants/dropbear.socket"))
	if err != nil && !os.IsNotExist(err) {
		return SystemSnapshot{}, err
	}
	result.SSHEnabled = err == nil
	for _, line := range strings.Split(ReadTrim(filepath.Join(root, "etc/os-release")), "\n") {
		if value, ok := strings.CutPrefix(line, "VERSION_ID="); ok {
			result.Version = strings.Trim(value, "\"'")
		}
	}
	return result, nil
}

func systemConfiguration(data []byte) ([]byte, AccessInfo, string, error) {
	var access AccessInfo
	if len(data) == 0 || len(data) > maxSystemConfig {
		return nil, access, "", fmt.Errorf("invalid system configuration size")
	}
	var cfg map[string]json.RawMessage
	if err := json.Unmarshal(data, &cfg); err != nil || cfg == nil {
		return nil, access, "", fmt.Errorf("invalid system configuration JSON")
	}
	var version int
	if json.Unmarshal(cfg["schema_version"], &version) != nil || version < 1 {
		return nil, access, "", fmt.Errorf("system configuration has no schema version")
	}
	var auth struct {
		Users []map[string]json.RawMessage `json:"users"`
	}
	if err := json.Unmarshal(cfg["auth"], &auth); err != nil {
		return nil, access, "", fmt.Errorf("invalid system accounts")
	}
	for _, user := range auth.Users {
		delete(user, "plugins")
		if access.Username == "" {
			_ = json.Unmarshal(user["username"], &access.Username)
		}
	}
	// Preserve all auth fields rather than decoding them through an older
	// installer's schema, which would drop newer protection and MFA settings.
	var authRaw map[string]json.RawMessage
	if err := json.Unmarshal(cfg["auth"], &authRaw); err != nil {
		return nil, access, "", err
	}
	users, err := json.Marshal(auth.Users)
	if err != nil {
		return nil, access, "", err
	}
	if _, ok := authRaw["users"]; ok {
		authRaw["users"] = users
	}
	cfg["auth"], err = json.Marshal(authRaw)
	if err != nil {
		return nil, access, "", err
	}
	var network struct {
		Device, Hostname string
		PrimaryInterface string `json:"primary_interface"`
		TLSEnabled       *bool  `json:"tls_enabled"`
		HTTPPort         int    `json:"http_port"`
		HTTPSPort        int    `json:"https_port"`
		IPv4Mode         string `json:"ipv4_mode"`
		IPv4Address      string `json:"ipv4_address"`
		Interfaces       []struct {
			Name, Device string
			IPv4Mode     string `json:"ipv4_mode"`
			IPv4Address  string `json:"ipv4_address"`
		} `json:"interfaces"`
	}
	if err := json.Unmarshal(cfg["network"], &network); err != nil {
		return nil, access, "", fmt.Errorf("invalid system network settings")
	}
	access.Network = Network{Interface: network.Device, Mode: network.IPv4Mode, Address: network.IPv4Address, Hostname: network.Hostname}
	if network.TLSEnabled != nil {
		access.WebScheme, access.WebPort = "http", network.HTTPPort
		if *network.TLSEnabled {
			access.WebScheme, access.WebPort = "https", network.HTTPSPort
		}
	}
	if len(network.Interfaces) > 0 {
		selected := 0
		primary := network.PrimaryInterface
		if primary == "" {
			primary = network.Device
		}
		for i, link := range network.Interfaces {
			if primary != "" && (link.Name == primary || link.Device == primary) {
				selected = i
				break
			}
		}
		link := network.Interfaces[selected]
		name := link.Name
		if name == "" {
			name = link.Device
		}
		access.Network.Interface, access.Network.Mode, access.Network.Address = name, link.IPv4Mode, link.IPv4Address
	}
	var system struct {
		Timezone string `json:"timezone"`
	}
	if err := json.Unmarshal(cfg["system"], &system); err != nil {
		return nil, access, "", fmt.Errorf("invalid system settings")
	}
	if system.Timezone != "" {
		if _, err := (SystemSettings{Language: "en", Timezone: system.Timezone}).Normalize(); err != nil {
			return nil, access, "", err
		}
	}
	result, err := json.Marshal(cfg)
	return append(result, '\n'), access, system.Timezone, err
}

func checkConfigVersion(source string, image Image) error {
	parse := func(s string) ([3]int, bool) {
		var v [3]int
		parts := strings.Split(s, ".")
		if len(parts) != 3 {
			return v, false
		}
		for i, p := range parts {
			n, err := strconv.Atoi(p)
			if err != nil || n < 0 {
				return v, false
			}
			v[i] = n
		}
		return v, true
	}
	s, ok := parse(source)
	if !ok {
		return fmt.Errorf("cannot determine the installed system version for configuration migration")
	}
	t, ok := parse(strings.SplitN(image.BuildID, "-", 2)[0])
	if !ok {
		return fmt.Errorf("cannot determine the image version for configuration migration")
	}
	for i := range s {
		if t[i] < s[i] {
			return fmt.Errorf("configuration-preserving reinstallation requires the same or a newer system version")
		}
		if t[i] > s[i] {
			return nil
		}
	}
	return nil
}

func loadSystemConfig(dir, expected string) ([]byte, error) {
	data, err := os.ReadFile(filepath.Join(dir, SystemConfigName))
	if err != nil {
		return nil, err
	}
	hash := sha256.Sum256(data)
	if len(data) > maxSystemConfig || hex.EncodeToString(hash[:]) != expected {
		clear(data)
		return nil, fmt.Errorf("staged system configuration changed")
	}
	return data, nil
}

// This one-shot initramfs module runs after the image's storage initializer
// (15) and before Recovery/rootfs (20+). Only the seed hash enters the kernel;
// credentials remain in the temporary boot file, deleted after durable import.
func ConfigurationHook(req Request) ([]byte, error) {
	if len(req.ConfigSHA256) != 64 {
		return nil, fmt.Errorf("invalid configuration snapshot hash")
	}
	if _, err := hex.DecodeString(req.ConfigSHA256); err != nil {
		return nil, err
	}
	var extras strings.Builder
	if req.SSHEnabled {
		extras.WriteString("mkdir -p \"$dest/etc/systemd/system/sockets.target.wants\" || fatal 'Cannot restore SSH startup'\nln -sf /usr/lib/systemd/system/dropbear.socket \"$dest/etc/systemd/system/sockets.target.wants/dropbear.socket\" || fatal 'Cannot restore SSH startup'\n")
	}
	if req.Timezone != "" {
		if _, err := (SystemSettings{Language: "en", Timezone: req.Timezone}).Normalize(); err != nil {
			return nil, err
		}
		fmt.Fprintf(&extras, "ln -sf %s \"$dest/etc/localtime\" || fatal 'Cannot restore time zone'\nprintf '%%s\\n' %s >\"$dest/etc/timezone\" || fatal 'Cannot restore time zone'\n", cliQuote("/usr/share/zoneinfo/"+req.Timezone), cliQuote(req.Timezone))
	}
	script := `onekvmrestore_enabled() { return 0; }
onekvmrestore_run() {
    . /usr/lib/onekvm/storage-layout.sh
    onekvm_storage_select || fatal 'Cannot identify configuration destination'
    boot=/run/onekvm-config-boot
    data=/run/onekvm-config-data
    mkdir -p "$boot" "$data" || fatal 'Cannot create configuration mountpoints'
    mount -t vfat -o ro,nodev,nosuid,noexec "$loader" "$boot" || fatal 'Cannot mount configuration source'
    if [ ! -f "$boot/onekvm-system-config.json" ]; then
        umount "$boot" || fatal 'Cannot release configuration source'
        return 0
    fi
    printf '%s  %s\n' HASH "$boot/onekvm-system-config.json" | sha256sum -c - >/dev/null || fatal 'Configuration checksum mismatch'
    mount -o rw,nodev,nosuid,noexec "$userdata" "$data" || fatal 'Cannot mount configuration destination'
    dest=$data/upper
    case "${bootparam_overlayrootisolated}" in
        A|B) dest=$data/slots/$bootparam_overlayrootisolated/upper ;;
        '') ;;
        *) fatal 'Invalid isolated configuration slot' ;;
    esac
    # These directories become / and /etc in overlayfs. System services
    # running without root must be able to traverse them.
    umask 022
    mkdir -p "$dest/etc/onekvm" || fatal 'Cannot create system configuration directory'
    umask 077
    if [ -e "$dest/etc/onekvm/config.json" ]; then
        cmp -s "$boot/onekvm-system-config.json" "$dest/etc/onekvm/config.json" || fatal 'Refusing to replace an existing system configuration'
    else
        cp "$boot/onekvm-system-config.json" "$dest/etc/onekvm/config.json.tmp" || fatal 'Cannot restore system configuration'
        chmod 0600 "$dest/etc/onekvm/config.json.tmp" || fatal 'Cannot protect system configuration'
        mv "$dest/etc/onekvm/config.json.tmp" "$dest/etc/onekvm/config.json" || fatal 'Cannot publish system configuration'
    fi
    printf 'restored\n' >"$dest/etc/onekvm/config.firstboot-done" || fatal 'Cannot record configuration import'
    umask 022
EXTRAS
    sync
    umount "$data" || fatal 'Cannot release configuration destination'
    mount -o remount,rw "$boot" || fatal 'Cannot consume configuration source'
    rm "$boot/onekvm-system-config.json" || fatal 'Cannot consume configuration source'
    sync
    umount "$boot" || fatal 'Cannot release configuration source'
}
`
	script = strings.ReplaceAll(strings.ReplaceAll(script, "HASH", req.ConfigSHA256), "EXTRAS", extras.String())
	return compressedArchive([]archiveFile{{"init.d", syscallDirMode, nil}, {"init.d/18-onekvmrestore", 0100755, []byte(script)}})
}

const syscallDirMode = 0040755
