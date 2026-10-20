// Package ui implements a single Bubble Tea program for the entire wizard.
package ui

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"
	"unicode"
	"unicode/utf8"

	"github.com/charmbracelet/bubbles/list"
	"github.com/charmbracelet/bubbles/progress"
	"github.com/charmbracelet/bubbles/spinner"
	"github.com/charmbracelet/bubbles/textinput"
	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
	"github.com/onekvm/onekvm-installer/internal/i18n"
	"github.com/onekvm/onekvm-installer/internal/installer"
	"golang.org/x/term"
)

type Options struct {
	Command, Image, Tag, Interface string
	Device                         installer.Device
	Catalog                        installer.Catalog
	Text                           i18n.Translator
}

type services struct {
	fetch             func(context.Context, string, string) (installer.Release, error)
	network           func(string, string) (installer.Network, error)
	wifi              func(string) (installer.WiFiSnapshot, error)
	static            func(string, installer.Network) (installer.Network, error)
	prepare           func(context.Context, installer.Device, string, *installer.ReleaseAsset, []byte, installer.Reporter) (installer.Request, error)
	arm               func(installer.Device, installer.Reporter) (installer.AccessInfo, error)
	reboot            func() error
	verify            func(context.Context, string, string, int64, installer.Progress) (installer.Image, error)
	prepared          func(installer.Device, installer.Progress) (installer.Request, error)
	target            func(string, installer.Device, string) (installer.Device, error)
	direct            func(context.Context, installer.Device, string, *installer.ReleaseAsset, []byte, installer.Reporter) (installer.AccessInfo, error)
	download          func(context.Context, installer.Device, installer.ReleaseAsset, *installer.DownloadedImage, installer.Reporter) error
	prepareDownloaded func(context.Context, installer.Device, *installer.DownloadedImage, []byte, installer.Reporter) (installer.Request, error)
	check             func(context.Context, installer.CheckContext) (installer.CheckResult, error)
	repair            func(context.Context, installer.CheckContext, string) (installer.RepairPlan, error)
}

func defaults() services {
	return services{fetch: installer.FetchRelease, network: installer.CopyEthernetNetwork, wifi: installer.CopyWiFi, static: installer.StaticNetwork, prepare: installer.Prepare,
		arm: installer.Arm, reboot: delayedReboot, verify: installer.VerifyContext, prepared: installer.LoadPrepared, target: installer.ProbeTarget, direct: installer.InstallDirect,
		download: installer.DownloadImage, prepareDownloaded: installer.PrepareDownloaded, check: installer.CheckSolutions, repair: installer.PrepareRepair}
}

type page uint8

const (
	splashPage page = iota
	languagePage
	sourcePage
	localPage
	assetsPage
	accountPage
	timezonePage
	networkPage
	copiedDHCPPage
	networkFormPage
	wifiPage
	wifiFormPage
	reviewPage
	busyPage
	preparedPage
	confirmPage
	rebootPage
	finishedPage
	failedPage
	issuesPage
	solutionPage
	repairConfirmPage
	repairDonePage
)

type field struct {
	key   string
	input textinput.Model
}

// The same mutex orders cancellation and the first boot write. Reading a
// cached UI message here would leave a race where Ctrl+C could abandon Arm.
type taskStatus struct {
	mu                sync.Mutex
	label             string
	done, total       int64
	critical, stopped bool
	steps             []taskStep
}

func (s *taskStatus) snapshot() (string, int64, int64, bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.label, s.done, s.total, s.critical
}

func (s *taskStatus) stop() bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.critical {
		return false
	}
	s.stopped = true
	return true
}

func (s *taskStatus) reporter(ctx context.Context) installer.Reporter {
	return func(label string, op func(installer.Progress) error) error {
		s.mu.Lock()
		if s.stopped || (!s.critical && ctx.Err() != nil) {
			s.mu.Unlock()
			return context.Canceled
		}
		s.label, s.done, s.total = label, 0, 0
		index := s.startStep(label)
		if label == "Installing recovery boot files (keep power connected)" || label == "op.boot_cleanup" || label == "op.reboot" || label == "op.direct_write" || label == "op.firmware_write" {
			s.critical = true
		}
		s.mu.Unlock()
		err := op(func(done, total int64) {
			s.mu.Lock()
			s.done, s.total = done, total
			s.mu.Unlock()
		})
		s.mu.Lock()
		s.steps[index].state = stepDone
		if err != nil {
			s.steps[index].state = stepFailed
		}
		s.mu.Unlock()
		return err
	}
}

type jobResult struct {
	kind string
	data any
	err  error
}
type interrupted struct{}

type model struct {
	opts          Options
	deps          services
	ctx           context.Context
	page          page
	menu          list.Model
	fields        []field
	focus         int
	spin          spinner.Model
	bar           progress.Model
	width, height int
	errKey        string
	errArgs       []any
	err           error
	source        string
	image         installer.Image
	asset         *installer.ReleaseAsset
	downloaded    *installer.DownloadedImage
	release       installer.Release
	assets        []installer.ReleaseAsset
	username      string
	password      string
	timezone      string
	network       installer.Network
	imported      bool
	wifi          installer.WiFiSnapshot
	req           installer.Request
	access        installer.AccessInfo
	task          *taskStatus
	taskCancel    context.CancelFunc
	exitAfterJob  bool
	message       string
	cancelled     bool
	pulse         int
	splash        splash
	checks        installer.CheckResult
	checkErr      error
	checkNext     string
	solutionIndex int
	solutionView  solutionView
	repairPlan    *installer.RepairPlan
	repairErr     error
}

func newModel(ctx context.Context, opts Options, deps services) *model {
	s := spinner.New(spinner.WithSpinner(spinner.Dot), spinner.WithStyle(accent))
	m := &model{opts: opts, deps: deps, ctx: ctx, spin: s,
		bar:   progress.New(progress.WithSolidFill(brandBlue), progress.WithoutPercentage(), progress.WithColorProfile(lipgloss.ColorProfile())),
		width: 78, height: 24, source: opts.Image, timezone: "UTC",
		network: installer.Network{Interface: opts.Interface, Hostname: installer.CurrentHostname("/")}}
	m.goPage(splashPage)
	return m
}

// Even commands that resume an installation choose a language
// before starting work. Merely opening the program never changes boot files.
func (m *model) startWizard() tea.Cmd {
	return m.startInstallationWizard()
}

func (m *model) startInstallationWizard() tea.Cmd {
	switch m.opts.Command {
	case "arm":
		deps, d := m.deps, m.opts.Device
		return m.begin("resume", "op.recheck", func(_ context.Context, report installer.Reporter) (any, error) {
			var req installer.Request
			err := report("op.recheck", func(p installer.Progress) error {
				var err error
				req, err = deps.prepared(d, p)
				return err
			})
			return req, err
		})
	default:
		if m.opts.Image != "" || m.opts.Command == "solution" {
			m.goPage(localPage)
			if m.source != "" {
				return m.verifyLocalImage()
			}
		} else {
			m.goPage(sourcePage)
		}
	}
	return textinput.Blink
}

func Run(ctx context.Context, opts Options) error {
	if !term.IsTerminal(int(os.Stdin.Fd())) || !term.IsTerminal(int(os.Stdout.Fd())) {
		return fmt.Errorf("%s", opts.Text.T("error.terminal"))
	}
	ctx, cancel := context.WithCancel(ctx)
	m := newModel(ctx, opts, defaults())
	defer func() {
		cancel()
		m.closeDownload()
	}()
	result, err := tea.NewProgram(m, tea.WithAltScreen(), tea.WithoutSignalHandler()).Run()
	if err != nil {
		return err
	}
	final := result.(*model)
	final.password = ""
	final.fields = nil
	if final.message != "" {
		fmt.Println(final.finalMessage())
	}
	if final.err == nil && final.opts.Command == "solution" {
		if final.checkErr != nil {
			return final.checkErr
		}
		if final.checks.Blocking() {
			return fmt.Errorf("%s", final.opts.Text.T("solutions.blocked"))
		}
	}
	return final.err
}

func (m *model) Init() tea.Cmd {
	watch := func() tea.Msg { <-m.ctx.Done(); return interrupted{} }
	m.splash.started = time.Now()
	return tea.Batch(m.splash.tick(), watch)
}

func (m *model) error(key string, args ...any) { m.errKey, m.errArgs = key, args }

func (m *model) goPage(p page) {
	m.page, m.errKey, m.errArgs = p, "", nil
	m.fields, m.focus = nil, 0
	newField := func(key, value string, secret bool) {
		f := textinput.New()
		f.Prompt, f.CharLimit, f.Width = "", 1024, min(64, max(1, m.width-10))
		f.SetValue(value)
		if secret {
			f.EchoMode, f.EchoCharacter = textinput.EchoPassword, '*'
		}
		m.fields = append(m.fields, field{key, f})
	}
	switch p {
	case localPage:
		newField("local.path", m.source, false)
	case accountPage:
		name := m.username
		if name == "" {
			name = "admin"
		}
		newField("account.username", name, false)
		newField("account.password", m.password, true)
		newField("account.confirm", m.password, true)
	case timezonePage:
		newField("timezone.label", m.timezone, false)
	case networkFormPage:
		if m.network.Mode == "static" {
			newField("network.address", m.network.Address, false)
			newField("network.gateway", m.network.Gateway, false)
		}
		newField("network.dns", strings.Join(m.network.DNS, " "), false)
		newField("network.hostname", m.network.Hostname, false)
	case wifiFormPage:
		newField("wifi.ssid", "", false)
		newField("wifi.password", "", true)
	case confirmPage:
		newField("confirm.label", "", false)
	}
	if len(m.fields) > 0 {
		m.fields[0].input.Focus()
	}
	m.rebuildMenu(0)
	if p == languagePage {
		for i, item := range m.menu.Items() {
			if item.(menuItem).id == m.opts.Text.Language {
				m.menu.Select(i)
				break
			}
		}
	}
}

func (m *model) rebuildMenu(selected int) {
	t := m.opts.Text.T
	var items []list.Item
	add := func(id, title, description string) { items = append(items, menuItem{id, t(title), t(description)}) }
	switch m.page {
	case languagePage:
		add("en", "language.en", "language.en.desc")
		add("zh", "language.zh", "language.zh.desc")
		add("zh-TW", "language.zh-TW", "language.zh-TW.desc")
	case sourcePage:
		add("github", "source.github", "source.github.desc")
		add("local", "source.local", "source.local.desc")
	case issuesPage:
		for i, issue := range m.checks.Issues {
			level := "issue.advisory"
			if issue.Blocking {
				level = "issue.blocking"
			}
			items = append(items, menuItem{fmt.Sprint(i), t(issue.TitleKey), t(level)})
		}
		add("recheck", "solutions.recheck", "solutions.recheck.desc")
		if m.opts.Command != "solution" && m.checkErr == nil && !m.checks.Blocking() {
			add("continue", "solutions.continue", "solutions.continue.desc")
		}
		if m.opts.Command != "arm" {
			add("change-image", "solutions.change_image", "solutions.change_image.desc")
		}
		add("exit", "solutions.exit", "solutions.exit.desc")
	case solutionPage:
		for _, solution := range m.checks.Issues[m.solutionIndex].Solutions {
			if solution.RepairID != "" {
				items = append(items, menuItem{"repair:" + solution.RepairID, t("repair.action", t(solution.TitleKey)), t("repair.action.desc")})
			}
		}
		add("back", "action.back", "solutions.back.desc")
	case repairConfirmPage:
		add("back", "action.back", "repair.back.desc")
		add("apply", "repair.confirm", "repair.confirm.desc")
	case repairDonePage:
		add("recheck", "solutions.recheck", "solutions.recheck.desc")
	case assetsPage:
		for i, a := range m.assets {
			items = append(items, menuItem{fmt.Sprint(i), clean(a.Name), t("assets.desc", float64(a.Size)/installer.MiB)})
		}
	case networkPage:
		add("copy", "network.copy", "network.copy.desc")
		add("dhcp", "network.dhcp", "network.dhcp.desc")
		add("static", "network.static", "network.static.desc")
	case copiedDHCPPage:
		add("dhcp", "network.keep_dhcp", "network.keep_dhcp.desc")
		add("static", "network.keep_ip", "network.keep_ip.desc")
	case wifiPage:
		if len(m.wifi.Networks) > 0 {
			add("copy", "wifi.copy", "wifi.copy.desc")
		}
		if len(m.wifi.Interfaces) > 0 {
			add("manual", "wifi.manual", "wifi.manual.desc")
		}
		add("skip", "wifi.skip", "wifi.skip.desc")
	case reviewPage:
		if installer.DirectInstall(m.opts.Device) {
			add("stage", "review.direct", "review.direct.desc")
		} else {
			add("stage", "review.stage", "review.stage.desc")
		}
		add("back", "action.back", "action.back.desc")
	case preparedPage:
		add("arm", "prepared.arm", "prepared.arm.desc")
		add("exit", "prepared.exit", "prepared.exit.desc")
	case rebootPage:
		// The default action never reboots the device.
		add("later", "reboot.later", "reboot.later.desc")
		if installer.DirectInstall(m.opts.Device) {
			add("now", "reboot.now", "reboot.direct.desc")
		} else {
			add("now", "reboot.now", "reboot.now.desc")
		}
	}
	m.menu = list.New(items, menuDelegate{}, max(24, m.width-6), max(3, min(10, m.height-14, len(items)*3)))
	m.menu.SetFilteringEnabled(false)
	m.menu.SetShowTitle(false)
	m.menu.SetShowStatusBar(false)
	m.menu.SetShowHelp(false)
	m.menu.SetShowPagination(len(items) > 3)
	m.menu.Styles.ActivePaginationDot = m.menu.Styles.ActivePaginationDot.Foreground(lipgloss.Color(brandBlue))
	m.menu.DisableQuitKeybindings()
	m.menu.Select(selected)
}

func (m *model) begin(kind, label string, op func(context.Context, installer.Reporter) (any, error)) tea.Cmd {
	ctx, cancel := context.WithCancel(m.ctx)
	plan := kind
	if kind == "prepare" && m.downloaded != nil {
		plan = "prepare-downloaded"
	}
	m.taskCancel, m.task = cancel, newTaskStatus(plan, label, m.asset != nil)
	if kind == "arm" {
		if machine, err := installer.MachineFor(m.opts.Device.Profile); err == nil {
			if plan, ok := machine.(interface{ RecoverySteps() []string }); ok {
				m.task.steps = nil
				for _, key := range plan.RecoverySteps() {
					m.task.steps = append(m.task.steps, taskStep{key: key})
				}
				m.task.startStep(label)
			}
		}
	}
	m.goPage(busyPage)
	status := m.task
	return func() tea.Msg {
		defer cancel()
		data, err := op(ctx, status.reporter(ctx))
		return jobResult{kind, data, err}
	}
}

func (m *model) exit() tea.Cmd {
	if m.page == busyPage && m.task != nil {
		if !m.task.stop() {
			m.exitAfterJob = true
			return nil
		}
		m.taskCancel()
	}
	m.closeDownload()
	m.password, m.fields = "", nil
	m.cancelled = true
	if m.page == rebootPage || (m.message == "done.armed" || m.message == "done.direct") {
		m.message = m.readyMessage()
	} else if m.page == preparedPage {
		m.message = "done.prepared"
	} else if m.message == "" && m.err == nil {
		m.message = "error.cancelled"
	}
	return tea.Quit
}

func (m *model) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	switch msg := msg.(type) {
	case splashTickMsg:
		if m.page != splashPage || m.cancelled {
			return m, nil
		}
		m.splash.elapsed = msg.at.Sub(m.splash.started)
		if m.splash.elapsed >= splashDuration {
			m.splash.raster = nil
			m.goPage(languagePage)
			return m, tea.Batch(m.spin.Tick, textinput.Blink)
		}
		return m, m.splash.tick()
	case tea.WindowSizeMsg:
		m.width, m.height = msg.Width, msg.Height
		m.menu.SetSize(max(24, m.width-6), max(3, min(10, m.height-14, len(m.menu.Items())*3)))
		m.bar.Width = max(10, min(54, m.width-10))
		for i := range m.fields {
			m.fields[i].input.Width = min(64, max(1, m.width-10))
		}
		if m.page == solutionPage || m.page == repairConfirmPage {
			m.resizeSolution()
		}
	case interrupted:
		return m, m.exit()
	case jobResult:
		return m, m.finishJob(msg)
	case spinner.TickMsg:
		m.pulse++
		var cmd tea.Cmd
		m.spin, cmd = m.spin.Update(msg)
		return m, cmd
	case tea.KeyMsg:
		if m.page == splashPage {
			if msg.Type == tea.KeyCtrlC || msg.Type == tea.KeyEsc {
				return m, m.exit()
			}
			return m, nil
		}
		switch msg.String() {
		case "ctrl+c":
			return m, m.exit()
		case "esc":
			return m, m.back()
		case "enter":
			if len(m.fields) > 0 {
				if m.focus < len(m.fields)-1 {
					return m, m.focusField(m.focus + 1)
				}
				return m, m.submitForm()
			}
			if m.page == finishedPage || m.page == failedPage {
				return m, tea.Quit
			}
			return m, m.selectItem()
		case "tab", "shift+tab", "up", "down":
			if len(m.fields) > 0 {
				step := 1
				if msg.String() == "shift+tab" || msg.String() == "up" {
					step = -1
				}
				return m, m.focusField((m.focus + step + len(m.fields)) % len(m.fields))
			}
		}
	}
	if m.page == solutionPage || m.page == repairConfirmPage {
		if key, ok := msg.(tea.KeyMsg); ok && (key.Type == tea.KeyPgUp || key.Type == tea.KeyPgDown || key.Type == tea.KeyHome || key.Type == tea.KeyEnd) {
			var cmd tea.Cmd
			m.solutionView.viewport, cmd = m.solutionView.viewport.Update(msg)
			return m, cmd
		}
	}
	if len(m.fields) > 0 {
		var cmd tea.Cmd
		m.fields[m.focus].input, cmd = m.fields[m.focus].input.Update(msg)
		return m, cmd
	}
	var cmd tea.Cmd
	m.menu, cmd = m.menu.Update(msg)
	return m, cmd
}

func (m *model) focusField(i int) tea.Cmd {
	m.fields[m.focus].input.Blur()
	m.focus = i
	return m.fields[i].input.Focus()
}

func (m *model) back() tea.Cmd {
	switch m.page {
	case languagePage, preparedPage, finishedPage, failedPage:
		return m.exit()
	case sourcePage:
		m.goPage(languagePage)
	case issuesPage:
		// Escape must never bypass a blocking check.
		return m.exit()
	case solutionPage:
		m.goPage(issuesPage)
	case repairConfirmPage:
		m.repairPlan = nil
		m.goPage(solutionPage)
		m.resizeSolution()
	case repairDonePage:
		return m.startChecks(m.checkNext)
	case localPage, assetsPage:
		if m.opts.Command == "solution" {
			return m.exit()
		}
		m.goPage(sourcePage)
	case accountPage:
		m.password = ""
		m.closeDownload()
		m.goPage(sourcePage)
	case networkPage:
		m.goPage(timezonePage)
	case timezonePage:
		m.goPage(accountPage)
	case copiedDHCPPage, networkFormPage:
		m.goPage(networkPage)
	case wifiPage:
		m.goPage(networkFormPage)
	case wifiFormPage:
		m.goPage(wifiPage)
	case reviewPage:
		if len(m.wifi.Interfaces) > 0 {
			m.goPage(wifiPage)
		} else {
			m.goPage(networkPage)
		}
	case confirmPage:
		if m.opts.Command == "arm" {
			m.goPage(languagePage)
			return nil
		}
		if installer.DirectInstall(m.opts.Device) {
			m.goPage(reviewPage)
		} else {
			m.goPage(preparedPage)
		}
	case rebootPage:
		m.message = m.readyMessage()
		return tea.Quit
	case busyPage:
		return m.exit()
	}
	return nil
}

func (m *model) selectItem() tea.Cmd {
	item, ok := m.menu.SelectedItem().(menuItem)
	if !ok {
		return nil
	}
	deps, opts := m.deps, m.opts
	switch m.page {
	case languagePage:
		m.opts.Text, _ = i18n.New(item.id, os.Getenv)
		return m.startWizard()
	case sourcePage:
		m.image = installer.Image{}
		m.closeDownload()
		if m.asset != nil {
			m.source, m.asset = "", nil
		}
		if item.id == "local" {
			m.goPage(localPage)
			return textinput.Blink
		}
		repo := opts.Catalog[opts.Device.Profile]
		if repo == "" {
			m.error("error.repository", opts.Device.Profile)
			return nil
		}
		return m.begin("github", "op.github", func(ctx context.Context, _ installer.Reporter) (any, error) {
			return deps.fetch(ctx, repo, opts.Tag) // Empty tag resolves to latest, without another prompt.
		})
	case assetsPage:
		a := m.assets[m.menu.Index()]
		m.asset, m.source = &a, ""
		if !m.selectTarget(installer.ReleaseProfile(a.Name)) {
			return nil
		}
		m.closeDownload()
		image := &installer.DownloadedImage{}
		m.downloaded = image
		target := m.opts.Device
		return m.begin("download", "op.download", func(ctx context.Context, report installer.Reporter) (any, error) {
			return image, deps.download(ctx, target, a, image, report)
		})
	case networkPage:
		m.imported = false
		if item.id == "copy" {
			return m.begin("network", "op.network", func(context.Context, installer.Reporter) (any, error) {
				return deps.network("/", opts.Interface)
			})
		}
		m.network = installer.Network{Interface: opts.Interface, Mode: item.id, Hostname: m.network.Hostname, WiFi: m.network.WiFi}
		m.goPage(networkFormPage)
		return textinput.Blink
	case copiedDHCPPage:
		if item.id == "static" {
			current := m.network
			return m.begin("network-static", "op.network", func(context.Context, installer.Reporter) (any, error) {
				return deps.static("/", current)
			})
		}
		m.goPage(networkFormPage)
		return textinput.Blink
	case wifiPage:
		switch item.id {
		case "copy":
			m.network.WiFi = append([]installer.WiFiNetwork(nil), m.wifi.Networks...)
		case "manual":
			m.goPage(wifiFormPage)
			return textinput.Blink
		case "skip":
			m.network.WiFi = nil
		}
		m.goPage(reviewPage)
	case reviewPage:
		if item.id == "back" {
			m.goPage(networkPage)
			return nil
		}
		if installer.DirectInstall(opts.Device) {
			return m.startChecks("confirm")
		}
		username, password, n, source, asset, downloaded := m.username, m.password, m.network, m.source, m.asset, m.downloaded
		system := installer.SystemSettings{Language: m.opts.Text.Language, Timezone: m.timezone}
		m.password = ""
		return m.begin("prepare", "op.prepare", func(ctx context.Context, report installer.Reporter) (any, error) {
			var firstboot []byte
			err := report("op.prepare", func(installer.Progress) error {
				var e error
				firstboot, e = installer.Firstboot(username, password, n, system)
				return e
			})
			password = ""
			if err != nil {
				return nil, err
			}
			defer clear(firstboot)
			if err = ctx.Err(); err != nil {
				return nil, err
			}
			if downloaded != nil {
				return deps.prepareDownloaded(ctx, opts.Device, downloaded, firstboot, report)
			}
			return deps.prepare(ctx, opts.Device, source, asset, firstboot, report)
		})
	case preparedPage:
		if item.id == "exit" {
			m.message = "done.prepared"
			return tea.Quit
		}
		return m.startChecks("confirm")
	case issuesPage:
		return m.selectIssue(item.id)
	case solutionPage:
		if item.id == "back" {
			m.goPage(issuesPage)
			return nil
		}
		if id, ok := strings.CutPrefix(item.id, "repair:"); ok {
			in := m.checkContext()
			return m.begin("repair-prepare", "op.repair_prepare", func(ctx context.Context, _ installer.Reporter) (any, error) {
				return deps.repair(ctx, in, id)
			})
		}
	case repairConfirmPage:
		if item.id == "back" {
			return m.back()
		}
		if item.id == "apply" && m.repairPlan != nil && m.repairPlan.Apply != nil {
			plan := m.repairPlan
			m.repairPlan, m.repairErr = nil, nil
			return m.begin("repair-apply", "op.firmware_write", func(ctx context.Context, report installer.Reporter) (any, error) {
				return nil, plan.Apply(ctx, report)
			})
		}
	case repairDonePage:
		return m.startChecks(m.checkNext)
	case rebootPage:
		if item.id == "later" {
			m.message = m.readyMessage()
			return tea.Quit
		}
		return m.begin("reboot", "op.reboot", func(_ context.Context, report installer.Reporter) (any, error) {
			return nil, report("op.reboot", func(installer.Progress) error { return deps.reboot() })
		})
	}
	return nil
}

func (m *model) submitForm() tea.Cmd {
	value := func(i int) string { return m.fields[i].input.Value() }
	switch m.page {
	case localPage:
		path, err := filepath.Abs(strings.TrimSpace(value(0)))
		s, statErr := os.Stat(path)
		if strings.TrimSpace(value(0)) == "" || err != nil || statErr != nil || !s.Mode().IsRegular() {
			m.error("error.path")
			return nil
		}
		m.source, m.asset = path, nil
		return m.verifyLocalImage()
	case accountPage:
		if key := accountError(value(0), value(1), value(2)); key != "" {
			m.error(key)
			return nil
		}
		m.username, m.password = value(0), value(1)
		m.goPage(timezonePage)
	case timezonePage:
		system, err := (installer.SystemSettings{Language: m.opts.Text.Language, Timezone: strings.TrimSpace(value(0))}).Normalize()
		if err != nil {
			m.error("error.timezone")
			return nil
		}
		m.timezone = system.Timezone
		m.goPage(networkPage)
	case networkFormPage:
		n, offset := m.network, 0
		if n.Mode == "static" {
			n.Address, n.Gateway, offset = strings.TrimSpace(value(0)), strings.TrimSpace(value(1)), 2
		}
		n.DNS, n.Hostname = strings.Fields(value(offset)), strings.TrimSpace(value(offset+1))
		if err := n.Validate(); err != nil {
			m.error("error.details", clean(err.Error()))
			return nil
		}
		m.network = n
		wifi, err := m.deps.wifi("/")
		m.wifi = wifi
		if len(wifi.Interfaces) == 0 && err == nil {
			m.goPage(reviewPage)
		} else {
			m.goPage(wifiPage)
			if err != nil {
				m.error("error.network", clean(err.Error()))
			}
		}
	case wifiFormPage:
		if len(m.wifi.Interfaces) == 0 {
			m.error("error.wifi_interface")
			return nil
		}
		n := m.network
		n.WiFi = []installer.WiFiNetwork{{Interface: m.wifi.Interfaces[0], SSID: value(0), Password: value(1), AutoConnect: true}}
		if err := n.Validate(); err != nil {
			m.error("error.details", clean(err.Error()))
			return nil
		}
		m.network = n
		m.goPage(reviewPage)
	case confirmPage:
		if value(0) != "ERASE "+m.opts.Device.Disk {
			m.error("error.confirm", m.opts.Device.Disk)
			return nil
		}
		deps, d := m.deps, m.opts.Device
		if installer.DirectInstall(d) {
			username, password, n, source, asset := m.username, m.password, m.network, m.source, m.asset
			system := installer.SystemSettings{Language: m.opts.Text.Language, Timezone: m.timezone}
			if m.downloaded != nil {
				asset = nil // Use the verified RAM file; never fetch the release again.
			}
			m.password = ""
			return m.begin("direct", "op.prepare", func(ctx context.Context, report installer.Reporter) (any, error) {
				var firstboot []byte
				err := report("op.prepare", func(installer.Progress) error {
					var e error
					firstboot, e = installer.Firstboot(username, password, n, system)
					return e
				})
				password = ""
				if err != nil {
					return nil, err
				}
				defer clear(firstboot)
				return deps.direct(ctx, d, source, asset, firstboot, report)
			})
		}
		return m.begin("arm", "op.recheck", func(_ context.Context, report installer.Reporter) (any, error) {
			return deps.arm(d, report)
		})
	}
	return textinput.Blink
}

func accountError(username, password, confirmation string) string {
	if len(username) == 0 || len(username) > 64 || strings.TrimSpace(username) != username || strings.ContainsFunc(username, unicode.IsControl) {
		return "error.username"
	}
	if !utf8.ValidString(password) || utf8.RuneCountInString(password) < 8 || strings.ContainsFunc(password, unicode.IsControl) {
		return "error.password"
	}
	if len(password) > 72 {
		return "error.password_long"
	}
	if password != confirmation {
		return "error.password_match"
	}
	return ""
}

func (m *model) finishJob(r jobResult) tea.Cmd {
	if m.cancelled {
		return nil
	}
	if m.task != nil {
		m.task.finish(r.err)
	}
	if m.taskCancel != nil {
		m.taskCancel()
	}
	if r.err != nil {
		switch r.kind {
		case "repair-prepare":
			m.goPage(solutionPage)
			m.error("error.details", clean(r.err.Error()))
			m.resizeSolution()
		case "repair-apply":
			m.repairErr = r.err
			m.goPage(repairDonePage)
		case "check":
			m.checks, _ = r.data.(installer.CheckResult)
			m.checkErr = r.err
			m.goPage(issuesPage)
		case "github":
			m.goPage(sourcePage)
			m.error("error.details", clean(r.err.Error()))
		case "download":
			m.closeDownload()
			m.goPage(assetsPage)
			m.error("error.download", clean(r.err.Error()))
		case "verify":
			m.goPage(localPage)
			m.error("error.details", clean(r.err.Error()))
		case "network", "network-static":
			m.goPage(networkPage)
			m.error("error.network", clean(r.err.Error()))
		default:
			m.goPage(failedPage)
			key := "error.details"
			if r.kind == "prepare" {
				key = "error.staging"
			} else if r.kind == "arm" {
				key = "error.arming"
			}
			m.error(key, clean(r.err.Error()))
			m.err = r.err
		}
	} else {
		switch r.kind {
		case "repair-prepare":
			plan := r.data.(installer.RepairPlan)
			m.repairPlan = &plan
			m.goPage(repairConfirmPage)
			m.resizeSolution()
		case "repair-apply":
			m.repairErr = nil
			m.goPage(repairDonePage)
		case "check":
			m.checks, m.checkErr = r.data.(installer.CheckResult), nil
			if len(m.checks.Issues) > 0 || m.opts.Command == "solution" {
				m.goPage(issuesPage)
			} else {
				return m.afterChecks()
			}
		case "github":
			m.release = r.data.(installer.Release)
			m.assets = installer.CandidateImages(m.release, m.opts.Device.Profile)
			if len(m.assets) == 0 {
				m.goPage(sourcePage)
				m.error("error.assets")
			} else {
				m.goPage(assetsPage)
			}
		case "download":
			m.source = m.downloaded.Source
			m.image = m.downloaded.Image
			return m.startChecks("account")
		case "verify":
			info := r.data.(installer.Image)
			m.goPage(localPage)
			if m.opts.Command == "solution" {
				m.opts.Device.Profile = info.Profile
			} else if !m.selectTarget(info.Profile) {
				return textinput.Blink
			}
			m.image = info
			return m.startChecks("account")
		case "resume":
			m.req = r.data.(installer.Request)
			m.source = filepath.Join(installer.StageDir(m.opts.Device), "image.img")
			m.image = m.req.Image
			return m.startChecks("confirm")
		case "network", "network-static":
			n := r.data.(installer.Network)
			n.WiFi = m.network.WiFi
			m.network, m.imported = n, true
			if r.kind == "network" && m.network.Mode == "dhcp" {
				m.goPage(copiedDHCPPage)
			} else {
				m.goPage(networkFormPage)
			}
			return textinput.Blink
		case "prepare":
			m.req = r.data.(installer.Request)
			m.source = filepath.Join(installer.StageDir(m.opts.Device), "image.img")
			m.image = m.req.Image
			if m.opts.Command == "prepare" {
				m.goPage(finishedPage)
				m.message = "done.prepared"
			} else {
				m.goPage(preparedPage)
			}
		case "arm", "direct":
			if access, ok := r.data.(installer.AccessInfo); ok {
				m.access = access
			}
			m.goPage(rebootPage)
			m.message = m.readyMessage()
		case "reboot":
			m.goPage(finishedPage)
			m.message = "done.reboot"
			return tea.Quit
		}
	}
	if m.exitAfterJob {
		return tea.Quit
	}
	return nil
}

func (m *model) closeDownload() {
	if m.downloaded != nil {
		_ = m.downloaded.Close()
		m.downloaded = nil
	}
}

// Never allow a release filename or low-level diagnostic to emit terminal
// control sequences. Newlines are retained for readable diagnostics.
func clean(s string) string {
	return strings.Map(func(r rune) rune {
		if unicode.IsControl(r) && r != '\n' {
			return -1
		}
		return r
	}, s)
}

func (m *model) verifyLocalImage() tea.Cmd {
	deps, source := m.deps, m.source
	m.image = installer.Image{}
	return m.begin("verify", "op.verify", func(ctx context.Context, report installer.Reporter) (any, error) {
		var info installer.Image
		err := report("op.verify", func(p installer.Progress) error {
			var err error
			info, err = deps.verify(ctx, source, "", 4*1024*installer.MiB, p)
			return err
		})
		return info, err
	})
}
func (m *model) selectTarget(profile string) bool {
	d, err := m.deps.target("/", m.opts.Device, profile)
	if err == nil && m.opts.Command == "prepare" && installer.DirectInstall(d) {
		err = fmt.Errorf("SD direct installation requires the install command")
	}
	if err != nil {
		m.error("error.details", clean(err.Error()))
		return false
	}
	m.opts.Device = d
	return true
}
func (m *model) readyMessage() string {
	if installer.DirectInstall(m.opts.Device) {
		return "done.direct"
	}
	return "done.armed"
}
