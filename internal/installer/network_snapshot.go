package installer

import (
	"bufio"
	"fmt"
	"net"
	"net/netip"
	"os"
	"path/filepath"
	"strconv"
	"strings"
)

// StaticNetwork snapshots the address in use only after the user explicitly
// chooses to keep the current DHCP address as a static configuration.
func StaticNetwork(root string, current Network) (Network, error) {
	if !interfacePattern.MatchString(current.Interface) {
		return current, fmt.Errorf("unsupported interface")
	}
	link, err := net.InterfaceByName(current.Interface)
	if err != nil {
		return current, err
	}
	addresses, err := link.Addrs()
	if err != nil {
		return current, err
	}
	var prefixes []netip.Prefix
	for _, address := range addresses {
		if prefix, err := netip.ParsePrefix(address.String()); err == nil {
			prefixes = append(prefixes, prefix)
		}
	}
	return staticNetwork(root, current, prefixes)
}

func staticNetwork(root string, current Network, prefixes []netip.Prefix) (Network, error) {
	n := current
	n.Mode, n.Address, n.Gateway = "static", "", ""
	for _, prefix := range prefixes {
		if !prefix.Addr().Is4() || !prefix.Addr().IsGlobalUnicast() || prefix.Addr().IsLinkLocalUnicast() {
			continue
		}
		if n.Address != "" && n.Address != prefix.String() {
			return n, fmt.Errorf("multiple current IPv4 addresses; configure a static address manually")
		}
		n.Address = prefix.String()
	}
	if n.Address == "" {
		return n, fmt.Errorf("no current IPv4 address; configure a static address manually")
	}
	routes, err := os.Open(filepath.Join(root, "proc/net/route"))
	if err != nil {
		return n, err
	}
	defer routes.Close()
	best := uint64(^uint32(0)) + 1
	ambiguous := false
	scan := bufio.NewScanner(routes)
	for scan.Scan() {
		fields := strings.Fields(scan.Text())
		if len(fields) < 8 || fields[0] != n.Interface || fields[1] != "00000000" || fields[7] != "00000000" {
			continue
		}
		flags, flagsErr := strconv.ParseUint(fields[3], 16, 32)
		gateway, gatewayErr := strconv.ParseUint(fields[2], 16, 32)
		metric, metricErr := strconv.ParseUint(fields[6], 10, 32)
		if flagsErr != nil || gatewayErr != nil || metricErr != nil {
			return n, fmt.Errorf("cannot read the current default gateway")
		}
		if flags&3 != 3 || flags&0x200 != 0 || metric > best {
			continue
		}
		address := netip.AddrFrom4([4]byte{byte(gateway), byte(gateway >> 8), byte(gateway >> 16), byte(gateway >> 24)}).String()
		if metric == best {
			ambiguous = ambiguous || address != n.Gateway
		} else {
			ambiguous = false
		}
		best, n.Gateway = metric, address
	}
	if err := scan.Err(); err != nil {
		return n, err
	}
	if ambiguous {
		return n, fmt.Errorf("multiple current default gateways; configure a static address manually")
	}
	if len(n.DNS) == 0 {
		n.DNS = CurrentDNS(root)
	}
	return n, n.Validate()
}

func CurrentDNS(root string) []string {
	for _, path := range []string{"run/systemd/resolve/resolv.conf", "etc/resolv.conf"} {
		var servers []string
		seen := map[string]bool{}
		for _, line := range strings.Split(ReadTrim(filepath.Join(root, path)), "\n") {
			fields := strings.Fields(line)
			if len(fields) < 2 || fields[0] != "nameserver" {
				continue
			}
			address, err := netip.ParseAddr(fields[1])
			if err == nil && !address.IsLoopback() && !address.IsUnspecified() && !seen[address.String()] {
				servers = append(servers, address.String())
				seen[address.String()] = true
			}
		}
		if len(servers) > 0 {
			return servers
		}
	}
	return nil
}
