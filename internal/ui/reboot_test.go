package ui

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/charmbracelet/lipgloss"
	"github.com/onekvm/onekvm-installer/internal/i18n"
	"github.com/onekvm/onekvm-installer/internal/installer"
	"golang.org/x/sys/unix"
)

func TestPostInstallAccessGuidance(t *testing.T) {
	for _, language := range []string{"zh", "zh-TW", "en"} {
		for _, mode := range []string{"static", "dhcp"} {
			for _, message := range []string{"done.armed", "done.reboot"} {
				t.Run(language+"/"+mode+"/"+message, func(t *testing.T) {
					tr, _ := i18n.New(language, os.Getenv)
					m := newModel(context.Background(), Options{Text: tr}, defaults())
					m.message, m.username, m.password = message, "old-username", "never-display-password"
					m.access = installer.AccessInfo{Username: "lab-admin", Network: installer.Network{
						Interface: "eth0", Mode: mode, Address: "10.100.99.107/24", Hostname: "lab-kvm"}}
					text := m.finalMessage()
					for _, want := range []string{tr.T(message), tr.T("done.access.title"), tr.T("done.access.username", "lab-admin"), tr.T("done.access.hostname", "lab-kvm")} {
						if !strings.Contains(text, want) {
							t.Fatalf("missing %q in %q", want, text)
						}
					}
					if strings.Contains(text, m.password) || strings.Contains(text, "old-username") || strings.Contains(text, "$2a$") || strings.Contains(text, "/24") {
						t.Fatal("guidance contains secrets, an old account, or a CIDR URL", text)
					}
					if mode == "static" {
						if !strings.Contains(text, "https://10.100.99.107/") || strings.Contains(text, "DHCP") {
							t.Fatal("static guidance does not show the configured URL", text)
						}
					} else if !strings.Contains(text, tr.T("done.access.dhcp")) || strings.Contains(text, "10.100.99.107") {
						t.Fatal("DHCP guidance promised the old IP", text)
					}
					m.width, m.height = 80, 24
					m.goPage(rebootPage)
					if h := lipgloss.Height(m.View()); h > 24 {
						t.Fatalf("reboot page needs %d rows", h)
					}
					for _, other := range []string{"done.prepared", "error.cancelled", "error.arming"} {
						m.message = other
						if strings.Contains(m.finalMessage(), "lab-admin") {
							t.Fatal("unarmed installation printed login guidance")
						}
					}
				})
			}
		}
	}
}

func TestReinstallAccessGuidanceUsesPreservedWebAddress(t *testing.T) {
	for _, test := range []struct {
		scheme string
		port   int
		want   string
	}{
		{"http", 80, "http://10.100.99.107/"},
		{"http", 8080, "http://10.100.99.107:8080/"},
		{"https", 8443, "https://10.100.99.107:8443/"},
	} {
		m := testModel(t)
		m.access = installer.AccessInfo{Username: "admin", Network: installer.Network{Mode: "static", Address: "10.100.99.107/24"}, WebScheme: test.scheme, WebPort: test.port}
		if text := m.accessMessage(); !strings.Contains(text, test.want) {
			t.Fatal("incorrect preserved Web address", text)
		}
	}
}

func TestRebootChildSurvivesInstallerExit(t *testing.T) {
	if os.Getenv("ONEKVM_TEST_REBOOT_HELPER") == "1" {
		if err := delayedReboot(); err != nil {
			fmt.Fprintln(os.Stderr, err)
			os.Exit(1)
		}
		os.Exit(0)
	}
	bin := t.TempDir()
	gate, marker := filepath.Join(bin, "continue"), filepath.Join(bin, "rebooted")
	// Release the child even when the test fails; never leave a waiting process.
	t.Cleanup(func() { _ = os.WriteFile(gate, nil, 0600) })
	for name, body := range map[string]string{
		"sleep":  "while [ ! -f \"$REBOOT_TEST_GATE\" ]; do /bin/sleep 0.02; done\n",
		"reboot": ": > \"$REBOOT_TEST_MARKER\"\n",
	} {
		if err := os.WriteFile(filepath.Join(bin, name), []byte("#!/bin/sh\n"+body), 0700); err != nil {
			t.Fatal(err)
		}
	}
	executable, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	cmd := exec.CommandContext(ctx, executable, "-test.run=^TestRebootChildSurvivesInstallerExit$")
	cmd.Env = append(os.Environ(), "PATH="+bin, "ONEKVM_TEST_REBOOT_HELPER=1",
		"REBOOT_TEST_GATE="+gate, "REBOOT_TEST_MARKER="+marker)
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("installer did not exit while reboot was pending: %v\n%s", err, out)
	}
	if _, err := os.Stat(marker); !os.IsNotExist(err) {
		t.Fatal("reboot ran before the detached delay completed")
	}
	if err := os.WriteFile(gate, nil, 0600); err != nil {
		t.Fatal(err)
	}
	for deadline := time.Now().Add(3 * time.Second); time.Now().Before(deadline); {
		if _, err := os.Stat(marker); err == nil {
			return
		}
		time.Sleep(20 * time.Millisecond)
	}
	t.Fatal("reboot child did not survive installer exit")
}

func TestRebootRunsInSeparateSession(t *testing.T) {
	bin := t.TempDir()
	log := filepath.Join(bin, "events")
	t.Setenv("PATH", bin) // Never allow the host's real reboot command.
	t.Setenv("REBOOT_TEST_LOG", log)
	for name, body := range map[string]string{
		"sleep": "printf 'sleep:%s\\n' \"$1\" >> \"$REBOOT_TEST_LOG\"\n",
		"reboot": "read -r proc_pid proc_comm proc_state proc_parent proc_group proc_session proc_rest < /proc/$$/stat\n" +
			"printf 'reboot:%s:%s\\n' \"$$\" \"$proc_session\" >> \"$REBOOT_TEST_LOG\"\n",
	} {
		if err := os.WriteFile(filepath.Join(bin, name), []byte("#!/bin/sh\n"+body), 0700); err != nil {
			t.Fatal(err)
		}
	}
	cmd, err := rebootCommand()
	if err != nil {
		t.Fatal(err)
	}
	if cmd.Stdin != nil || cmd.Stdout != nil || cmd.Stderr != nil || !cmd.SysProcAttr.Setsid {
		t.Fatal("reboot must not inherit the installer terminal or session")
	}
	parentSession, err := unix.Getsid(0)
	if err != nil {
		t.Fatal(err)
	}
	if err := cmd.Run(); err != nil {
		t.Fatal(err)
	}
	events, err := os.ReadFile(log)
	want := fmt.Sprintf("sleep:1\nreboot:%d:%d\n", cmd.Process.Pid, cmd.Process.Pid)
	if err != nil || string(events) != want {
		t.Fatalf("reboot did not follow the one-second delay: %s, %v", events, err)
	}
	if cmd.Process.Pid == parentSession {
		t.Fatal("reboot shares the installer session")
	}
}
