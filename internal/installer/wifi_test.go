package installer

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestCopyWiFiPreservesSavedProfilesSeparatelyFromEthernet(t *testing.T) {
	root := t.TempDir()
	if err := os.MkdirAll(filepath.Join(root, "sys/class/net/wlan0/wireless"), 0700); err != nil {
		t.Fatal(err)
	}
	put(t, root, "etc/network/interfaces", "iface eth0 inet dhcp\n")
	config := `ctrl_interface=/run/wpa_supplicant
network={
 ssid="Office # 中文"
 psk="pass'word\literal#"
 scan_ssid=1
 priority=100
}
network={
 ssid=4775657374
 key_mgmt=NONE
 disabled=1
}
network={
 ssid="Derived"
 psk=` + strings.Repeat("a1", 32) + `
}
`
	put(t, root, "etc/wpa_supplicant/wpa_supplicant.conf", config)
	n, err := CopyNetwork(root, "eth0")
	if err != nil || n.Interface != "eth0" || n.Mode != "dhcp" || len(n.WiFi) != 3 {
		t.Fatalf("wrong copied network: %v", err)
	}
	if n.WiFi[0].SSID != "Office # 中文" || !n.WiFi[0].Hidden || n.WiFi[0].Priority != 100 ||
		n.WiFi[0].Password != `pass'word\literal#` || n.WiFi[1].AutoConnect || n.WiFi[1].SSID != "Guest" || n.WiFi[1].Password != "" || n.WiFi[2].PSK != strings.Repeat("a1", 32) || n.WiFi[2].Password != "" {
		t.Fatal("Wi-Fi profile settings changed")
	}
	public, _ := json.Marshal(n)
	if strings.Contains(string(public), "literal#") || strings.Contains(string(public), strings.Repeat("a1", 32)) {
		t.Fatal("Wi-Fi credentials leaked in network JSON")
	}
	firstboot, err := Firstboot("operator", "testing-only", n, SystemSettings{})
	if err != nil || !strings.Contains(string(firstboot), "set interfaces ethernet eth0 mode dhcp\n") ||
		!strings.Contains(string(firstboot), `set wifi wlan0 network 'Office # 中文' password 'pass'\''word\literal#'`) ||
		!strings.Contains(string(firstboot), "set wifi wlan0 network 'Guest' auto-connect false\n") ||
		!strings.Contains(string(firstboot), "set wifi wlan0 network 'Derived' psk '"+strings.Repeat("a1", 32)+"'\n") {
		t.Fatal("separate Ethernet and Wi-Fi firstboot statements missing", err)
	}
	access, err := AccessFromFirstboot(firstboot)
	if err != nil || len(access.Network.WiFi) != 0 {
		t.Fatal("access summary must not retain Wi-Fi credentials", err)
	}
	after, _ := os.ReadFile(filepath.Join(root, "etc/wpa_supplicant/wpa_supplicant.conf"))
	if string(after) != config {
		t.Fatal("import changed the current Wi-Fi configuration")
	}
	wired, err := CopyEthernetNetwork(root, "eth0")
	if err != nil || len(wired.WiFi) != 0 {
		t.Fatal("Ethernet-only import included Wi-Fi", err)
	}
}

func TestWiFiImportRejectsUnsupportedOrAmbiguousCredentialsWithoutLeaking(t *testing.T) {
	for _, text := range []string{
		"network={\nssid=\"Office\"\npsk=\"private\"\n}\n",
		"network={\nssid=\"Office\"\npsk=\"private-secret\"\nkey_mgmt=WPA-EAP\n}\n",
		"network={\nssid=\"Office\"\npsk=\"private-secret\"\n",
		"network={\nssid=\"Office\"\npsk=\"private-secret\"\npsk=\"second-private\"\n}\n",
		"network={\nssid=\"Office\"\npsk=" + strings.Repeat("z", 64) + "\n}\n",
	} {
		_, err := parseWPANetworks(text, "wlan0")
		if err == nil || strings.Contains(err.Error(), "private") {
			t.Fatal("unsafe or invalid Wi-Fi configuration accepted or leaked")
		}
	}
	profiles, err := parseWPANetworks("network={\nssid=\"\"\npsk=\"\"\n}\n", "wlan0")
	if err != nil || len(profiles) != 0 {
		t.Fatal("empty factory template treated as a saved connection", err)
	}
	if _, err := parseWPANetworks("network={\nssid=\"Guest\"\nkey_mgmt=NONE\n}\n", "../wlan0"); err == nil {
		t.Fatal("invalid interface accepted")
	}
}

func TestCopyWiFiUsesInterfaceSpecificFileAndSkipsDevicesWithoutRadio(t *testing.T) {
	root := t.TempDir()
	put(t, root, "etc/wpa_supplicant.conf", "invalid-template")
	wifi, err := CopyWiFi(root)
	if err != nil || len(wifi.Interfaces) != 0 {
		t.Fatal("Wi-Fi offered on a device without a radio", err)
	}
	if err := os.MkdirAll(filepath.Join(root, "sys/class/net/wlan0/wireless"), 0700); err != nil {
		t.Fatal(err)
	}
	put(t, root, "etc/wpa_supplicant/wpa_supplicant-wlan0.conf", "network={\nssid=\"Guest\"\nkey_mgmt=NONE\n}\n")
	wifi, err = CopyWiFi(root)
	if err != nil || len(wifi.Networks) != 1 || wifi.Networks[0].SSID != "Guest" {
		t.Fatal("interface-specific Wi-Fi configuration ignored", err)
	}
}
