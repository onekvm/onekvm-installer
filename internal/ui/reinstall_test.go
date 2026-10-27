package ui

import (
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/charmbracelet/x/ansi"
	"github.com/onekvm/onekvm-installer/internal/installer"
)

func TestReinstallOffersPreserveAndInitialize(t *testing.T) {
	m := testModel(t)
	m.opts.Device.Reinstall = true
	m.checkNext = "account"
	m.afterChecks()
	if m.page != reinstallModePage || len(m.menu.Items()) != 2 {
		t.Fatal("missing reinstall choice")
	}
	m.deps.snapshot = func(string) (installer.SystemSnapshot, error) {
		return installer.SystemSnapshot{Data: []byte("private snapshot"), Version: "0.2.0", Access: installer.AccessInfo{Username: "original-admin", Network: installer.Network{Interface: "eth0", Mode: "static", Address: "10.0.0.7/24"}}}, nil
	}
	runJobs(m, m.selectItem())
	if m.page != reviewPage || m.systemSnapshot == nil || m.username != "original-admin" {
		t.Fatal("preserve did not skip account initialization")
	}
	if strings.Contains(ansi.Strip(m.View()), "private snapshot") {
		t.Fatal("displayed private snapshot")
	}
	called := false
	m.deps.prepareConfig = func(_ context.Context, d installer.Device, _ string, _ *installer.ReleaseAsset, s installer.SystemSnapshot, _ installer.Reporter) (installer.Request, error) {
		called = true
		if !d.Reinstall || string(s.Data) != "private snapshot" {
			t.Fatal("lost chosen configuration")
		}
		return installer.Request{ConfigSHA256: strings.Repeat("a", 64)}, nil
	}
	runJobs(m, m.selectItem())
	if !called || m.req.ConfigSHA256 == "" {
		t.Fatal("did not stage preserved configuration")
	}
	m.goPage(reinstallModePage)
	m.menu.Select(1)
	m.selectItem()
	if m.page != accountPage || m.systemSnapshot != nil {
		t.Fatal("fresh installation retained old configuration")
	}
}

func TestSnapshotFailureStaysOnModeSelection(t *testing.T) {
	m := testModel(t)
	m.opts.Device.Reinstall = true
	m.goPage(reinstallModePage)
	m.deps.snapshot = func(string) (installer.SystemSnapshot, error) {
		return installer.SystemSnapshot{}, errors.New("cannot read configuration")
	}
	runJobs(m, m.selectItem())
	if m.page != reinstallModePage || m.systemSnapshot != nil || m.errKey == "" {
		t.Fatal("snapshot failure silently initialized the system")
	}
}
