package ui

import (
	"context"
	"os"
	"strings"
	"testing"

	"github.com/onekvm/onekvm-installer/internal/i18n"
	"github.com/onekvm/onekvm-installer/internal/installer"
)

func TestTimezoneStepDefaultsToUTCAndValidatesBeforeNetwork(t *testing.T) {
	m := testModel(t)
	m.goPage(timezonePage)
	if m.fields[0].input.Value() != "UTC" {
		t.Fatal("timezone did not default to UTC")
	}
	m.fields[0].input.SetValue("Invalid/Zone")
	if m.submitForm() != nil || m.page != timezonePage || m.errKey != "error.timezone" {
		t.Fatal("invalid zone advanced the wizard")
	}
	m.fields[0].input.SetValue("Asia/Taipei")
	m.submitForm()
	if m.page != networkPage || m.timezone != "Asia/Taipei" {
		t.Fatal("selected zone lost")
	}
	m.back()
	if m.page != timezonePage || m.fields[0].input.Value() != "Asia/Taipei" {
		t.Fatal("back navigation lost timezone")
	}
}

func TestRegionSettingsReachBothInstallationPaths(t *testing.T) {
	for _, direct := range []bool{false, true} {
		m := testModel(t)
		if direct {
			m = proModel(t)
			m.opts.Device.Profile = "pro-sd"
			m.opts.Device.Disk = "/dev/mmcblk1"
		}
		m.opts.Text, _ = i18n.New("zh-TW", os.Getenv)
		m.timezone = "Asia/Taipei"
		m.username, m.password = "operator", "testing-only"
		m.network = installer.Network{Interface: "eth0", Mode: "dhcp"}
		called := false
		check := func(b []byte) {
			called = true
			if !strings.Contains(string(b), "set system language zh_tw\n") || !strings.Contains(string(b), "set system time-zone Asia/Taipei\n") {
				t.Fatal("installer did not pass selected region")
			}
		}
		m.deps.prepare = func(_ context.Context, _ installer.Device, _ string, _ *installer.ReleaseAsset, b []byte, _ installer.Reporter) (installer.Request, error) {
			check(b)
			return installer.Request{}, nil
		}
		m.deps.direct = func(_ context.Context, _ installer.Device, _ string, _ *installer.ReleaseAsset, b []byte, _ installer.Reporter) (installer.AccessInfo, error) {
			check(b)
			return installer.AccessInfo{}, nil
		}
		if direct {
			m.goPage(confirmPage)
			m.fields[0].input.SetValue("ERASE /dev/mmcblk1")
			runJobs(m, m.submitForm())
		} else {
			m.goPage(reviewPage)
			runJobs(m, m.selectItem())
		}
		if !called {
			t.Fatal("installation path did not run")
		}
	}
}
