package ui

import (
	"fmt"
	"io"
	"strings"

	"github.com/charmbracelet/bubbles/list"
	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
	"github.com/charmbracelet/x/ansi"
	"github.com/onekvm/onekvm-installer/internal/buildinfo"
	"github.com/onekvm/onekvm-installer/internal/installer"
)

// OneKVM's logo and brand assets use this blue.
const brandBlue = "#1677FF"

var (
	accent      = lipgloss.NewStyle().Foreground(lipgloss.Color(brandBlue)).Bold(true)
	muted       = lipgloss.NewStyle().Foreground(lipgloss.Color("#94A3B8"))
	success     = lipgloss.NewStyle().Foreground(lipgloss.Color("#52C41A"))
	danger      = lipgloss.NewStyle().Foreground(lipgloss.Color("#FBBF24"))
	selected    = lipgloss.NewStyle().Foreground(lipgloss.Color("#FFFFFF")).Background(lipgloss.Color(brandBlue)).Bold(true)
	inputBorder = lipgloss.Color("#64748B")
)

// Put the label in the frame so every field remains three terminal rows,
// including four-field network forms in an 80x24 terminal.
func fieldView(f field, label string, focused bool) string {
	color, labelStyle := inputBorder, muted
	if focused {
		color, labelStyle = lipgloss.Color(brandBlue), accent
	}
	frame := lipgloss.NewStyle().Border(lipgloss.RoundedBorder()).BorderForeground(color).Padding(0, 1)
	lines := strings.Split(frame.Render(f.input.View()), "\n")
	inside := lipgloss.Width(lines[0]) - 2
	label = ansi.Truncate(label, max(1, inside-4), "…")
	edge := lipgloss.NewStyle().Foreground(color)
	lines[0] = edge.Render("╭─ ") + labelStyle.Render(label) +
		edge.Render(" "+strings.Repeat("─", max(0, inside-ansi.StringWidth(label)-3))+"╮")
	return strings.Join(lines, "\n")
}

type menuItem struct{ id, title, description string }

func (i menuItem) FilterValue() string { return i.title }

type menuDelegate struct{}

func (menuDelegate) Height() int                         { return 2 }
func (menuDelegate) Spacing() int                        { return 1 }
func (menuDelegate) Update(tea.Msg, *list.Model) tea.Cmd { return nil }
func (menuDelegate) Render(w io.Writer, m list.Model, index int, item list.Item) {
	i := item.(menuItem)
	width := max(12, m.Width()-2)
	title := ansi.Truncate(i.title, width-4, "…")
	desc := ansi.Truncate(i.description, width-4, "…")
	if index == m.Index() {
		fmt.Fprint(w, selected.Width(width).Render("› "+title+"\n  "+desc))
	} else {
		fmt.Fprint(w, "  "+title+"\n"+muted.Render("  "+desc))
	}
}

func (m *model) View() string {
	if m.cancelled {
		return ""
	}
	if m.page == splashPage {
		return m.splash.view(m.width, m.height)
	}
	if m.page == issuesPage || m.page == solutionPage || m.page == repairConfirmPage || m.page == repairDonePage {
		return m.solutionsView()
	}
	t := m.opts.Text.T
	width := max(20, m.width-4)
	d := m.opts.Device
	model := d.HardwareModel
	if model == "" {
		model = d.Model
	}
	media := d.Media
	if media == "SD" {
		media = t("value.sd")
	} else if media == "MMC" {
		media = "eMMC"
	}
	header := accent.Render(t("app.title")+" · v"+buildinfo.Version) + "\n" +
		muted.Render(clean(t("app.device", model, media, float64(d.Bytes)/(1024*installer.MiB))))
	var title, body, hint string
	help := "help.menu"
	switch m.page {
	case languagePage:
		title, help = t("language.title"), "help.language"
	case sourcePage:
		title = t("source.title")
		if !installer.DirectInstall(d) {
			hint = t("app.data", clean(d.DataMount), d.DataType)
		}
	case localPage:
		title = t("local.title")
	case remotePage:
		title = t("remote.title")
	case assetsPage:
		title = t("assets.title")
		hint = clean(m.opts.Catalog[d.Profile] + " · " + m.release.Tag)
	case accountPage:
		title, hint = t("account.title"), t("account.help")
	case timezonePage:
		title, hint = t("timezone.title"), t("timezone.help")
	case networkPage:
		title = t("network.title")
	case copiedDHCPPage:
		title = t("network.copied_dhcp.title")
		hint = danger.Render(t("network.copied_dhcp.help"))
	case networkFormPage:
		mode := t("network." + m.network.Mode)
		title = t("network.form") + " · " + clean(m.network.Interface) + " · " + mode
		if m.imported {
			hint = t("network.copied", mode, m.network.Interface)
		}
	case wifiPage:
		title, hint = t("wifi.title"), t("wifi.help")
		if len(m.wifi.Networks) > 0 {
			var names []string
			for _, n := range m.wifi.Networks {
				names = append(names, n.SSID)
			}
			body = clean(t("wifi.found", len(names), strings.Join(names, ", "))) + "\n\n"
		}
	case wifiFormPage:
		title, hint = t("wifi.manual"), t("wifi.form.help")
	case reviewPage:
		title = t("review.title")
		if installer.DirectInstall(d) {
			title = t("review.direct.title")
		}
		image := m.source
		if m.asset != nil {
			image = m.asset.Name
		}
		hostname := m.network.Hostname
		if hostname == "" {
			hostname = t("value.default")
		}
		body = clean(t("review.summary", image, m.username, m.network.Interface, m.network.Mode, m.network.Address, hostname)) + "\n"
		body += clean(t("timezone.review", m.timezone)) + "\n"
		if len(m.network.WiFi) > 0 {
			var names []string
			for _, n := range m.network.WiFi {
				names = append(names, n.Interface+" · "+n.SSID)
			}
			body += clean(t("wifi.review", strings.Join(names, ", "))) + "\n\n"
		}
		hint = t("review.help")
		body += clean(t("review.target", d.Disk, media, float64(d.Bytes)/(1024*installer.MiB))) + "\n\n"
		if installer.DirectInstall(d) {
			hint = t("review.direct.help")
		}
	case preparedPage:
		title = t("prepared.title")
		build := t("prepared.build", m.req.Image.BuildID)
		if version, revision, ok := strings.Cut(m.req.Image.BuildID, "-"+m.req.Image.Machine+"-"); ok && version != "" && revision != "" {
			build = "OneKVM " + version + " · " + revision
		}
		hint = clean(t("prepared.summary", build, model, media, float64(m.req.ImageBytes)/installer.MiB))
	case confirmPage:
		title = t("confirm.title")
		hint = danger.Render(t("confirm.warning", d.Disk, media, float64(d.Bytes)/(1024*installer.MiB)))
		body = t("confirm.note") + "\n\n"
		if installer.DirectInstall(d) {
			body = t("confirm.direct.note") + "\n\n"
		}
	case rebootPage:
		title = t("armed.title")
		hint = t("armed.summary")
		if installer.DirectInstall(d) {
			title = t("direct.title")
			hint = t("direct.summary")
		}
		if access := m.accessMessage(); access != "" {
			hint += "\n\n" + access
		}
	case busyPage:
		title, help = t("busy.title"), ""
		if m.task != nil {
			_, done, total, critical := m.task.snapshot()
			body = m.stepsView() + "\n\n"
			if total > 0 {
				p := max(0, min(1, float64(done)/float64(total)))
				body += m.bar.ViewAs(p) + fmt.Sprintf(" %3.0f%%", p*100)
			} else {
				body += m.indeterminateBar()
			}
			if done > 0 {
				body += fmt.Sprintf("\n%.1f MiB", float64(done)/installer.MiB)
				if total > 0 {
					body += fmt.Sprintf(" / %.1f MiB", float64(total)/installer.MiB)
				}
			}
			if critical {
				help = "help.arm"
			}
			if m.exitAfterJob {
				body += "\n\n" + t("busy.stopping")
			}
		}
	case finishedPage:
		title, body, help = t("done.title"), m.finalMessage(), "help.finish"
	case failedPage:
		title, help = t("error.title"), "help.finish"
		if m.task != nil {
			body = m.stepsView()
		}
	}
	if len(m.fields) > 0 {
		help = "help.form"
		for i, f := range m.fields {
			label := t(f.key)
			if f.key == "confirm.label" {
				label = t(f.key, d.Disk)
			}
			if i > 0 {
				body += "\n"
			}
			body += fieldView(f, label, i == m.focus)
		}
	} else if m.page != busyPage && m.page != failedPage && m.page != finishedPage {
		body += m.menu.View()
	}
	var blocks []string
	blocks = append(blocks, header, lipgloss.NewStyle().Bold(true).Render(title))
	if hint != "" {
		blocks = append(blocks, muted.Render(hint))
	}
	blocks = append(blocks, body)
	if m.errKey != "" {
		args := append([]any(nil), m.errArgs...)
		for i, arg := range args {
			if s, ok := arg.(string); ok {
				args[i] = m.opts.Text.Diagnostic(s)
			}
		}
		blocks = append(blocks, danger.Render(t(m.errKey, args...)))
	}
	if help != "" {
		blocks = append(blocks, muted.Render(t(help)))
	}
	return lipgloss.NewStyle().Width(width).Margin(1, 2).Render(strings.Join(blocks, "\n\n"))
}
