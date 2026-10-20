package ui

import (
	"context"
	"errors"
	"fmt"
	_ "github.com/onekvm/onekvm-installer/internal/machines"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"

	"github.com/charmbracelet/bubbles/textinput"
	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
	"github.com/charmbracelet/x/ansi"
	"github.com/onekvm/onekvm-installer/internal/i18n"
	"github.com/onekvm/onekvm-installer/internal/installer"
)

func testModel(t *testing.T) *model {
	t.Helper()
	if _, err := installer.MachineFor("nanokvm"); err != nil {
		t.Skip("NanoKVM wizard fixtures require riscv64 or -tags=onekvm_test")
	}
	tr, err := i18n.New("en", os.Getenv)
	if err != nil {
		t.Fatal(err)
	}
	m := newModel(context.Background(), Options{Command: "install", Interface: "eth0", Text: tr,
		Device:  installer.Device{Profile: "nanokvm", Disk: "/dev/mmcblk0", Media: "SD", DataMount: t.TempDir()},
		Catalog: installer.DefaultCatalog}, defaults())
	m.deps.verify = func(context.Context, string, string, int64, installer.Progress) (installer.Image, error) {
		return installer.Image{Profile: "nanokvm"}, nil
	}
	m.deps.prepared = func(installer.Device, installer.Progress) (installer.Request, error) {
		return installer.Request{Image: installer.Image{Profile: "nanokvm"}}, nil
	}
	m.deps.wifi = func(string) (installer.WiFiSnapshot, error) { return installer.WiFiSnapshot{}, nil }
	m.deps.check = func(context.Context, installer.CheckContext) (installer.CheckResult, error) {
		return installer.CheckResult{}, nil
	}
	m.deps.target = func(_ string, d installer.Device, profile string) (installer.Device, error) {
		if profile != d.Profile {
			return d, fmt.Errorf("wrong test profile")
		}
		return d, nil
	}
	m.Update(splashTickMsg{m.splash.started.Add(splashDuration)})
	cmd := m.selectItem() // Confirm the initial English language selection.
	if cmd != nil {
		m.Update(cmd())
	}
	return m
}

func key(m *model, k tea.KeyType) tea.Cmd {
	_, cmd := m.Update(tea.KeyMsg{Type: k})
	return cmd
}

func runJobs(m *model, cmd tea.Cmd) {
	for cmd != nil && m.page == busyPage {
		_, cmd = m.Update(cmd())
	}
}

func isQuit(cmd tea.Cmd) bool {
	if cmd == nil {
		return false
	}
	_, ok := cmd().(tea.QuitMsg)
	return ok
}

func TestVerticalSelection(t *testing.T) {
	m := testModel(t)
	view := ansi.Strip(m.View())
	if !strings.Contains(view, "› GitHub Release") || strings.Index(view, "GitHub Release") >= strings.Index(view, "Local image file") {
		t.Fatal("first vertical option is not selected", view)
	}
	key(m, tea.KeyDown)
	if !strings.Contains(ansi.Strip(m.View()), "› Local image file") {
		t.Fatal("arrow key did not move selection")
	}
	key(m, tea.KeyCtrlL)
	if m.menu.Index() != 1 || m.opts.Text.Language != "en" {
		t.Fatal("Ctrl+L changed language or selection")
	}
}

func TestGitHubUsesLatestWithoutTagPrompt(t *testing.T) {
	m := testModel(t)
	m.deps.fetch = func(_ context.Context, repo, tag string) (installer.Release, error) {
		if repo != "onekvm/onekvm-release-nanokvm" || tag != "" {
			t.Fatalf("wrong default release: %s %s", repo, tag)
		}
		return installer.Release{Tag: "v2", Assets: []installer.ReleaseAsset{{Name: "onekvm-nanokvm.img", Size: 100}}}, nil
	}
	cmd := key(m, tea.KeyEnter)
	if m.page != busyPage {
		t.Fatal("GitHub selection did not fetch latest immediately")
	}
	m.Update(cmd())
	if m.page != assetsPage || len(m.assets) != 1 {
		t.Fatal("did not show matching image list")
	}
}

func TestPasswordsUseAsterisksAndNeverAppearInView(t *testing.T) {
	m := testModel(t)
	m.goPage(accountPage)
	for _, i := range []int{1, 2} {
		f := &m.fields[i].input
		if f.EchoMode != textinput.EchoPassword || f.EchoCharacter != '*' {
			t.Fatal("password field is not masked with asterisks")
		}
		f.SetValue("a-secret-password")
	}
	view := ansi.Strip(m.View())
	if strings.Contains(view, "a-secret-password") || !strings.Contains(view, "*****************") {
		t.Fatal("password was exposed or not echoed as asterisks", view)
	}
}

func TestWizardStagesBeforeExplicitDiskConfirmation(t *testing.T) {
	m := testModel(t)
	image := filepath.Join(t.TempDir(), "image.img")
	if err := os.WriteFile(image, []byte("fixture"), 0600); err != nil {
		t.Fatal(err)
	}
	var prepared, armed, rebooted bool
	m.deps.prepare = func(_ context.Context, _ installer.Device, path string, _ *installer.ReleaseAsset, firstboot []byte, _ installer.Reporter) (installer.Request, error) {
		prepared = true
		if path != image || !strings.Contains(string(firstboot), "encrypted-password") || strings.Contains(string(firstboot), "test-password") {
			t.Fatal("invalid staged account or image")
		}
		return installer.Request{Image: installer.Image{Profile: "nanokvm", BuildID: "test"}}, nil
	}
	m.deps.arm = func(_ installer.Device, report installer.Reporter) (installer.AccessInfo, error) {
		err := report("Installing recovery boot files (keep power connected)", func(installer.Progress) error { armed = true; return nil })
		return installer.AccessInfo{Username: "admin", Network: installer.Network{Interface: "eth0", Mode: "dhcp"}}, err
	}
	m.deps.reboot = func() error { rebooted = true; return nil }
	key(m, tea.KeyDown)
	key(m, tea.KeyEnter)
	m.fields[0].input.SetValue(image)
	runJobs(m, m.submitForm())
	m.fields[1].input.SetValue("test-password")
	m.fields[2].input.SetValue("test-password")
	m.submitForm()
	if m.page != timezonePage || m.fields[0].input.Value() != "UTC" {
		t.Fatal("account did not advance to the UTC timezone step")
	}
	m.submitForm()
	key(m, tea.KeyDown) // DHCP
	key(m, tea.KeyEnter)
	m.submitForm()
	if m.page != reviewPage || prepared || armed {
		t.Fatal("work started before review")
	}
	cmd := key(m, tea.KeyEnter)
	m.Update(cmd())
	if m.page != preparedPage || !prepared || armed || m.password != "" {
		t.Fatal("staging crossed confirmation boundary or retained password")
	}
	m.Update(key(m, tea.KeyEnter)())
	m.fields[0].input.SetValue("yes")
	if cmd := m.submitForm(); cmd != nil || armed {
		t.Fatal("wrong erase confirmation accepted")
	}
	m.fields[0].input.SetValue("ERASE /dev/mmcblk0")
	cmd = m.submitForm()
	if armed {
		t.Fatal("arm command ran synchronously")
	}
	m.Update(cmd())
	if m.page != rebootPage || !armed || rebooted || m.menu.SelectedItem().(menuItem).id != "later" {
		t.Fatal("unexpected reboot without user choice")
	}
	if !isQuit(key(m, tea.KeyEnter)) || rebooted {
		t.Fatal("default reboot choice did not exit safely")
	}
	if !strings.Contains(m.finalMessage(), "Username: admin") || !strings.Contains(m.finalMessage(), "https://<device-ip>/") {
		t.Fatal("wizard did not retain verified login guidance", m.finalMessage())
	}
	m.goPage(rebootPage)
	key(m, tea.KeyDown)
	cmd = key(m, tea.KeyEnter)
	if rebooted || cmd == nil {
		t.Fatal("explicit reboot must run as a separate job")
	}
	m.Update(cmd())
	if !rebooted || m.message != "done.reboot" || m.page != finishedPage {
		t.Fatal("explicit reboot choice did not reboot")
	}
}

func TestRebootSchedulingExitsWithMessage(t *testing.T) {
	m := testModel(t)
	m.deps.reboot = func() error { return nil }
	m.goPage(rebootPage)
	key(m, tea.KeyDown)
	cmd := key(m, tea.KeyEnter)
	if _, next := m.Update(cmd()); !isQuit(next) || m.message != "done.reboot" || m.page != finishedPage {
		t.Fatal("scheduled reboot did not immediately exit with its final message")
	}
}

func TestResumedArmUsesStagedAccessSettings(t *testing.T) {
	m := testModel(t)
	m.opts.Command = "arm"
	m.network = installer.Network{Interface: "eth0", Mode: "dhcp", Hostname: "old-host"}
	m.deps.arm = func(installer.Device, installer.Reporter) (installer.AccessInfo, error) {
		return installer.AccessInfo{Username: "staged-admin", Network: installer.Network{
			Interface: "eth0", Mode: "static", Address: "10.100.99.108/24", Hostname: "staged-host"}}, nil
	}
	runJobs(m, m.startWizard())
	m.fields[0].input.SetValue("ERASE /dev/mmcblk0")
	cmd := m.submitForm()
	m.Update(cmd())
	if m.page != rebootPage || !strings.Contains(m.View(), "staged-admin") {
		t.Fatal("resumed installation did not load staged access settings")
	}
	if !isQuit(key(m, tea.KeyEnter)) {
		t.Fatal("reboot later did not exit")
	}
	text := m.finalMessage()
	if !strings.Contains(text, "https://10.100.99.108/") || !strings.Contains(text, "staged-admin") || !strings.Contains(text, "staged-host") || strings.Contains(text, "old-host") {
		t.Fatal("resumed installation showed current-system settings", text)
	}
}

func TestRebootInstallationEstimateByMachine(t *testing.T) {
	for _, language := range []string{"zh", "zh-TW", "en"} {
		m := testModel(t)
		m.opts.Text, _ = i18n.New(language, os.Getenv)
		m.message = "done.reboot"
		want := m.opts.Text.T("done.reboot") + "\n" + m.opts.Text.T("done.reboot.estimate", 5)
		if got := m.finalMessage(); got != want {
			t.Fatalf("%s NanoKVM message: %q", language, got)
		}
		m.opts.Device.Profile = "nanokvm-pro"
		if m.finalMessage() != m.opts.Text.T("done.reboot") {
			t.Fatal("an unsupported machine borrowed the NanoKVM installation estimate")
		}
		m.message = "done.armed"
		if m.finalMessage() != m.opts.Text.T("done.armed") {
			t.Fatal("reboot-later message included an installation estimate")
		}
	}
}

func TestRebootFailureCanExit(t *testing.T) {
	m := testModel(t)
	m.deps.reboot = func() error { return errors.New("reboot failed") }
	m.goPage(rebootPage)
	key(m, tea.KeyDown)
	cmd := key(m, tea.KeyEnter)
	m.Update(cmd())
	if m.page != failedPage || m.err == nil || !isQuit(key(m, tea.KeyCtrlC)) {
		t.Fatal("failed reboot left an undismissable screen")
	}
}

func TestCtrlCExitsEveryPageBeforeBootWrite(t *testing.T) {
	for _, p := range []page{splashPage, languagePage, sourcePage, localPage, assetsPage, accountPage, networkPage, copiedDHCPPage, networkFormPage, reviewPage, preparedPage, confirmPage} {
		m := testModel(t)
		m.goPage(p)
		if !isQuit(key(m, tea.KeyCtrlC)) {
			t.Errorf("Ctrl+C did not exit page %d", p)
		}
	}
	for _, kind := range []string{"github", "network", "prepare", "arm"} {
		m := testModel(t)
		wrote := false
		cmd := m.begin(kind, "op.recheck", func(_ context.Context, report installer.Reporter) (any, error) {
			return nil, report("Installing recovery boot files (keep power connected)", func(installer.Progress) error { wrote = true; return nil })
		})
		if !isQuit(key(m, tea.KeyCtrlC)) {
			t.Fatalf("Ctrl+C did not exit %s before writes", kind)
		}
		r := cmd().(jobResult)
		if wrote || !errors.Is(r.err, context.Canceled) {
			t.Fatal("operation wrote after the UI exited")
		}
	}
}

func TestCancellationCannotRaceFirstBootWrite(t *testing.T) {
	for i := 0; i < 100; i++ {
		s := &taskStatus{}
		var wg sync.WaitGroup
		var stopped, wrote bool
		wg.Add(2)
		go func() { defer wg.Done(); stopped = s.stop() }()
		go func() {
			defer wg.Done()
			_ = s.reporter(context.Background())("Installing recovery boot files (keep power connected)", func(installer.Progress) error { wrote = true; return nil })
		}()
		wg.Wait()
		if stopped && wrote {
			t.Fatal("accepted immediate exit while boot was being written")
		}
	}
}

func TestCtrlCDuringBootWriteWaitsForCompletion(t *testing.T) {
	m := testModel(t)
	started, release := make(chan struct{}), make(chan struct{})
	cmd := m.begin("arm", "op.recheck", func(_ context.Context, report installer.Reporter) (any, error) {
		return nil, report("Installing recovery boot files (keep power connected)", func(installer.Progress) error {
			close(started)
			<-release
			return nil
		})
	})
	result := make(chan tea.Msg, 1)
	go func() { result <- cmd() }()
	<-started
	if key(m, tea.KeyCtrlC) != nil || !m.exitAfterJob {
		t.Fatal("boot write was abandoned")
	}
	close(release)
	_, quit := m.Update(<-result)
	if !isQuit(quit) || m.message != "done.armed" {
		t.Fatal("did not finish armed state before quitting")
	}
}

func TestWizardFitsStandardTerminal(t *testing.T) {
	for _, lang := range []string{"en", "zh", "zh-TW"} {
		for _, p := range []page{languagePage, sourcePage, localPage, accountPage, timezonePage, networkPage, copiedDHCPPage, networkFormPage, reviewPage, preparedPage, confirmPage, rebootPage} {
			m := testModel(t)
			m.opts.Text, _ = i18n.New(lang, os.Getenv)
			m.width, m.height = 80, 24
			m.network.Mode = "static"
			m.opts.Device.HardwareModel = "Sipeed NanoKVM Cube"
			m.req = installer.Request{Image: installer.Image{BuildID: "0.2.0-onekvm-nanokvm-r9", Machine: "onekvm-nanokvm"}, ImageBytes: 190722560}
			m.goPage(p)
			if h := lipgloss.Height(m.View()); h > 24 {
				t.Errorf("%s page %d needs %d rows", lang, p, h)
			}
		}
		for _, kind := range []string{"prepare", "arm", "github"} {
			m := testModel(t)
			m.opts.Text, _ = i18n.New(lang, os.Getenv)
			m.width, m.height = 80, 24
			label := "op.prepare"
			if kind != "prepare" {
				label = "op.recheck"
			}
			m.task = newTaskStatus(kind, label, true)
			m.task.done, m.task.total = 50, 100
			m.goPage(busyPage)
			if h := lipgloss.Height(m.View()); h > 24 {
				t.Errorf("%s %s processing needs %d rows", lang, kind, h)
			}
		}
	}
}

func TestProcessingShowsCompletedCurrentAndPendingSteps(t *testing.T) {
	m := testModel(t)
	started, release := make(chan struct{}), make(chan struct{})
	defer close(release)
	failure := errors.New("verification failed")
	cmd := m.begin("prepare", "op.prepare", func(ctx context.Context, report installer.Reporter) (any, error) {
		if err := report("op.prepare", func(installer.Progress) error { return nil }); err != nil {
			return nil, err
		}
		return nil, report("Validating image metadata and SHA-256", func(p installer.Progress) error {
			p(25, 100)
			close(started)
			<-release
			return failure
		})
	})
	result := make(chan tea.Msg, 1)
	go func() { result <- cmd() }()
	<-started
	view := ansi.Strip(m.View())
	for _, want := range []string{"✓ Creating first-boot configuration", "Validating image metadata and SHA-256", "○ Extracting installer recovery kernel", "○ Saving and checking installation files", "25%"} {
		if !strings.Contains(view, want) {
			t.Errorf("missing %q: %s", want, view)
		}
	}
	// Release the operation without closing the deferred channel twice.
	release <- struct{}{}
	m.Update(<-result)
	view = ansi.Strip(m.View())
	if !strings.Contains(view, "✗ Validating image") || strings.Contains(view, "✓ Extracting installer recovery kernel") || !errors.Is(m.err, failure) {
		t.Fatal("failed or unstarted work was marked complete", view)
	}
}

func TestProcessingWithoutTotalAlwaysShowsMovingBar(t *testing.T) {
	m := testModel(t)
	m.task = newTaskStatus("github", "op.github", false)
	m.goPage(busyPage)
	before := ansi.Strip(m.View())
	m.pulse += 3
	after := ansi.Strip(m.View())
	if !strings.Contains(before, "━") || before == after || strings.Contains(before, "%") {
		t.Fatal("unknown total lacks an animated bar or shows a false percentage", before, after)
	}
}

func TestLanguageSelectionPrecedesAllInteractiveCommands(t *testing.T) {
	for _, command := range []string{"install", "prepare", "arm", "solution"} {
		for _, language := range []string{"en", "zh", "zh-TW"} {
			tr, _ := i18n.New(language, os.Getenv)
			m := newModel(context.Background(), Options{Command: command, Text: tr}, defaults())
			m.deps.check = func(context.Context, installer.CheckContext) (installer.CheckResult, error) {
				return installer.CheckResult{}, nil
			}
			m.Update(splashTickMsg{m.splash.started.Add(splashDuration)})
			if m.page != languagePage || m.menu.SelectedItem().(menuItem).id != language || m.task != nil {
				t.Fatalf("%s/%s skipped initial language selection", command, language)
			}
			view := ansi.Strip(m.View())
			if !strings.Contains(view, "English") || !strings.Contains(view, "简体中文") || !strings.Contains(view, "繁體中文") {
				t.Fatal("language menu is missing an option", view)
			}
			m.menu.Select(2)
			m.Update(key(m, tea.KeyEnter)())
			if m.opts.Text.Language != "zh-TW" || m.page == languagePage {
				t.Fatal("Traditional Chinese selection did not advance the wizard")
			}
			if command == "install" && !strings.Contains(ansi.Strip(m.View()), "選擇映像檔來源") {
				t.Fatal("wizard did not use the selected Traditional Chinese translation")
			}
		}
	}
}

func TestCopyDHCPRequiresChoiceBeforeStaticConversion(t *testing.T) {
	for _, static := range []bool{false, true} {
		m := testModel(t)
		m.goPage(networkPage)
		current := installer.Network{Interface: "eth0", Mode: "dhcp", Hostname: "kvm-e73e"}
		m.deps.network = func(string, string) (installer.Network, error) { return current, nil }
		converted := false
		m.deps.static = func(_ string, n installer.Network) (installer.Network, error) {
			converted = true
			if n.Mode != "dhcp" {
				t.Fatal("DHCP changed before conversion")
			}
			n.Mode, n.Address, n.Gateway = "static", "10.100.99.172/24", "10.100.99.1"
			return n, nil
		}
		cmd := key(m, tea.KeyEnter)
		m.Update(cmd())
		if m.page != copiedDHCPPage || converted {
			t.Fatal("skipped DHCP choice")
		}
		if !strings.Contains(ansi.Strip(m.View()), "IP address may change") {
			t.Fatal(m.View())
		}
		if static {
			key(m, tea.KeyDown)
		}
		cmd = key(m, tea.KeyEnter)
		if static {
			m.Update(cmd())
		}
		if m.page != networkFormPage || converted != static || m.network.Hostname != "kvm-e73e" {
			t.Fatal("wrong copy flow", m.page, m.network)
		}
		if static && m.fields[0].input.Value() != "10.100.99.172/24" {
			t.Fatal("current address not copied")
		}
	}
}

func TestManualNetworkKeepsCurrentHostname(t *testing.T) {
	m := testModel(t)
	m.network.Hostname = "machine-hostname"
	m.goPage(networkPage)
	key(m, tea.KeyDown)
	key(m, tea.KeyEnter)
	if m.network.Hostname != "machine-hostname" || m.fields[1].input.Value() != "machine-hostname" {
		t.Fatal(m.network)
	}
}

func TestPasswordValidationUsesCharacters(t *testing.T) {
	if got := accountError("admin", "一二三四", "一二三四"); got != "error.password" {
		t.Fatal(got)
	}
	if got := accountError("admin", "一二三四五六七八", "一二三四五六七八"); got != "" {
		t.Fatal(got)
	}
	if got := accountError("admin", strings.Repeat("密", 25), strings.Repeat("密", 25)); got != "error.password_long" {
		t.Fatal(got)
	}
}

func TestBootCleanupClosesCancellationBeforeDeletingFiles(t *testing.T) {
	m := testModel(t)
	started, release := make(chan struct{}), make(chan struct{})
	cmd := m.begin("arm", "op.recheck", func(_ context.Context, report installer.Reporter) (any, error) {
		err := report("op.boot_cleanup", func(installer.Progress) error {
			close(started)
			<-release
			return nil
		})
		if err != nil {
			return nil, err
		}
		return nil, report("Installing recovery boot files (keep power connected)", func(installer.Progress) error { return nil })
	})
	result := make(chan tea.Msg, 1)
	go func() { result <- cmd() }()
	<-started
	if key(m, tea.KeyCtrlC) != nil || !m.exitAfterJob {
		t.Fatal("cleanup was abandoned before recovery files were written")
	}
	close(release)
	_, quit := m.Update(<-result)
	if !isQuit(quit) || m.message != "done.armed" {
		t.Fatal("did not finish boot replacement before exiting")
	}
}
