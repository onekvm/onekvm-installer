package installer

import (
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"reflect"
	"strings"
	"testing"
)

func TestPreparedAccessUsesStagedSettingsWithoutSecrets(t *testing.T) {
	for _, tt := range []struct {
		name    string
		network Network
	}{
		{"operator", Network{Interface: "eth0", Mode: "dhcp", Hostname: "lab-kvm"}},
		{"运维 ' operator", Network{Interface: "eth0", Mode: "static", Address: "10.100.99.107/24", Gateway: "10.100.99.1", DNS: []string{"1.1.1.1"}, Hostname: "lab-kvm"}},
		{`a'\'|$(test)"`, Network{Interface: "eth1", Mode: "dhcp"}},
	} {
		t.Run(tt.name, func(t *testing.T) {
			const password = "never-display-this-password"
			firstboot, err := Firstboot(tt.name, password, tt.network, SystemSettings{})
			if err != nil {
				t.Fatal(err)
			}
			dir := t.TempDir()
			put(t, dir, "firstboot.txt", string(firstboot))
			hash := sha256.Sum256(firstboot)
			got, err := preparedAccess(dir, hex.EncodeToString(hash[:]))
			if err != nil || got.Username != tt.name || got.Network.Interface != tt.network.Interface || got.Network.Mode != tt.network.Mode || got.Network.Address != tt.network.Address || got.Network.Hostname != tt.network.Hostname {
				t.Fatalf("staged access settings mismatch: %+v, %v", got, err)
			}
			if text := fmt.Sprint(got); strings.Contains(text, password) || strings.Contains(text, "$2a$") || strings.Contains(text, "encrypted-password") {
				t.Fatal("access summary contains secrets")
			}
		})
	}
}

func TestPreparedAccessRejectsChangedOrInvalidConfiguration(t *testing.T) {
	const valid = "set auth user 'operator' role admin\nset auth user 'operator' encrypted-password 'SECRET-HASH'\nset interfaces ethernet eth0 mode dhcp\n"
	for _, tt := range []struct {
		name, content string
		changed       bool
	}{
		{"changed", valid, true},
		{"missing account", "set interfaces ethernet eth0 mode dhcp\n", false},
		{"missing network", "set auth user 'operator' role admin\n", false},
		{"unquoted account", strings.Replace(valid, "'operator' role", "operator role", 1), false},
		{"invalid quoting", strings.Replace(valid, "'operator' role", "'opera'tor' role", 1), false},
		{"terminal control", strings.Replace(valid, "'operator' role", "'opera\x1btor' role", 1), false},
		{"oversized", valid + strings.Repeat("#", 65536), false},
	} {
		t.Run(tt.name, func(t *testing.T) {
			dir := t.TempDir()
			put(t, dir, "firstboot.txt", tt.content)
			hash := sha256.Sum256([]byte(tt.content))
			expected := hex.EncodeToString(hash[:])
			if tt.changed {
				expected = strings.Repeat("0", 64)
			}
			got, err := preparedAccess(dir, expected)
			if err == nil || !reflect.DeepEqual(got, AccessInfo{}) {
				t.Fatal("invalid configuration produced login guidance")
			}
			if strings.Contains(err.Error(), "SECRET-HASH") || strings.Contains(err.Error(), "operator") {
				t.Fatal("configuration contents leaked into the error")
			}
		})
	}
}
