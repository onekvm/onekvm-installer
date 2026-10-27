package machines_test

import (
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
			installed, err := InstalledSystem(root)
			if err != nil || installed != tt.installed {
				t.Fatalf("installed=%v, want %v; error=%v", installed, tt.installed, err)
			}

		})
	}
}

func TestInstalledSystemCanBeReinstalled(t *testing.T) {
	root := vendorFixture(t)
	put(t, root, "etc/os-release", "ID=onekvm\n")
	d, err := Probe(root)
	if err != nil || !d.Reinstall {
		t.Fatalf("installed system was not selected for reinstallation: %+v %v", d, err)
	}
	if err := d.CanArm(); err != nil {
		t.Fatal(err)
	}
}
