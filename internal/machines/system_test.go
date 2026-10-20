package machines_test

import (
	"errors"
	"testing"

	. "github.com/onekvm/onekvm-installer/internal/installer"
	_ "github.com/onekvm/onekvm-installer/internal/machines"
)

func TestInstallTargetSystemIdentity(t *testing.T) {
	for _, tt := range []struct {
		name      string
		files     map[string]string
		installed bool
	}{
		{"OneKVM", map[string]string{"etc/os-release": "ID=onekvm\nNAME=\"OneKVM OS\"\n"}, true},
		{"quoted ID", map[string]string{"etc/os-release": "ID=\"onekvm\"\n"}, true},
		{"single quoted ID", map[string]string{"etc/os-release": "ID='onekvm'\n"}, true},
		{"usr fallback", map[string]string{"usr/lib/os-release": "ID=onekvm\n"}, true},
		{"etc precedence", map[string]string{"etc/os-release": "ID=buildroot\n", "usr/lib/os-release": "ID=onekvm\n"}, false},
		{"stock", map[string]string{"etc/os-release": "ID=buildroot\n", "etc/kvm/hw": "beta", "etc/kvm/oled_exist": ""}, false},
		{"related names", map[string]string{"etc/os-release": "ID=buildroot\nID_LIKE=onekvm\nNAME=\"OneKVM compatible\"\n# ID=onekvm\n"}, false},
		{"legacy identity", map[string]string{"etc/os-release": "ID=buildroot\n", "usr/share/onekvm/machine": "nanokvm\n"}, true},
		{"JSON identity", map[string]string{"usr/share/onekvm/machine.json": `{"machine":"nanokvm-pro","name":"NanoKVM Pro"}`}, true},
		{"missing identity", nil, false},
	} {
		t.Run(tt.name, func(t *testing.T) {
			root := t.TempDir()
			for path, data := range tt.files {
				put(t, root, path, data)
			}
			// There is no boot mount or userdata: the installed-system check
			// must work before probing storage or creating any installation files.
			err := CheckInstallTarget(root)
			if tt.installed {
				if !errors.Is(err, ErrAlreadyInstalled) {
					t.Fatalf("expected installed-system refusal, got %v", err)
				}
			} else if err != nil {
				t.Fatal(err)
			}
		})
	}
}

func TestInstalledSystemCanStillBeInspected(t *testing.T) {
	root := vendorFixture(t)
	put(t, root, "etc/os-release", "ID=onekvm\n")
	if !errors.Is(CheckInstallTarget(root), ErrAlreadyInstalled) {
		t.Fatal("installation allowed on OneKVM")
	}
	if _, err := Probe(root); err != nil {
		t.Fatal("read-only inspection was blocked", err)
	}
}
