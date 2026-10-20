package installer

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"unicode"
)

type MachineIdentity struct {
	Machine string `json:"machine"`
	Vendor  string `json:"vendor"`
	Name    string `json:"name"`
	Variant string `json:"variant"`
}

// Match OneKVM's MachineProfile::model_label and the UI's machineDisplayName.
// Identity is presentation only: image compatibility still comes from Probe.
func (m MachineIdentity) ModelLabel() string {
	name, vendor := strings.TrimSpace(m.Name), strings.TrimSpace(m.Vendor)
	label := name
	if label == "" {
		label = strings.TrimSpace(m.Machine)
	}
	if vendor != "" && name != "" && !strings.HasPrefix(strings.ToLower(name), strings.ToLower(vendor)) {
		label = vendor + " " + name
	}
	variant := strings.TrimSpace(m.Variant)
	switch strings.ToLower(variant) {
	case "pcie", "atx":
		variant = "PCIe"
	case "cube":
		variant = "Cube"
	case "desk":
		variant = "Desk"
	}
	if variant != "" {
		label = strings.TrimSpace(label + " " + variant)
	}
	var result []rune
	for _, r := range label {
		if !unicode.IsControl(r) {
			result = append(result, r)
			if len(result) == 128 {
				break
			}
		}
	}
	return string(result)
}

// ReadMachineIdentity only reads display metadata for the expected family.
func ReadMachineIdentity(root string, defaults MachineIdentity) MachineIdentity {
	var installed MachineIdentity
	if data, err := os.ReadFile(filepath.Join(root, "usr/share/onekvm/machine.json")); err == nil && json.Unmarshal(data, &installed) == nil && installed.Machine == defaults.Machine {
		return installed
	}
	return defaults
}
