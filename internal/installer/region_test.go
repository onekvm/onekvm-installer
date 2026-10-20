package installer

import (
	"strings"
	"testing"
)

func TestFirstbootLanguageAndTimezone(t *testing.T) {
	for _, tt := range []struct{ language, timezone, wantLanguage, wantZone string }{
		{"", "", "en", "UTC"}, {"en", "UTC", "en", "UTC"},
		{"zh", "Asia/Shanghai", "zh", "Asia/Shanghai"},
		{"zh-TW", "Asia/Taipei", "zh_tw", "Asia/Taipei"},
	} {
		b, err := Firstboot("operator", "testing-only", Network{Interface: "eth0", Mode: "dhcp"}, SystemSettings{Language: tt.language, Timezone: tt.timezone})
		if err != nil {
			t.Fatal(err)
		}
		if !strings.Contains(string(b), "set system language "+tt.wantLanguage+"\n") || !strings.Contains(string(b), "set system time-zone "+tt.wantZone+"\n") {
			t.Fatal("region settings not serialized")
		}
	}
	for _, zone := range []string{"Local", "../etc/passwd", "/etc/localtime", "No/Such_Zone", "UTC\nrequest system reboot", "UTC;reboot"} {
		if _, err := (SystemSettings{Timezone: zone}).Normalize(); err == nil {
			t.Fatalf("accepted invalid zone %q", zone)
		}
	}
}
