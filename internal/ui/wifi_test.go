package ui

import (
	"strings"
	"testing"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/x/ansi"
	"github.com/onekvm/onekvm-installer/internal/installer"
)

func TestEthernetAndWiFiHaveSeparatePagesAndDoNotOverwriteEachOther(t *testing.T) {
	m := testModel(t)
	m.network = installer.Network{Interface: "eth0", Mode: "static", Address: "10.1.2.3/24", Gateway: "10.1.2.1", Hostname: "test-kvm"}
	wifi := installer.WiFiNetwork{Interface: "wlan0", SSID: "Office 中文", Password: "private-wifi-password", AutoConnect: true}
	m.deps.wifi = func(string) (installer.WiFiSnapshot, error) {
		return installer.WiFiSnapshot{Interfaces: []string{"wlan0"}, Networks: []installer.WiFiNetwork{wifi}}, nil
	}
	m.goPage(networkFormPage)
	m.submitForm()
	if m.page != wifiPage || len(m.network.WiFi) != 0 {
		t.Fatal("Wi-Fi was not presented as an independent choice")
	}
	view := ansi.Strip(m.View())
	if !strings.Contains(view, "Office 中文") || strings.Contains(view, wifi.Password) {
		t.Fatal("Wi-Fi choice omitted its SSID or exposed its password")
	}
	key(m, tea.KeyEnter) // Copy saved Wi-Fi.
	if m.page != reviewPage || len(m.network.WiFi) != 1 || m.network.Address != "10.1.2.3/24" {
		t.Fatal("copying Wi-Fi changed Ethernet settings")
	}
	view = ansi.Strip(m.View())
	if !strings.Contains(view, "Ethernet:") || !strings.Contains(view, "Wi-Fi:") || strings.Contains(view, wifi.Password) {
		t.Fatal("review did not separate wired and wireless settings")
	}
	m.back()
	key(m, tea.KeyDown)
	key(m, tea.KeyDown)
	key(m, tea.KeyEnter) // Skip Wi-Fi while retaining Ethernet.
	if m.page != reviewPage || len(m.network.WiFi) != 0 || m.network.Mode != "static" || m.network.Address != "10.1.2.3/24" {
		t.Fatal("skipping Wi-Fi overwrote Ethernet settings")
	}
}

func TestManualWiFiMasksPasswordAndRetainsEthernet(t *testing.T) {
	m := testModel(t)
	m.network = installer.Network{Interface: "eth0", Mode: "dhcp"}
	m.wifi.Interfaces = []string{"wlan0"}
	m.goPage(wifiPage)
	key(m, tea.KeyEnter) // Configure another network.
	if m.page != wifiFormPage {
		t.Fatal("Wi-Fi form not offered")
	}
	m.fields[0].input.SetValue("New Wi-Fi")
	m.fields[1].input.SetValue("private-new-password")
	if strings.Contains(m.View(), "private-new-password") {
		t.Fatal("Wi-Fi form exposed password")
	}
	m.submitForm()
	if m.page != reviewPage || m.network.Mode != "dhcp" || len(m.network.WiFi) != 1 || m.network.WiFi[0].SSID != "New Wi-Fi" {
		t.Fatal("manual Wi-Fi configuration was not kept separately")
	}
}
