// The initramfs entry point shares the writer without linking the TUI.
package main

import (
	"fmt"
	"os"
	"syscall"
	"time"

	"github.com/onekvm/onekvm-installer/internal/installer"
	_ "github.com/onekvm/onekvm-installer/internal/machines"
)

func Run() error {
	unlock, err := installer.Lock(installer.LockPath)
	if err != nil {
		return err
	}
	defer unlock()
	installed, err := installer.Recover(func(label string, operation func(installer.Progress) error) error {
		fmt.Fprintln(os.Stderr, label)
		last := time.Now()
		return operation(func(done, total int64) {
			if time.Since(last) < 5*time.Second {
				return
			}
			last = time.Now()
			if total > 0 {
				fmt.Fprintf(os.Stderr, "%s: %.0f%%\n", label, float64(done)*100/float64(total))
			} else {
				fmt.Fprintf(os.Stderr, "%s: %.1f MiB\n", label, float64(done)/installer.MiB)
			}
		})
	})
	if err != nil {
		return err
	}
	if installed {
		fmt.Fprintln(os.Stderr, "OneKVM image installed and verified. Rebooting for first boot.")
		syscall.Sync()
		return syscall.Reboot(syscall.LINUX_REBOOT_CMD_RESTART)
	}
	return nil
}

func main() {
	if err := Run(); err != nil {
		fmt.Fprintln(os.Stderr, "OneKVM installation failed:", err)
		os.Exit(1)
	}
}
