package nanokvm

import (
	"encoding/json"
	"os"
	"path/filepath"

	"github.com/onekvm/onekvm-installer/internal/installer"
)

func HardwareModel(root string) string {
	identity := installer.ReadMachineIdentity(root, installer.MachineIdentity{Machine: "nanokvm", Vendor: "Sipeed", Name: "NanoKVM"})
	var runtime struct {
		Variant string `json:"variant"`
	}
	if data, err := os.ReadFile(filepath.Join(root, "run/onekvm/hardware.json")); err == nil && json.Unmarshal(data, &runtime) == nil {
		switch runtime.Variant {
		case "pcie", "cube", "lite":
			identity.Variant = runtime.Variant
			return identity.ModelLabel()
		}
	}
	if identity.Variant == "" {
		identity.Variant = stockNanoKVMVariant(root)
	}
	return identity.ModelLabel()
}
func stockNanoKVMVariant(root string) string {
	// Read the same C906L identity consumed by OneKVM's machine detector.
	// Never run that detector here: it changes pin muxes and resets the OLED.
	for _, path := range []string{"sys/class/misc/onekvm-oled/variant", "run/onekvm/oled-rtos"} {
		switch installer.ReadTrim(filepath.Join(root, path)) {
		case "pcie":
			return "pcie"
		case "cube-gpio", "cube-i2c1":
			return "cube"
		}
	}
	// Stock NanoKVM records the detected OLED in this marker. Alpha/Beta
	// identify revisions, while the 0x3d OLED establishes the Cube variant.
	// PCIe has its own explicit hw value and a 0x3c OLED.
	switch installer.ReadTrim(filepath.Join(root, "etc/kvm/hw")) {
	case "pcie":
		return "pcie"
	case "lite":
		return "lite"
	case "alpha", "beta":
		if info, err := os.Stat(filepath.Join(root, "etc/kvm/oled_exist")); err == nil && info.Mode().IsRegular() {
			return "cube"
		}
	}
	// A missing OLED marker alone is not proof of Lite: stock services may
	// not have finished detecting hardware, or the panel may be unavailable.
	return ""
}
