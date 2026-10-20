package ui

import (
	"fmt"
	"net/netip"
	"os/exec"
	"strings"
	"syscall"
	"time"

	"github.com/onekvm/onekvm-installer/internal/installer"
)

func (m *model) finalMessage() string {
	message := m.opts.Text.T(m.message)
	if m.message == "done.reboot" && !installer.DirectInstall(m.opts.Device) {
		if machine, err := installer.MachineFor(m.opts.Device.Profile); err == nil && machine.InstallationTime() > 0 {
			minutes := int(machine.InstallationTime() / time.Minute)
			message += "\n" + m.opts.Text.T("done.reboot.estimate", minutes)
		}
	}
	if m.message == "done.reboot" && installer.DirectInstall(m.opts.Device) {
		message = m.opts.Text.T("done.direct.reboot")
	}
	if m.message == "done.reboot" || m.message == "done.armed" || m.message == "done.direct" {
		if access := m.accessMessage(); access != "" {
			message += "\n\n" + access
		}
	}
	return message
}

func (m *model) accessMessage() string {
	if m.access.Username == "" {
		return ""
	}
	t := m.opts.Text.T
	n := m.access.Network
	lines := []string{t("done.access.title")}
	if n.Mode == "static" {
		if prefix, err := netip.ParsePrefix(n.Address); err == nil {
			lines = append(lines, t("done.access.url", "https://"+prefix.Addr().String()+"/"))
		}
	} else if n.Mode == "dhcp" {
		lines = append(lines, t("done.access.dhcp"))
	}
	if n.Hostname != "" {
		lines = append(lines, t("done.access.hostname", clean(n.Hostname)))
	}
	lines = append(lines, t("done.access.username", clean(m.access.Username)))
	return strings.Join(lines, "\n")
}

func rebootCommand() (*exec.Cmd, error) {
	// Factory systems may only have BusyBox. A new session keeps the reboot
	// independent of the terminal and installer; no inherited terminal streams.
	sleep, err := exec.LookPath("sleep")
	if err != nil {
		return nil, err
	}
	reboot, err := exec.LookPath("reboot")
	if err != nil {
		return nil, err
	}
	cmd := exec.Command("/bin/sh", "-c", `"$1" 1 && exec "$2"`, "onekvm-reboot", sleep, reboot)
	cmd.SysProcAttr = &syscall.SysProcAttr{Setsid: true}
	return cmd, nil
}

func delayedReboot() error {
	cmd, err := rebootCommand()
	if err != nil {
		return fmt.Errorf("schedule reboot: %w", err)
	}
	syscall.Sync()
	if err := cmd.Start(); err != nil {
		return fmt.Errorf("schedule reboot: %w", err)
	}
	// Reap the child if still running; it survives the installer's normal exit.
	go func() { _ = cmd.Wait() }()
	return nil
}
