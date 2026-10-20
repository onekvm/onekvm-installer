package machines_test

import (
	"testing"

	. "github.com/onekvm/onekvm-installer/internal/installer"
	_ "github.com/onekvm/onekvm-installer/internal/machines"
	"github.com/onekvm/onekvm-installer/internal/machines/nanokvm"
)

func TestHardwareModelMatchesOneKVM(t *testing.T) {
	r := vendorFixture(t)
	put(t, r, "usr/share/onekvm/machine.json", `{"machine":"nanokvm","vendor":"Sipeed","name":"NanoKVM","variant":"cube"}`)
	put(t, r, "run/onekvm/hardware.json", `{"variant":"pcie","revision":"pcie"}`)
	d, err := Probe(r)
	if err != nil || d.HardwareModel != "Sipeed NanoKVM PCIe" {
		t.Fatal(d.HardwareModel, err)
	}
	put(t, r, "usr/share/onekvm/machine.json", `{"machine":"nanokvm","vendor":"Sipeed","name":"Sipeed NanoKVM"}`)
	if got := nanokvm.HardwareModel(r); got != "Sipeed NanoKVM PCIe" {
		t.Fatal("duplicated vendor name", got)
	}
	// Display metadata cannot select another device profile.
	put(t, r, "usr/share/onekvm/machine.json", `{"machine":"nanokvm-pro","vendor":"Sipeed","name":"NanoKVM Pro"}`)
	d, err = Probe(r)
	if err != nil || d.Profile != "nanokvm" || d.HardwareModel != "Sipeed NanoKVM PCIe" {
		t.Fatal(d, err)
	}
}

func TestStockHardwareModelFallback(t *testing.T) {
	r := vendorFixture(t)
	d, err := Probe(r)
	if err != nil || d.HardwareModel != "Sipeed NanoKVM" {
		t.Fatal(d, err)
	}
}

func TestStockHardwareVariants(t *testing.T) {
	for _, tt := range []struct {
		hw, oled, shared, want string
	}{
		{"alpha", "yes", "", "Sipeed NanoKVM Cube"},
		{"beta", "yes", "", "Sipeed NanoKVM Cube"},
		{"pcie", "yes", "", "Sipeed NanoKVM PCIe"},
		{"lite", "", "", "Sipeed NanoKVM lite"},
		{"beta", "", "", "Sipeed NanoKVM"},
		{"beta", "yes", "pcie", "Sipeed NanoKVM PCIe"},
		{"beta", "", "cube-gpio", "Sipeed NanoKVM Cube"},
	} {
		t.Run(tt.hw+"/"+tt.oled+"/"+tt.shared, func(t *testing.T) {
			r := vendorFixture(t)
			put(t, r, "etc/kvm/hw", tt.hw)
			if tt.oled != "" {
				put(t, r, "etc/kvm/oled_exist", "")
			}
			if tt.shared != "" {
				put(t, r, "sys/class/misc/onekvm-oled/variant", tt.shared)
			}
			if got := nanokvm.HardwareModel(r); got != tt.want {
				t.Fatalf("got %s, want %s", got, tt.want)
			}
			// Installed OneKVM detection remains authoritative over old stock markers.
			put(t, r, "run/onekvm/hardware.json", `{"variant":"pcie"}`)
			if got := nanokvm.HardwareModel(r); got != "Sipeed NanoKVM PCIe" {
				t.Fatal("stock markers overrode OneKVM runtime detection", got)
			}
		})
	}
}
