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

func githubImageModel(t *testing.T) *model {
	t.Helper()
	m := testModel(t)
	m.assets = []installer.ReleaseAsset{{Name: "onekvm-nanokvm-test.img", Size: 100}}
	m.goPage(assetsPage)
	return m
}

func TestGitHubDownloadPrecedesAccountAndIsReused(t *testing.T) {
	m := githubImageModel(t)
	downloads, preparations := 0, 0
	m.deps.download = func(ctx context.Context, d installer.Device, a installer.ReleaseAsset, image *installer.DownloadedImage, report installer.Reporter) error {
		downloads++
		if m.page != busyPage || m.username != "" || m.password != "" || d.Profile != "nanokvm" || a.Name != m.asset.Name {
			t.Fatal("download did not run immediately after image selection")
		}
		if err := report("op.download", func(p installer.Progress) error { p(100, 100); return nil }); err != nil {
			return err
		}
		return report("op.verify", func(installer.Progress) error {
			image.Source = "/data/test-download/image.img"
			image.Image = installer.Image{Profile: "nanokvm", BuildID: "verified-build"}
			return nil
		})
	}
	m.deps.prepare = func(context.Context, installer.Device, string, *installer.ReleaseAsset, []byte, installer.Reporter) (installer.Request, error) {
		t.Fatal("verified download was sent through the original download path")
		return installer.Request{}, nil
	}
	m.deps.prepareDownloaded = func(_ context.Context, _ installer.Device, image *installer.DownloadedImage, firstboot []byte, _ installer.Reporter) (installer.Request, error) {
		preparations++
		if image != m.downloaded || image.Source != m.source || image.Image.BuildID != "verified-build" || !strings.Contains(string(firstboot), "encrypted-password") {
			t.Fatal("preparation did not reuse the verified image and account")
		}
		return installer.Request{Image: image.Image}, nil
	}
	cmd := key(m, tea.KeyEnter)
	if cmd == nil || m.page != busyPage || downloads != 0 {
		t.Fatal("selection did not schedule an immediate download")
	}
	view := ansi.Strip(m.View())
	if !strings.Contains(view, "Downloading full image") || !strings.Contains(view, "○ Validating image") {
		t.Fatal("download did not show current and pending steps", view)
	}
	runJobs(m, cmd)
	if downloads != 1 || m.page != accountPage || m.source != "/data/test-download/image.img" {
		t.Fatal("account setup was not gated by successful validation")
	}
	m.fields[1].input.SetValue("test-password")
	m.fields[2].input.SetValue("test-password")
	m.submitForm()
	if m.page != timezonePage {
		t.Fatal("downloaded image skipped the timezone step")
	}
	m.submitForm()
	key(m, tea.KeyDown) // DHCP
	key(m, tea.KeyEnter)
	m.submitForm()
	cmd = key(m, tea.KeyEnter)
	for _, step := range m.task.stepSnapshot() {
		if step.key == "op.download" || step.key == "op.stage" {
			t.Fatal("cached image planned another download or copy")
		}
	}
	m.Update(cmd())
	if m.page != preparedPage || downloads != 1 || preparations != 1 {
		t.Fatal("cached image was not prepared exactly once")
	}
}

func TestGitHubDownloadFailureCanRetry(t *testing.T) {
	m := githubImageModel(t)
	calls := 0
	m.deps.download = func(_ context.Context, _ installer.Device, _ installer.ReleaseAsset, image *installer.DownloadedImage, _ installer.Reporter) error {
		calls++
		if calls == 1 {
			return errors.New("payload SHA-256 mismatch")
		}
		image.Source = "/data/retry/image.img"
		image.Image = installer.Image{Profile: "nanokvm"}
		return nil
	}
	runJobs(m, key(m, tea.KeyEnter))
	if m.page != assetsPage || m.errKey != "error.download" || m.downloaded != nil || m.err != nil {
		t.Fatal("failed validation did not return to the image list")
	}
	runJobs(m, key(m, tea.KeyEnter))
	if calls != 2 || m.page != accountPage || m.errKey != "" {
		t.Fatal("download could not be retried")
	}
}

func TestCtrlCCancelsGitHubDownloadAndLateResult(t *testing.T) {
	m := githubImageModel(t)
	started := make(chan struct{})
	m.deps.download = func(ctx context.Context, _ installer.Device, _ installer.ReleaseAsset, _ *installer.DownloadedImage, report installer.Reporter) error {
		return report("op.verify", func(p installer.Progress) error {
			p(1, 10)
			close(started)
			<-ctx.Done()
			return ctx.Err()
		})
	}
	cmd := key(m, tea.KeyEnter)
	result := make(chan tea.Msg, 1)
	go func() { result <- cmd() }()
	<-started
	if !strings.Contains(ansi.Strip(m.View()), "10%") || !isQuit(key(m, tea.KeyCtrlC)) || m.downloaded != nil {
		t.Fatal("verification did not show progress or exit immediately")
	}
	m.Update(<-result)
	if !m.cancelled || m.View() != "" || m.err != nil {
		t.Fatal("late completion revived a canceled wizard")
	}
}
