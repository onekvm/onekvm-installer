package installer

import (
	"net/netip"
	"reflect"
	"strings"
	"testing"
)

func TestStaticSnapshotUsesCurrentAddressAndRoute(t *testing.T) {
	root := t.TempDir()
	put(t, root, "proc/net/route", "Iface Destination Gateway Flags RefCnt Use Metric Mask MTU Window IRTT\neth1 00000000 0100000A 0003 0 0 0 00000000 0 0 0\neth0 00000000 0263640A 0003 0 0 100 00000000 0 0 0\neth0 00000000 0363640A 0003 0 0 100 00000000 0 0 0\neth0 00000000 0163640A 0003 0 0 0 00000000 0 0 0\n")
	put(t, root, "etc/resolv.conf", "nameserver 127.0.0.53\n")
	put(t, root, "run/systemd/resolve/resolv.conf", "nameserver 10.100.99.1\nnameserver 8.8.8.8\nnameserver 8.8.8.8\n")
	current := Network{Interface: "eth0", Mode: "dhcp", Hostname: "kvm-e73e"}
	got, err := staticNetwork(root, current, []netip.Prefix{netip.MustParsePrefix("fe80::1/64"), netip.MustParsePrefix("10.100.99.172/24")})
	want := Network{Interface: "eth0", Mode: "static", Address: "10.100.99.172/24", Gateway: "10.100.99.1", DNS: []string{"10.100.99.1", "8.8.8.8"}, Hostname: "kvm-e73e"}
	if err != nil || !reflect.DeepEqual(got, want) {
		t.Fatal(got, err)
	}
	if current.Mode != "dhcp" || current.Address != "" {
		t.Fatal("changed imported DHCP configuration")
	}
}

func TestStaticSnapshotRejectsUnknownOrAmbiguousSettings(t *testing.T) {
	root := t.TempDir()
	put(t, root, "proc/net/route", "eth0 00000000 0100A8C0 0003 0 0 0 00000000\neth0 00000000 0200A8C0 0003 0 0 0 00000000\n")
	n := Network{Interface: "eth0", Mode: "dhcp"}
	for _, prefixes := range [][]netip.Prefix{nil, {netip.MustParsePrefix("192.168.0.5/24"), netip.MustParsePrefix("192.168.0.6/24")}, {netip.MustParsePrefix("192.168.0.5/24")}} {
		if _, err := staticNetwork(root, n, prefixes); err == nil {
			t.Fatal("accepted missing/ambiguous runtime configuration")
		}
	}
}

func TestStaticSnapshotWithoutGatewayKeepsDNS(t *testing.T) {
	root := t.TempDir()
	put(t, root, "proc/net/route", "Iface Destination Gateway Flags RefCnt Use Metric Mask\n")
	n := Network{Interface: "eth0", Mode: "dhcp", DNS: []string{"1.1.1.1"}}
	got, err := staticNetwork(root, n, []netip.Prefix{netip.MustParsePrefix("10.1.2.3/24")})
	if err != nil || got.Gateway != "" || strings.Join(got.DNS, ",") != "1.1.1.1" {
		t.Fatal(got, err)
	}
}
