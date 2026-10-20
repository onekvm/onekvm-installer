package nanokvm

import (
	"errors"
	"fmt"
	"net/netip"
	"os"
	"path/filepath"
	"strings"

	"github.com/onekvm/onekvm-installer/internal/installer"
)

// Stock S30eth reads eth.nodhcp rather than /etc/network/interfaces. Its
// fallback DHCP process is detected in the shared importer before this hook.
func (Machine) ImportNetwork(root, iface string) (installer.Network, bool, error) {
	n := installer.Network{Interface: iface, Hostname: installer.CurrentHostname(root)}
	if iface != "eth0" {
		return n, false, nil
	}
	content, err := os.ReadFile(filepath.Join(root, "boot/eth.nodhcp"))
	if errors.Is(err, os.ErrNotExist) {
		return n, false, nil
	}
	if err != nil {
		return n, true, err
	}
	for _, line := range strings.Split(string(content), "\n") {
		fields := strings.Fields(line)
		if len(fields) == 0 || strings.HasPrefix(fields[0], "#") {
			continue
		}
		if n.Address != "" {
			return n, true, fmt.Errorf("multiple stock static addresses require manual network setup")
		}
		if len(fields) > 2 {
			return n, true, fmt.Errorf("invalid stock static network configuration")
		}
		prefix, err := netip.ParsePrefix(fields[0])
		if err != nil || !prefix.Addr().Is4() {
			return n, true, fmt.Errorf("static IPv4 address must include a prefix, e.g. 192.168.1.50/24")
		}
		n.Mode, n.Address = "static", prefix.String()
		if len(fields) == 2 {
			n.Gateway = fields[1]
		} else {
			// Match S30eth's calc_gw: subnet network address OR 1.
			gateway := prefix.Masked().Addr().As4()
			gateway[3] |= 1
			n.Gateway = netip.AddrFrom4(gateway).String()
		}
	}
	if n.Address == "" {
		return n, true, fmt.Errorf("stock static network configuration has no IPv4 address")
	}
	n.DNS = installer.CurrentDNS(root)
	return n, true, n.Validate()
}
