package installer

import (
	"bufio"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"net"
	"os"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"unicode"
	"unicode/utf8"
)

// Wi-Fi credentials are independent of Ethernet and never appear in JSON or
// the access summary. Only the private firstboot file carries the password.
type WiFiNetwork struct {
	Interface   string `json:"interface"`
	SSID        string `json:"ssid"`
	Password    string `json:"-"`
	PSK         string `json:"-"`
	AutoConnect bool   `json:"auto_connect"`
	Hidden      bool   `json:"hidden"`
	Priority    int    `json:"priority"`
	BSSID       string `json:"bssid,omitempty"`
}

type WiFiSnapshot struct {
	Interfaces []string
	Networks   []WiFiNetwork
}

var wifiInterfacePattern = regexp.MustCompile(`^wlan[0-9]+$`)

func validateWiFi(networks []WiFiNetwork) error {
	counts, seen := map[string]int{}, map[string]bool{}
	for _, n := range networks {
		if !wifiInterfacePattern.MatchString(n.Interface) || len(n.Interface) > 15 {
			return fmt.Errorf("Wi-Fi interface must be named wlanX")
		}
		if !utf8.ValidString(n.SSID) || len(n.SSID) < 1 || len(n.SSID) > 32 || strings.ContainsFunc(n.SSID, unicode.IsControl) {
			return fmt.Errorf("Wi-Fi SSID must contain 1–32 UTF-8 bytes without control characters")
		}
		if n.Password != "" && n.PSK != "" {
			return fmt.Errorf("Wi-Fi password and PSK are mutually exclusive")
		}
		if n.Password != "" &&
			(!utf8.ValidString(n.Password) || len(n.Password) < 8 || len(n.Password) > 63 || strings.ContainsFunc(n.Password, unicode.IsControl)) {
			return fmt.Errorf("Wi-Fi password must contain 8–63 UTF-8 bytes")
		}
		rawPSK, err := hex.DecodeString(n.PSK)
		if n.PSK != "" && (len(n.PSK) != 64 || err != nil || len(rawPSK) != 32) {
			return fmt.Errorf("Wi-Fi PSK must contain 64 hexadecimal digits")
		}
		if n.Priority < 0 || n.Priority > 999 {
			return fmt.Errorf("Wi-Fi priority must be 0–999")
		}
		if n.BSSID != "" {
			mac, err := net.ParseMAC(n.BSSID)
			if err != nil || len(mac) != 6 || len(n.BSSID) != 17 || mac[0]&1 != 0 || n.BSSID == "00:00:00:00:00:00" {
				return fmt.Errorf("Wi-Fi BSSID must be a unicast MAC address")
			}
		}
		key := n.Interface + "\x00" + n.SSID
		counts[n.Interface]++
		if seen[key] || counts[n.Interface] > 64 || len(counts) > 16 {
			return fmt.Errorf("duplicate or too many saved Wi-Fi networks")
		}
		seen[key] = true
	}
	return nil
}

// CopyWiFi reads persistent wpa_supplicant profiles without scanning, bringing
// links up or consulting the transient association state. Disconnected saved
// networks remain available after installation.
func CopyWiFi(root string) (WiFiSnapshot, error) {
	var result WiFiSnapshot
	entries, err := os.ReadDir(filepath.Join(root, "sys/class/net"))
	if errors.Is(err, os.ErrNotExist) {
		return result, nil
	}
	if err != nil {
		return result, err
	}
	for _, entry := range entries {
		name := entry.Name()
		if !wifiInterfacePattern.MatchString(name) || len(name) > 15 {
			continue
		}
		if _, err := os.Stat(filepath.Join(root, "sys/class/net", name, "wireless")); err != nil {
			continue
		}
		result.Interfaces = append(result.Interfaces, name)
		paths := []string{"etc/wpa_supplicant/wpa_supplicant-" + name + ".conf"}
		if name == "wlan0" {
			paths = append(paths, "etc/wpa_supplicant/wpa_supplicant.conf", "etc/wpa_supplicant.conf")
		}
		for _, path := range paths {
			f, err := os.Open(filepath.Join(root, path))
			if errors.Is(err, os.ErrNotExist) {
				continue
			}
			if err != nil {
				return result, fmt.Errorf("cannot read saved Wi-Fi settings for %s", name)
			}
			data, err := io.ReadAll(io.LimitReader(f, 65537))
			f.Close()
			if err != nil || len(data) > 65536 {
				clear(data)
				return result, fmt.Errorf("cannot read saved Wi-Fi settings for %s (64 KiB limit)", name)
			}
			profiles, err := parseWPANetworks(string(data), name)
			clear(data)
			if err != nil {
				// Never include source lines or credentials in error output.
				return result, fmt.Errorf("cannot copy saved Wi-Fi settings for %s: %w; configure Wi-Fi separately", name, err)
			}
			result.Networks = append(result.Networks, profiles...)
			break // The interface-specific file takes precedence over defaults.
		}
	}
	return result, validateWiFi(result.Networks)
}

func parseWPANetworks(text, iface string) ([]WiFiNetwork, error) {
	var result []WiFiNetwork
	var fields map[string]string
	scan := bufio.NewScanner(strings.NewReader(text))
	for scan.Scan() {
		line := strings.TrimSpace(scan.Text())
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		if strings.HasPrefix(line, "}") {
			if fields == nil || strings.TrimSpace(strings.SplitN(line[1:], "#", 2)[0]) != "" {
				return nil, fmt.Errorf("invalid network block")
			}
			n, configured, err := wpaNetwork(fields, iface)
			if err != nil {
				return nil, err
			}
			if configured {
				result = append(result, n)
			}
			fields = nil
			continue
		}
		key, value, ok := strings.Cut(line, "=")
		if !ok {
			return nil, fmt.Errorf("invalid configuration line")
		}
		key, value = strings.TrimSpace(key), strings.TrimSpace(value)
		if !strings.HasPrefix(value, `"`) && !strings.HasPrefix(value, `P"`) {
			value = strings.TrimSpace(strings.SplitN(value, "#", 2)[0])
		}
		if key == "network" {
			if fields != nil || value != "{" {
				return nil, fmt.Errorf("invalid network block")
			}
			fields = map[string]string{}
		} else if fields != nil {
			if _, exists := fields[key]; exists {
				return nil, fmt.Errorf("duplicate Wi-Fi setting")
			}
			fields[key] = value
		}
	}
	if scan.Err() != nil || fields != nil {
		return nil, fmt.Errorf("incomplete Wi-Fi configuration")
	}
	return result, validateWiFi(result)
}

func wpaString(raw string, ssid bool) (string, error) {
	if strings.HasPrefix(raw, `"`) || strings.HasPrefix(raw, `P"`) {
		end := strings.LastIndex(raw, `"`)
		start := strings.Index(raw, `"`)
		if end <= start || (strings.TrimSpace(raw[end+1:]) != "" && !strings.HasPrefix(strings.TrimSpace(raw[end+1:]), "#")) {
			return "", fmt.Errorf("invalid quoted Wi-Fi value")
		}
		if start == 1 {
			value, err := strconv.Unquote(raw[1 : end+1])
			if err != nil {
				return "", fmt.Errorf("unsupported escaped Wi-Fi value")
			}
			return value, nil
		}
		// Ordinary wpa_supplicant quoted values preserve literal backslashes.
		return raw[1:end], nil
	}
	value, err := hex.DecodeString(raw)
	if err != nil || (!ssid && len(value) != 32) {
		return "", fmt.Errorf("invalid hexadecimal Wi-Fi value")
	}
	if ssid {
		return string(value), nil
	}
	return raw, nil
}

func wpaNetwork(fields map[string]string, iface string) (WiFiNetwork, bool, error) {
	n := WiFiNetwork{Interface: iface, AutoConnect: true}
	var err error
	if fields["ssid"] == "" || fields["ssid"] == `""` {
		return n, false, nil // Unconfigured factory template.
	}
	n.SSID, err = wpaString(fields["ssid"], true)
	if err != nil {
		return n, false, err
	}
	for key := range fields {
		if strings.HasPrefix(key, "wep_") || key == "eap" || key == "sae_password" {
			return n, false, fmt.Errorf("unsupported Wi-Fi authentication")
		}
	}
	management := fields["key_mgmt"]
	if management != "" && management != "NONE" && management != "WPA-PSK" && management != "WPA-PSK-SHA256" {
		return n, false, fmt.Errorf("unsupported Wi-Fi authentication")
	}
	if fields["psk"] != "" {
		var credential string
		credential, err = wpaString(fields["psk"], false)
		if err != nil {
			return n, false, err
		}
		if strings.HasPrefix(fields["psk"], `"`) || strings.HasPrefix(fields["psk"], `P"`) {
			n.Password = credential
		} else {
			n.PSK = credential
		}
	}
	secure := n.Password != "" || n.PSK != ""
	if (management != "NONE" && !secure) || (management == "NONE" && secure) {
		return n, false, fmt.Errorf("missing or inconsistent Wi-Fi credential")
	}
	for key, dest := range map[string]*bool{"disabled": &n.AutoConnect, "scan_ssid": &n.Hidden} {
		if value, exists := fields[key]; exists {
			if value != "0" && value != "1" {
				return n, false, fmt.Errorf("unsupported Wi-Fi setting")
			}
			*dest = value == "1"
			if key == "disabled" {
				*dest = !*dest
			}
		}
	}
	if value, exists := fields["priority"]; exists {
		n.Priority, err = strconv.Atoi(value)
		if err != nil {
			return n, false, fmt.Errorf("invalid Wi-Fi priority")
		}
	}
	n.BSSID = fields["bssid"]
	return n, true, validateWiFi([]WiFiNetwork{n})
}
