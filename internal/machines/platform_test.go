package machines_test

import (
	"os"
	"os/exec"
	"reflect"
	"strings"
	"testing"
	"time"

	. "github.com/onekvm/onekvm-installer/internal/installer"
	_ "github.com/onekvm/onekvm-installer/internal/machines"
)

func TestMachineBuildConstraints(t *testing.T) {
	goTool, err := exec.LookPath("go")
	if err != nil {
		t.Skip("source selection is checked on the build host")
	}
	for _, arch := range []string{"riscv64", "arm64", "amd64"} {
		cmd := exec.Command(goTool, "list", "-f", `{{join .Imports ","}}`, ".")
		cmd.Env = append(os.Environ(), "GOOS=linux", "GOARCH="+arch, "GOWORK=off", "GOFLAGS=-mod=vendor")
		out, err := cmd.CombinedOutput()
		if err != nil {
			t.Fatalf("%s source selection: %v\n%s", arch, err, out)
		}
		for pkg, wantArch := range map[string]string{"/nanokvm": "riscv64", "/nanokvmpro": "arm64"} {
			imports := strings.Split(strings.TrimSpace(string(out)), ",")
			found := false
			for _, name := range imports {
				if name == "github.com/onekvm/onekvm-installer/internal/machines"+pkg {
					found = true
				}
			}
			if found != (arch == wantArch) {
				t.Fatalf("%s has unexpected machine import %s: %s", arch, pkg, out)
			}
		}
	}
}

func TestNanoKVMMachineCapabilities(t *testing.T) {
	machine, err := MachineFor("nanokvm")
	if err != nil {
		t.Skip("NanoKVM is not compiled for this architecture")
	}
	if !reflect.DeepEqual(machine.Architectures(), []string{"riscv64"}) || machine.InstallationTime() != 5*time.Minute ||
		DefaultCatalog[machine.Profile()] != machine.ImageRepository() {
		t.Fatal("machine architecture, estimate and catalog are inconsistent")
	}
	for _, profile := range []string{"unknown"} {
		if _, err := MachineFor(profile); err == nil {
			t.Fatalf("%s gained automatic installation support", profile)
		}
	}
}
