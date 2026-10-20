package installer

import (
	"fmt"
	"io"
	"os"
)

// WritePayload dispatches only to a compiled, supported machine implementation.
func WritePayload(target io.ReadWriteSeeker, source io.Reader, info Image, progress Progress) error {
	machine, err := MachineFor(info.Profile)
	if err != nil {
		return err
	}
	return machine.WritePayload(target, source, info, progress)
}

func Recover(report Reporter) (bool, error) {
	if os.Getenv("ONEKVM_INITRAMFS") != "1" || os.Geteuid() != 0 {
		return false, fmt.Errorf("recover is only available in the installation initramfs")
	}
	for _, machine := range Machines() {
		// Recovery has its own early-boot detection: the factory identity
		// files used by Probe may not exist in the initramfs.
		installed, err := machine.Recover(report)
		if installed || err != nil {
			return installed, err
		}
	}
	return false, nil
}
