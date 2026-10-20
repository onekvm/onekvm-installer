//go:build riscv64 || onekvm_test

package machines_test

import (
	"reflect"
	"testing"

	. "github.com/onekvm/onekvm-installer/internal/installer"
	_ "github.com/onekvm/onekvm-installer/internal/machines"
)

func TestNetworkCopyStockStatic(t *testing.T) {
	for _, tt := range []struct {
		name, content, address, gateway string
	}{
		{"explicit gateway", "10.100.99.172/24 10.100.99.1\n", "10.100.99.172/24", "10.100.99.1"},
		{"default gateway", "192.168.91.20/22\n", "192.168.91.20/22", "192.168.88.1"},
		{"CRLF and whitespace", "\r\n# Network\r\n  10.100.99.172/24\t10.100.99.1\r\n", "10.100.99.172/24", "10.100.99.1"},
	} {
		t.Run(tt.name, func(t *testing.T) {
			root := vendorFixture(t)
			put(t, root, "etc/network/interfaces", "auto lo\niface lo inet loopback\n")
			put(t, root, "etc/hostname", "kvm-e73e\n")
			put(t, root, "boot/eth.nodhcp", tt.content)
			put(t, root, "etc/resolv.conf", "nameserver 127.0.0.53\nnameserver 192.168.0.1\nnameserver 8.8.8.8\nnameserver 8.8.8.8\n")
			got, err := CopyNetwork(root, "eth0")
			want := Network{Interface: "eth0", Mode: "static", Address: tt.address, Gateway: tt.gateway,
				DNS: []string{"192.168.0.1", "8.8.8.8"}, Hostname: "kvm-e73e"}
			if err != nil || !reflect.DeepEqual(got, want) {
				t.Fatalf("stock network not imported: %+v, %v", got, err)
			}
		})
	}
}

func TestNetworkCopyStockFallbackPreservesDHCP(t *testing.T) {
	root := vendorFixture(t)
	put(t, root, "boot/eth.nodhcp", "10.100.99.172/24 10.100.99.1\n")
	put(t, root, "proc/467/cmdline", "udhcpc\x00-i\x00eth0\x00-b\x00")
	got, err := CopyNetwork(root, "eth0")
	if err != nil || got.Mode != "dhcp" || got.Address != "" || got.Gateway != "" {
		t.Fatal("stock fallback DHCP was converted to static", got, err)
	}
}

func TestNetworkCopyStockRejectsAmbiguousOrInvalidSettings(t *testing.T) {
	for _, content := range []string{"", "\n# Only comments\n", "10.100.99.172\n", "10.100.99.172/24 192.168.0.1\n",
		"10.100.99.172/24 10.100.99.1 extra\n", "10.100.99.172/24\n10.100.99.173/24\n"} {
		root := vendorFixture(t)
		put(t, root, "boot/eth.nodhcp", content)
		if got, err := CopyNetwork(root, "eth0"); err == nil {
			t.Fatalf("imported ambiguous/invalid stock settings %q: %+v", content, got)
		}
	}
	root := vendorFixture(t)
	put(t, root, "boot/eth.nodhcp", "10.100.99.172/24\n")
	put(t, root, "etc/kvm/hw", "unknown")
	if _, err := CopyNetwork(root, "eth0"); err == nil {
		t.Fatal("applied NanoKVM configuration to an unknown board")
	}
	if _, err := CopyNetwork(root, "eth1"); err == nil {
		t.Fatal("applied eth0 settings to a different interface")
	}
}
