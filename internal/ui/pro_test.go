package ui

import (
	"context"
	"errors"
	"strings"
	"testing"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/x/ansi"
	"github.com/onekvm/onekvm-installer/internal/installer"
)

func proModel(t *testing.T) *model {
	t.Helper()
	m := testModel(t)
	m.opts.Device.Profile = "pro-emmc"
	m.opts.Device.Media = "MMC"
	m.opts.Device.Bytes = 8 * 1024 * installer.MiB
	m.deps.target = func(_ string, d installer.Device, profile string) (installer.Device, error) {
		if profile != "pro-sd" && profile != "pro-emmc" {
			return d, errors.New("wrong family")
		}
		d.Profile = profile
		d.Disk = "/dev/mmcblk0"
		d.Media = "MMC"
		if profile == "pro-sd" {
			d.Disk = "/dev/mmcblk1"
			d.Media = "SD"
		}
		return d, nil
	}
	return m
}
func TestProSDSelectsImageTargetAndSkipsStaging(t *testing.T) {
	m := proModel(t)
	m.finishJob(jobResult{kind: "github", data: installer.Release{Assets: []installer.ReleaseAsset{
		{Name: "onekvm-nanokvm-pro-sd-0.2.0.img", Size: 100}, {Name: "onekvm-nanokvm-pro-emmc-0.2.0.img", Size: 200}, {Name: "onekvm-nanokvm-0.2.0.img", Size: 300},
	}}})
	if len(m.assets) != 2 {
		t.Fatal("must offer both Pro media", m.assets)
	}
	m.deps.download = func(_ context.Context, d installer.Device, _ installer.ReleaseAsset, image *installer.DownloadedImage, _ installer.Reporter) error {
		if d.Profile != "pro-sd" {
			t.Fatal("wrong download target")
		}
		image.Source = "/run/onekvm-test/image.img"
		image.Image = installer.Image{Profile: "pro-sd"}
		return nil
	}
	cmd := m.selectItem()
	if m.page != busyPage || m.opts.Device.Profile != "pro-sd" || m.opts.Device.Disk != "/dev/mmcblk1" {
		t.Fatal("asset did not select SD target")
	}
	runJobs(m, cmd)
	if m.page != accountPage {
		t.Fatal("verified SD download did not continue to account setup")
	}
	m.username, m.password = "operator", "private-test-password"
	m.network = installer.Network{Interface: "eth0", Mode: "static", Address: "10.100.99.50/24"}
	m.deps.prepare = func(context.Context, installer.Device, string, *installer.ReleaseAsset, []byte, installer.Reporter) (installer.Request, error) {
		t.Fatal("SD direct write must never stage")
		return installer.Request{}, nil
	}
	m.deps.arm = func(installer.Device, installer.Reporter) (installer.AccessInfo, error) {
		t.Fatal("SD direct write must never arm recovery")
		return installer.AccessInfo{}, nil
	}
	calls := 0
	m.deps.direct = func(ctx context.Context, d installer.Device, source string, asset *installer.ReleaseAsset, firstboot []byte, report installer.Reporter) (installer.AccessInfo, error) {
		calls++
		if d.Disk != "/dev/mmcblk1" || asset != nil || source != "/run/onekvm-test/image.img" || strings.Contains(string(firstboot), "private-test-password") {
			t.Fatal("incorrect direct install inputs")
		}
		for _, label := range []string{"op.direct_check", "op.direct_write", "op.direct_config"} {
			if err := report(label, func(p installer.Progress) error { p(1, 1); return nil }); err != nil {
				return installer.AccessInfo{}, err
			}
		}
		return installer.AccessInfo{Username: "operator", Network: m.network}, nil
	}
	m.goPage(reviewPage)
	if view := ansi.Strip(m.View()); !strings.Contains(view, "/dev/mmcblk1") || !strings.Contains(view, "SD card") {
		t.Fatal("target not reviewable", view)
	}
	cmd = m.selectItem()
	m.Update(cmd())
	if m.page != confirmPage || calls != 0 {
		t.Fatal("wrote without explicit confirmation")
	}
	m.fields[0].input.SetValue("ERASE /dev/mmcblk0")
	if cmd := m.submitForm(); cmd != nil || calls != 0 {
		t.Fatal("accepted wrong disk")
	}
	m.fields[0].input.SetValue("ERASE /dev/mmcblk1")
	cmd = m.submitForm()
	if cmd == nil || m.page != busyPage {
		t.Fatal("did not start direct job")
	}
	if len(m.task.steps) != 4 {
		t.Fatal("missing planned direct steps")
	}
	for _, step := range m.task.steps {
		if step.key == "op.stage" || step.key == "op.cleanup" {
			t.Fatal("direct path planned staging")
		}
	}
	m.Update(cmd())
	if calls != 1 || m.page != rebootPage || m.message != "done.direct" || m.password != "" {
		t.Fatal("wrong direct completion")
	}
	message := m.finalMessage()
	if !strings.Contains(message, "operator") || !strings.Contains(message, "https://10.100.99.50/") || strings.Contains(message, "private-test-password") || strings.Contains(message, "$2a$") {
		t.Fatal("wrong access summary", message)
	}
	m.message = "done.reboot"
	if strings.Contains(m.finalMessage(), "minutes") {
		t.Fatal("direct SD completion claims recovery installation remains")
	}
}
func TestProLocalImageMetadataSelectsEMMCAndMissingSDIsRecoverable(t *testing.T) {
	m := proModel(t)
	m.deps.verify = func(context.Context, string, string, int64, installer.Progress) (installer.Image, error) {
		return installer.Image{Profile: "pro-emmc"}, nil
	}
	m.source = "/test/emmc.img"
	runJobs(m, m.verifyLocalImage())
	if m.page != accountPage || m.opts.Device.Disk != "/dev/mmcblk0" || installer.DirectInstall(m.opts.Device) {
		t.Fatal("local eMMC metadata selected wrong path")
	}
	m.deps.target = func(string, installer.Device, string) (installer.Device, error) {
		return installer.Device{}, errors.New("insert an SD card")
	}
	if m.selectTarget("pro-sd") || m.errKey == "" || m.opts.Device.Profile != "pro-emmc" {
		t.Fatal("missing SD target was accepted")
	}
}
func TestDirectWriteCancellationBoundary(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	status := newTaskStatus("direct", "op.prepare", true)
	if err := status.reporter(ctx)("op.direct_check", func(installer.Progress) error { return nil }); err != nil {
		t.Fatal(err)
	}
	if !status.stop() {
		t.Fatal("cannot cancel before SD write")
	}
	called := false
	if err := status.reporter(ctx)("op.direct_write", func(installer.Progress) error { called = true; return nil }); !errors.Is(err, context.Canceled) || called {
		t.Fatal("cancelled job started writing", err)
	}
	status = newTaskStatus("direct", "op.prepare", true)
	if err := status.reporter(ctx)("op.direct_write", func(installer.Progress) error {
		if status.stop() {
			t.Fatal("Ctrl+C interrupted destructive write")
		}
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	cancel()
	if err := status.reporter(ctx)("op.direct_config", func(installer.Progress) error { return nil }); err != nil {
		t.Fatal("parent cancellation interrupted a started write", err)
	}
	m := proModel(t)
	m.task = status
	m.page = busyPage
	m.taskCancel = cancel
	if cmd := key(m, tea.KeyCtrlC); cmd != nil || !m.exitAfterJob {
		t.Fatal("did not wait for active SD write")
	}
}
