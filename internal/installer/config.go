package installer

import (
	"bufio"
	"fmt"
	"net/netip"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"unicode"
	"unicode/utf8"

	"golang.org/x/crypto/bcrypt"
)

type Network struct {
	Interface string        `json:"interface"`
	Mode      string        `json:"mode"`
	Address   string        `json:"address,omitempty"`
	Gateway   string        `json:"gateway,omitempty"`
	DNS       []string      `json:"dns,omitempty"`
	Hostname  string        `json:"hostname,omitempty"`
	WiFi      []WiFiNetwork `json:"wifi,omitempty"`
}

var interfacePattern = regexp.MustCompile(`^(eth[0-9]+|en[a-zA-Z0-9]+)$`)
var hostnamePattern = regexp.MustCompile(`^[a-zA-Z0-9]([a-zA-Z0-9-]{0,61}[a-zA-Z0-9])?$`)

func (n Network) Validate() error {
	if !interfacePattern.MatchString(n.Interface) {
		return fmt.Errorf("select a physical Ethernet interface; Wi-Fi profiles are configured separately")
	}
	if n.Mode != "dhcp" && n.Mode != "static" {
		return fmt.Errorf("network mode must be dhcp or static")
	}
	if n.Hostname != "" && !hostnamePattern.MatchString(n.Hostname) {
		return fmt.Errorf("invalid hostname")
	}
	if n.Mode == "static" {
		p, err := netip.ParsePrefix(n.Address)
		if err != nil || !p.Addr().Is4() {
			return fmt.Errorf("static IPv4 address must include a prefix, e.g. 192.168.1.50/24")
		}
		if n.Gateway != "" {
			g, err := netip.ParseAddr(n.Gateway)
			if err != nil || !g.Is4() || !p.Contains(g) {
				return fmt.Errorf("IPv4 gateway must be in the address subnet")
			}
		}
	}
	for _, dns := range n.DNS {
		if _, err := netip.ParseAddr(dns); err != nil {
			return fmt.Errorf("invalid DNS address %q", dns)
		}
	}
	return validateWiFi(n.WiFi)
}

// Quote for OneKVM CLI's shell_words parser, never for shell execution.
func cliQuote(v string) string { return "'" + strings.ReplaceAll(v, "'", `'\''`) + "'" }
func Firstboot(username, password string, n Network, system SystemSettings) ([]byte, error) {
	if username == "" || len(username) > 64 || strings.TrimSpace(username) != username || strings.ContainsFunc(username, unicode.IsControl) {
		return nil, fmt.Errorf("username must contain 1–64 bytes and no control characters or surrounding spaces")
	}
	// Core uses bcrypt: its effective password input is at most 72 bytes.
	if !utf8.ValidString(password) || utf8.RuneCountInString(password) < 8 || strings.ContainsFunc(password, unicode.IsControl) {
		return nil, fmt.Errorf("password must contain at least 8 characters and no control characters")
	}
	if len(password) > 72 {
		return nil, fmt.Errorf("password is too long; use a shorter password")
	}
	if err := n.Validate(); err != nil {
		return nil, err
	}
	system, err := system.Normalize()
	if err != nil {
		return nil, err
	}
	hash, err := bcrypt.GenerateFromPassword([]byte(password), 10)
	if err != nil {
		return nil, err
	}
	prefix := "set interfaces ethernet " + n.Interface + " "
	lines := []string{"set auth user " + cliQuote(username) + " role admin", "set auth user " + cliQuote(username) + " encrypted-password " + cliQuote(string(hash)), prefix + "mode " + n.Mode}
	lines = append(lines, "set system language "+system.Language, "set system time-zone "+system.Timezone)
	if n.Mode == "static" {
		lines = append(lines, prefix+"address "+n.Address)
		if n.Gateway != "" {
			lines = append(lines, prefix+"gateway "+n.Gateway)
		}
	}
	for _, dns := range n.DNS {
		lines = append(lines, "set system name-server "+dns)
	}
	if n.Hostname != "" {
		lines = append(lines, "set network hostname "+n.Hostname)
	}
	for _, wifi := range n.WiFi {
		prefix := "set wifi " + wifi.Interface + " network " + cliQuote(wifi.SSID) + " "
		credential := "password " + cliQuote(wifi.Password)
		if wifi.PSK != "" {
			credential = "psk " + cliQuote(wifi.PSK)
		}
		lines = append(lines, prefix+credential,
			prefix+fmt.Sprintf("auto-connect %t", wifi.AutoConnect),
			prefix+fmt.Sprintf("hidden %t", wifi.Hidden),
			prefix+fmt.Sprintf("priority %d", wifi.Priority))
		if wifi.BSSID != "" {
			lines = append(lines, prefix+"bssid "+wifi.BSSID)
		}
	}
	lines = append(lines, "request system setup complete")
	text := []byte(strings.Join(lines, "\n") + "\n")
	if len(text) > 65536 {
		clear(text)
		return nil, fmt.Errorf("firstboot configuration exceeds 64 KiB; copy fewer Wi-Fi profiles")
	}
	return text, nil
}

// Copy only recognized persistent settings. A DHCP lease is never converted
// to a static address. Unknown managers require explicit user configuration.
func CurrentHostname(root string) string {
	for _, path := range []string{"proc/sys/kernel/hostname", "etc/hostname"} {
		if name := ReadTrim(filepath.Join(root, path)); hostnamePattern.MatchString(name) {
			return name
		}
	}
	return ""
}

func CopyNetwork(root, iface string) (Network, error) {
	n, err := CopyEthernetNetwork(root, iface)
	if err != nil {
		return n, err
	}
	wifi, err := CopyWiFi(root)
	if err != nil {
		return n, err
	}
	n.WiFi = wifi.Networks
	return n, n.Validate()
}

func CopyEthernetNetwork(root, iface string) (Network, error) {
	n := Network{Interface: iface, Hostname: CurrentHostname(root)}
	if !interfacePattern.MatchString(iface) {
		return n, fmt.Errorf("unsupported interface")
	}
	idx := ReadTrim(filepath.Join(root, "sys/class/net", iface, "ifindex"))
	link := ReadTrim(filepath.Join(root, "run/systemd/netif/links", idx))
	for _, line := range strings.Split(link, "\n") {
		if strings.HasPrefix(line, "NETWORK_FILE_DROPINS=") && strings.Trim(strings.TrimPrefix(line, "NETWORK_FILE_DROPINS="), `"`) != "" {
			return n, fmt.Errorf("networkd drop-ins require manual network setup")
		}
	}
	for _, line := range strings.Split(link, "\n") {
		if strings.HasPrefix(line, "NETWORK_FILE=") {
			file := strings.Trim(strings.TrimPrefix(line, "NETWORK_FILE="), `"`)
			b, err := os.ReadFile(filepath.Join(root, file))
			if err != nil {
				return n, err
			}
			section := ""
			for _, line := range strings.Split(string(b), "\n") {
				line = strings.TrimSpace(line)
				if strings.HasPrefix(line, "[") {
					section = line
					continue
				}
				if section == "[Route]" || section == "[Address]" {
					return n, fmt.Errorf("separate networkd route/address sections require manual setup")
				}
				if section != "[Network]" {
					continue
				}
				key, value, ok := strings.Cut(line, "=")
				if !ok {
					continue
				}
				value = strings.TrimSpace(value)
				switch strings.TrimSpace(key) {
				case "DHCP":
					if value == "yes" || value == "ipv4" {
						n.Mode = "dhcp"
					}
				case "Address":
					if n.Address != "" {
						return n, fmt.Errorf("multiple addresses require manual network setup")
					}
					n.Address = value
				case "Gateway":
					n.Gateway = value
				case "DNS":
					n.DNS = append(n.DNS, strings.Fields(value)...)
				}
			}
			if n.Mode == "dhcp" && n.Address != "" {
				return n, fmt.Errorf("DHCP plus static addressing requires manual setup")
			}
			if n.Mode == "" && n.Address != "" {
				n.Mode = "static"
			}
			return n, n.Validate()
		}
	}
	file, err := os.Open(filepath.Join(root, "etc/network/interfaces"))
	if err == nil {
		defer file.Close()
		active := false
		scan := bufio.NewScanner(file)
		for scan.Scan() {
			v := strings.Fields(scan.Text())
			if len(v) == 0 || strings.HasPrefix(v[0], "#") {
				continue
			}
			if v[0] == "iface" {
				active = len(v) >= 4 && v[1] == iface && v[2] == "inet"
				if active {
					n.Mode = v[3]
				}
				continue
			}
			if !active || len(v) < 2 {
				continue
			}
			switch v[0] {
			case "address":
				n.Address = v[1]
			case "gateway":
				n.Gateway = v[1]
			case "dns-nameservers":
				n.DNS = v[1:]
			}
		}
		if n.Mode != "" {
			return n, n.Validate()
		}
	}
	// Stock NanoKVM runs udhcpc outside /etc/network/interfaces.
	processes, _ := filepath.Glob(filepath.Join(root, "proc/[0-9]*/cmdline"))
	for _, p := range processes {
		b, _ := os.ReadFile(p)
		v := strings.Split(string(b), "\x00")
		if len(v) < 1 || filepath.Base(v[0]) != "udhcpc" {
			continue
		}
		for i := 1; i+1 < len(v); i++ {
			if v[i] == "-i" && v[i+1] == iface {
				n.Mode = "dhcp"
				return n, n.Validate()
			}
		}
	}
	for _, machine := range Machines() {
		if machine.Matches(root) {
			if imported, handled, err := machine.ImportNetwork(root, iface); handled || err != nil {
				return imported, err
			}
		}
	}
	return n, fmt.Errorf("cannot safely import %s configuration; choose DHCP or enter a static address", iface)
}
