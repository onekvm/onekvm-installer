package installer

import (
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strings"
)

var ErrAlreadyInstalled = errors.New("OneKVM is already installed; use the system updater")

// CheckInstallTarget only guards new installations. Read-only commands,
// cancellation and initramfs recovery must remain available.
func CheckInstallTarget(root string) error {
	data, err := os.ReadFile(filepath.Join(root, "etc/os-release"))
	if os.IsNotExist(err) {
		data, err = os.ReadFile(filepath.Join(root, "usr/lib/os-release"))
	}
	if err != nil && !os.IsNotExist(err) {
		return err
	}
	for _, line := range strings.Split(string(data), "\n") {
		key, value, ok := strings.Cut(strings.TrimSpace(line), "=")
		if !ok || key != "ID" {
			continue
		}
		value = strings.TrimSpace(value)
		if len(value) >= 2 && (value[0] == '"' || value[0] == '\'') && value[len(value)-1] == value[0] {
			value = value[1 : len(value)-1]
		}
		if value == "onekvm" {
			return ErrAlreadyInstalled
		}
	}
	// Older installations can retain a vendor os-release. These identity
	// files are supplied by OneKVM, independently of the vendor board markers.
	if ReadTrim(filepath.Join(root, "usr/share/onekvm/machine")) != "" {
		return ErrAlreadyInstalled
	}
	var identity MachineIdentity
	if data, err := os.ReadFile(filepath.Join(root, "usr/share/onekvm/machine.json")); err == nil &&
		json.Unmarshal(data, &identity) == nil && identity.Machine != "" {
		return ErrAlreadyInstalled
	}
	return nil
}
