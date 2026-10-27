package nanokvmpro

import (
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"time"

	"golang.org/x/sys/unix"
)

// Only the RAM installer owns the framebuffer here. The drivers and static
// renderer come from the same verified image as the recovery kernel.
func (Machine) RecoveryDisplay() func(string) {
	const program = "/usr/sbin/onekvm-boot-splash"
	if os.Getenv("ONEKVM_INITRAMFS") != "1" || !proMatches("/") {
		return nil
	}
	if _, err := os.Stat(program); err != nil {
		return nil // Older images may not include early display support.
	}
	for _, name := range []string{"fbtft", "fb_jd9853"} {
		file, err := os.Open("/usr/lib/onekvm-initramfs/" + name + ".ko")
		if err == nil {
			err = unix.FinitModule(int(file.Fd()), "", 0)
			file.Close()
		}
		if err != nil && !errors.Is(err, unix.EEXIST) {
			fmt.Fprintln(os.Stderr, "Recovery display unavailable:", err)
			return nil
		}
	}
	return func(state string) {
		args := recoveryDisplayArgs(state)
		if args == nil {
			return
		}
		ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
		defer cancel()
		if output, err := exec.CommandContext(ctx, program, args...).CombinedOutput(); err != nil {
			fmt.Fprintf(os.Stderr, "Recovery display: %v: %s\n", err, output)
		} else {
			fmt.Fprintln(os.Stderr, "Recovery display:", state)
		}
	}
}

func recoveryDisplayArgs(state string) []string {
	switch state {
	case "preparing":
		return []string{"install", "1"} // Preparing system files.
	case "writing":
		return []string{"install", "5"} // Installing system files.
	case "finishing":
		return []string{"install", "7"} // Finishing up.
	case "error":
		return []string{"error"}
	default:
		return nil
	}
}
