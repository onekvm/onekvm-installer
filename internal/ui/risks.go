package ui

import (
	"fmt"
	"net/url"
	"strings"

	"github.com/charmbracelet/bubbles/viewport"
	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
	"github.com/charmbracelet/x/ansi"
	"github.com/onekvm/onekvm-installer/internal/buildinfo"
	"github.com/onekvm/onekvm-installer/internal/installer"
)

type riskOption struct {
	id, software string
	points       []string
}

func option(id, software string, count int) riskOption {
	r := riskOption{id: id, software: software}
	for i := 1; i <= count; i++ {
		r.points = append(r.points, fmt.Sprintf("risk.%s.point.%d", id, i))
	}
	return r
}

// Counts and numbered details have one source of truth. These are risk notices,
// including uncertainties, not a count of confirmed vulnerabilities.
var riskOptions = []riskOption{
	option("internet", "iptables / ip6tables (Linux netfilter)", 3),
	option("download", "onekvm-installer (Go net/http); curl (onekvm.sh)", 4),
	option("ntp", "systemd-timesyncd", 4),
	option("dns", "systemd-resolved", 4),
	option("discovery", "", 4),
	option("stun", "onekvm-server (webrtc-rs)", 4),
	option("turn", "onekvm-server (webrtc-rs)", 4),
	option("acme", "onekvm-server (instant-acme / ureq)", 5),
	option("update", "onekvm-server (ureq)", 4),
	option("extensions", "onekvm-extension-host / opkg / wget", 5),
	option("mdns", "systemd-resolved", 3),
	option("cpu", "Linux", 2),
	option("memory", "Linux", 2),
	option("storage", "Linux / onekvm-installer", 2),
	option("linux", "Linux", 3),
}

type riskState struct {
	disabled map[string]bool
	accepted bool
	index    int
	// 0: option toggle, 1: details, 2: next, 3: back.
	focus    int
	start    int
	dns      []string
	viewport viewport.Model
}

type riskGeometry struct {
	width, height, bodyY, bodyHeight, rows, footerY, detailX int
	header                                                   string
}

func riskScreen(p page) bool { return p == riskPage || p == riskDetailsPage }

func (m *model) initRisks() {
	if m.risks.disabled == nil {
		m.risks.disabled = make(map[string]bool)
		m.risks.dns = installer.CurrentDNS("/")
		m.risks.viewport = viewport.New(1, 1)
	}
}

func (m *model) disabledRisks() []string {
	var names []string
	for _, item := range riskOptions {
		if m.risks.disabled[item.id] {
			names = append(names, m.opts.Text.T("risk."+item.id+".title"))
		}
	}
	return names
}

func (m *model) riskLayout() riskGeometry {
	t := m.opts.Text.T
	g := riskGeometry{width: max(1, m.width-4), height: max(1, m.height-2)}
	title, note := t("risk.title"), t("risk.note")
	if m.page == riskDetailsPage {
		title = t("risk." + riskOptions[m.risks.index].id + ".title")
		note = t("risk.count_note")
	}
	g.header = accent.Render("OneKVM Installer · v"+buildinfo.Version) + "\n" +
		lipgloss.NewStyle().Bold(true).Render(ansi.Truncate(title, g.width, "…")) + "\n" +
		muted.Render(ansi.Wrap(note, g.width, "")) + "\n"
	g.bodyY = lipgloss.Height(g.header)
	g.bodyHeight = max(1, g.height-g.bodyY-4)
	g.rows = max(1, g.bodyHeight/2)
	g.footerY = g.bodyY + g.bodyHeight + 1
	button := "[ " + t("risk.details", 5) + " ]"
	g.detailX = max(0, g.width-ansi.StringWidth(button))
	return g
}

func (m *model) resizeRisks() {
	g := m.riskLayout()
	m.risks.viewport.Width, m.risks.viewport.Height = g.width, g.bodyHeight
	m.risks.viewport.SetContent(ansi.Wrap(m.riskContent(), g.width, ""))
	m.keepRiskVisible(g.rows)
}

func (m *model) keepRiskVisible(rows int) {
	m.risks.index = max(0, min(m.risks.index, len(riskOptions)-1))
	if m.risks.index < m.risks.start {
		m.risks.start = m.risks.index
	}
	if m.risks.index >= m.risks.start+rows {
		m.risks.start = m.risks.index - rows + 1
	}
	m.risks.start = max(0, min(m.risks.start, max(0, len(riskOptions)-rows)))
}

func (m *model) riskContent() string {
	t := m.opts.Text.T
	r := riskOptions[m.risks.index]
	software := r.software
	if r.id == "discovery" || r.id == "cpu" || r.id == "memory" || r.id == "storage" || r.id == "linux" {
		software = t("risk." + r.id + ".software")
	} else if r.id == "stun" || r.id == "turn" {
		software += " / " + t("risk.browser_webrtc")
	}
	blocks := []string{t("risk.software", software), t("risk.targets") + "\n" + t("risk."+r.id+".targets")}
	if r.id == "dns" && len(m.risks.dns) > 0 {
		blocks = append(blocks, t("risk.current_dns", strings.Join(m.risks.dns, ", ")))
	}
	if r.id == "download" && m.opts.URL != "" {
		// Signed query strings and embedded credentials must not enter the UI.
		if u, err := url.Parse(m.opts.URL); err == nil && u.Host != "" {
			u.User, u.RawQuery, u.Fragment = nil, "", ""
			blocks = append(blocks, t("risk.image_url", clean(u.String())))
		}
	}
	blocks = append(blocks, t("risk.points", len(r.points)))
	for i, point := range r.points {
		blocks = append(blocks, fmt.Sprintf("%d. %s", i+1, t(point)))
	}
	blocks = append(blocks, t("risk.off"))
	return strings.Join(blocks, "\n\n")
}

func (m *model) riskButton(label string, focused, enabled bool) string {
	button := "[ " + label + " ]"
	if !enabled {
		style := muted
		if focused {
			style = style.Underline(true)
		}
		return style.Render(button)
	}
	if focused {
		return selected.Render(button)
	}
	return accent.Render(button)
}

func (m *model) risksView() string {
	if m.width < 1 || m.height < 1 {
		return ""
	}
	t, g := m.opts.Text.T, m.riskLayout()
	var body string
	if m.page == riskDetailsPage {
		body = m.risks.viewport.View()
	} else {
		var rows []string
		end := min(len(riskOptions), m.risks.start+g.rows)
		for i := m.risks.start; i < end; i++ {
			r := riskOptions[i]
			mark, pointer := "[✓]", "  "
			if m.risks.disabled[r.id] {
				mark = "[ ]"
			}
			if i == m.risks.index && m.risks.focus < 2 {
				pointer = "› "
			}
			left := pointer + mark + " " + t("risk."+r.id+".title")
			button := m.riskButton(t("risk.details", len(r.points)), i == m.risks.index && m.risks.focus == 1, true)
			// Reserve the details button, including its count, before truncating
			// labels. On narrow terminals d/Enter still opens the full details.
			if g.detailX > 8 {
				left = ansi.Truncate(left, g.detailX-1, "…")
				if i == m.risks.index && m.risks.focus == 0 {
					left = accent.Render(left)
				}
				rows = append(rows, left+strings.Repeat(" ", max(1, g.detailX-ansi.StringWidth(left)))+button)
			} else {
				rows = append(rows, ansi.Truncate(left+" "+button, g.width, "…"))
			}
			rows = append(rows, muted.Render(ansi.Truncate("  "+t("risk."+r.id+".summary"), g.width, "…")))
		}
		body = strings.Join(rows, "\n")
	}
	body = lipgloss.NewStyle().Height(g.bodyHeight).Render(body)
	var footer []string
	if m.page == riskDetailsPage {
		footer = []string{m.riskButton(t("action.back"), true, true), muted.Render(t("risk.detail_help")), ""}
	} else {
		disabled := m.disabledRisks()
		status := t("risk.ready")
		if len(disabled) > 0 {
			status = danger.Render(t("risk.blocked", len(disabled)) + " " + strings.Join(disabled, ", "))
		}
		footer = []string{ansi.Truncate(status, g.width, "…"),
			m.riskButton(t("risk.next"), m.risks.focus == 2, len(disabled) == 0) + "  " + m.riskButton(t("action.back"), m.risks.focus == 3, true),
			muted.Render(t("risk.help"))}
	}
	view := g.header + "\n" + body + "\n\n" + strings.Join(footer, "\n")
	// Keep both drawing and mouse hit coordinates inside the physical terminal.
	lines := strings.Split(view, "\n")
	for i := range lines {
		lines[i] = ansi.Truncate(lines[i], g.width, "…")
	}
	if len(lines) > g.height {
		lines = lines[:g.height]
	}
	return lipgloss.NewStyle().Margin(1, 2).Render(strings.Join(lines, "\n"))
}

func (m *model) toggleRisk() {
	r := riskOptions[m.risks.index]
	m.risks.disabled[r.id] = !m.risks.disabled[r.id]
	m.risks.accepted = false
}

func (m *model) acceptRisks() tea.Cmd {
	// This is the transition guard, independent of button styling or the input
	// path (mouse, Enter, shortcut, or a supplied --image/--url).
	if len(m.disabledRisks()) != 0 {
		return nil
	}
	m.risks.accepted = true
	return m.startInstallationWizard()
}

func (m *model) openRiskDetails() {
	m.goPage(riskDetailsPage)
	m.risks.viewport.GotoTop()
}

func (m *model) updateRiskKey(msg tea.KeyMsg) tea.Cmd {
	if msg.String() == "ctrl+c" {
		return m.exit()
	}
	if m.page == riskDetailsPage {
		if msg.String() == "esc" || msg.String() == "enter" {
			m.goPage(riskPage)
			m.risks.focus = 1
			return nil
		}
		var cmd tea.Cmd
		m.risks.viewport, cmd = m.risks.viewport.Update(msg)
		return cmd
	}
	switch msg.String() {
	case "esc":
		m.goPage(languagePage)
	case "tab":
		m.risks.focus = (m.risks.focus + 1) % 4
	case "shift+tab":
		m.risks.focus = (m.risks.focus + 3) % 4
	case "left", "right":
		m.risks.focus ^= 1
	case "up", "k":
		if m.risks.focus >= 2 {
			m.risks.focus = 0
		} else {
			m.risks.index = max(0, m.risks.index-1)
		}
	case "down", "j":
		if m.risks.index == len(riskOptions)-1 || m.risks.focus >= 2 {
			m.risks.focus = 2
		} else {
			m.risks.index++
		}
	case "home":
		m.risks.index, m.risks.focus = 0, 0
	case "end":
		m.risks.index, m.risks.focus = len(riskOptions)-1, 0
	case "pgup", "pgdown":
		step := m.riskLayout().rows
		if msg.String() == "pgup" {
			step = -step
		}
		m.risks.index = max(0, min(len(riskOptions)-1, m.risks.index+step))
		m.risks.focus = 0
	case " ":
		if m.risks.focus < 2 {
			m.toggleRisk()
		}
	case "d":
		if m.risks.focus < 2 {
			m.openRiskDetails()
		}
	case "n":
		return m.acceptRisks()
	case "enter":
		switch m.risks.focus {
		case 0:
			m.toggleRisk()
		case 1:
			m.openRiskDetails()
		case 2:
			return m.acceptRisks()
		case 3:
			m.goPage(languagePage)
		}
	}
	m.keepRiskVisible(m.riskLayout().rows)
	return nil
}

func (m *model) updateRiskMouse(msg tea.MouseMsg) tea.Cmd {
	g := m.riskLayout()
	x, y := msg.X-2, msg.Y-1
	if m.page == riskDetailsPage {
		if msg.Button == tea.MouseButtonLeft && msg.Action == tea.MouseActionPress && y == g.footerY {
			if x >= 0 && x < ansi.StringWidth("[ "+m.opts.Text.T("action.back")+" ]") {
				m.goPage(riskPage)
				m.risks.focus = 1
			}
			return nil
		}
		var cmd tea.Cmd
		m.risks.viewport, cmd = m.risks.viewport.Update(msg)
		return cmd
	}
	if msg.Button == tea.MouseButtonWheelUp || msg.Button == tea.MouseButtonWheelDown {
		step := 1
		if msg.Button == tea.MouseButtonWheelUp {
			step = -1
		}
		m.risks.index = max(0, min(len(riskOptions)-1, m.risks.index+step))
		m.risks.focus = 0
		m.keepRiskVisible(g.rows)
		return nil
	}
	if msg.Button != tea.MouseButtonLeft || msg.Action != tea.MouseActionPress || x < 0 || x >= g.width {
		return nil
	}
	if y >= g.bodyY && y < g.bodyY+g.bodyHeight {
		index := m.risks.start + (y-g.bodyY)/2
		if index >= len(riskOptions) {
			return nil
		}
		m.risks.index = index
		if (y-g.bodyY)%2 == 0 && x >= g.detailX && g.detailX > 8 {
			m.risks.focus = 1
			m.openRiskDetails()
		} else {
			m.risks.focus = 0
			m.toggleRisk()
		}
	} else if y == g.footerY+1 {
		nextWidth := ansi.StringWidth("[ " + m.opts.Text.T("risk.next") + " ]")
		backWidth := ansi.StringWidth("[ " + m.opts.Text.T("action.back") + " ]")
		if x < nextWidth {
			m.risks.focus = 2
			return m.acceptRisks()
		}
		if x >= nextWidth+2 && x < nextWidth+2+backWidth {
			m.goPage(languagePage)
		}
	}
	return nil
}
