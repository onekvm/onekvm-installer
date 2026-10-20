package installer

import (
	"fmt"
	"io"
	"runtime"
	"slices"
	"time"
)

// Machine owns the device-specific installation path. Downloads, account and
// network configuration, staging, locking and the TUI stay in the shared flow;
// ImportNetwork handles a machine's additional persistent network formats.
// A metadata profile alone never enables a machine's installation support.
type Machine interface {
	Profile() string
	Family() string
	Direct() bool
	Architectures() []string
	ImageRepository() string
	InstallationTime() time.Duration
	Matches(root string) bool
	Probe(root string) (Device, error)
	ImportNetwork(root, iface string) (Network, bool, error)
	ValidateTarget(Device) error
	MatchesImage(name string) bool
	ExtractBootstrap(image, destination string, progress Progress) error
	BootstrapFiles() []string
	PrepareBootstrap(string, Request) error
	ActivateRecovery(Device, Request, Reporter) error
	CheckDisk(Device) error
	WritePayload(io.ReadWriteSeeker, io.Reader, Image, Progress) error
	Recover(Reporter) (bool, error)
}

var registeredMachines []Machine

// A machine registers from its architecture-constrained source file. Builds
// for other architectures never include that implementation.
func RegisterMachine(machine Machine) {
	for _, registered := range registeredMachines {
		if registered.Profile() == machine.Profile() {
			panic("duplicate machine profile: " + machine.Profile())
		}
	}
	registeredMachines = append(registeredMachines, machine)
	DefaultCatalog[machine.Profile()] = machine.ImageRepository()
}

func Machines() []Machine {
	return append([]Machine(nil), registeredMachines...)
}

func MachineFor(profile string) (Machine, error) {
	for _, machine := range Machines() {
		if machine.Profile() == profile {
			return machine, nil
		}
	}
	return nil, fmt.Errorf("automatic bootstrap unavailable for %s: no supported machine implementation in this build", profile)
}

func CheckRuntime(d Device) error {
	machine, err := MachineFor(d.Profile)
	if err != nil {
		return err
	}
	if !slices.Contains(machine.Architectures(), runtime.GOARCH) {
		return fmt.Errorf("%s installation does not support Linux/%s", d.Profile, runtime.GOARCH)
	}
	return nil
}
