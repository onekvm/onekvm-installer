package ui

import (
	"fmt"
	"strings"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
	"github.com/charmbracelet/x/ansi"
	"github.com/onekvm/onekvm-installer/internal/installer"
)

type solutionFocus uint8

const (
	solutionIssues solutionFocus = iota
	solutionDetails
	solutionActions
)

var (
	solutionBlue = lipgloss.NewStyle().Foreground(lipgloss.Color("#83C6FF"))
	solutionWarn = lipgloss.NewStyle().Foreground(lipgloss.Color("#EABD72"))
	solutionFail = lipgloss.NewStyle().Foreground(lipgloss.Color("#FF9D9D"))
	solutionOK   = lipgloss.NewStyle().Foreground(lipgloss.Color("#8BD5B3"))
	solutionLine = lipgloss.NewStyle().Foreground(lipgloss.Color("#36414E"))
	solutionPick = solutionBlue.Background(lipgloss.Color("#172D42")).Bold(true)
	solutionPush = lipgloss.NewStyle().Foreground(lipgloss.Color("#102235")).Background(lipgloss.Color("#83C6FF")).Bold(true)
)

func solutionScreen(p page) bool {
	return p == issuesPage || p == repairConfirmPage || p == repairDonePage
}

func (m *model) solutionWorkspace() bool { return m.page == issuesPage }

func (m *model) solutionDiagnostic() string {
	if m.checkErr != nil {
		return clean(m.opts.Text.Diagnostic(m.checkErr.Error()))
	}
	var errors []string
	for _, check := range m.checks.Checks {
		if check.Error != "" {
			errors = append(errors, clean(check.ID+": "+m.opts.Text.Diagnostic(check.Error)))
		}
	}
	return strings.Join(errors, "\n")
}

func (m *model) solutionFindingCount() int {
	n := len(m.checks.Issues)
	if m.solutionDiagnostic() != "" {
		n++
	}
	return n
}

func (m *model) activeSolutionIssue() (installer.Issue, bool) {
	if m.solutionIndex >= 0 && m.solutionIndex < len(m.checks.Issues) {
		return m.checks.Issues[m.solutionIndex], true
	}
	return installer.Issue{}, false
}

func (m *model) chooseSolutionIssue(index int) {
	m.solutionIndex = max(0, min(index, m.solutionFindingCount()-1))
	m.solutionView.viewport.GotoTop()
	m.rebuildMenu(0)
	m.resizeSolution()
}

func (m *model) solutionFinding(index int) (string, string, lipgloss.Style) {
	t := m.opts.Text.T
	if index >= len(m.checks.Issues) {
		return t("solutions.check_failed"), t("solutions.level.error"), solutionFail
	}
	issue := m.checks.Issues[index]
	if issue.Blocking {
		return t(issue.TitleKey), t("solutions.level.blocking"), solutionFail
	}
	return t(issue.TitleKey), t("solutions.level.advisory"), solutionWarn
}

func (m *model) solutionSummary() string {
	t := m.opts.Text.T
	if m.page == repairConfirmPage {
		return solutionWarn.Render(t("solutions.repair.confirm.note"))
	}
	if m.page == repairDonePage {
		if m.repairErr != nil {
			return solutionFail.Render(t("repair.failed"))
		}
		return solutionOK.Render(t("solutions.repair.complete"))
	}
	if m.solutionDiagnostic() != "" {
		return solutionFail.Render("× " + t("solutions.summary.failed"))
	}
	blocking := 0
	for _, issue := range m.checks.Issues {
		if issue.Blocking {
			blocking++
		}
	}
	if blocking > 0 {
		return solutionFail.Render("× " + t("solutions.summary.blocking", blocking))
	}
	if len(m.checks.Issues) > 0 {
		return solutionWarn.Render("! " + t("solutions.summary.advisory", len(m.checks.Issues)))
	}
	if m.checks.Supported {
		return solutionOK.Render("✓ " + t("solutions.clear"))
	}
	return muted.Render(t("solutions.unsupported"))
}

func (m *model) solutionContent() string {
	t := m.opts.Text.T
	var blocks []string
	if m.errKey != "" {
		blocks = append(blocks, solutionFail.Render(t(m.errKey, m.errArgs...)))
	}
	switch {
	case m.page == repairConfirmPage && m.repairPlan != nil:
		blocks = append(blocks, t(m.repairPlan.SummaryKey))
		for _, fact := range m.repairPlan.Facts {
			blocks = append(blocks, solutionBlue.Bold(true).Render(t(fact.LabelKey))+"\n"+clean(fact.Value))
		}
	case m.page == repairDonePage:
		if m.repairErr != nil {
			blocks = append(blocks, solutionFail.Render(t("repair.failed")), clean(m.opts.Text.Diagnostic(m.repairErr.Error())))
		} else {
			blocks = append(blocks, t("repair.done"))
		}
	case m.solutionFindingCount() == 0:
		key := "solutions.unsupported"
		if m.checks.Supported {
			key = "solutions.clear"
		}
		blocks = append(blocks, t(key))
	default:
		if issue, ok := m.activeSolutionIssue(); ok {
			blocks = append(blocks, lipgloss.NewStyle().Bold(true).Render(t(issue.TitleKey)), t(issue.DescriptionKey))
			for _, solution := range issue.Solutions {
				blocks = append(blocks, solutionBlue.Bold(true).Render(t(solution.TitleKey))+"\n"+t(solution.BodyKey))
			}
		} else {
			blocks = append(blocks, solutionFail.Bold(true).Render(t("solutions.check_failed")), m.solutionDiagnostic(), t("solutions.check_failed.note"))
		}
	}
	return strings.Join(blocks, "\n\n")
}

type solutionHit struct{ x, y, width, index int }

// Keep the action bar within two rows, including at 48 columns. Additional
// repair actions remain reachable by arrow keys on subsequent groups of six.
func (m *model) solutionMenu(width int) (string, []solutionHit) {
	items := m.menu.Items()
	start := m.menu.Index() / 6 * 6
	end := min(len(items), start+6)
	rows, hits := []string{""}, []solutionHit{}
	x, y := 0, 0
	buttonWidth := max(8, (width-4)/3)
	for i := start; i < end; i++ {
		item := items[i].(menuItem)
		label := "[ " + ansi.Truncate(item.title, max(1, buttonWidth-4), "…") + " ]"
		n := ansi.StringWidth(label)
		if x > 0 && x+n > width {
			rows = append(rows, "")
			x, y = 0, y+1
		}
		style := lipgloss.NewStyle()
		if m.solutionView.focus == solutionActions && i == m.menu.Index() {
			style = solutionPush
		}
		rows[y] += style.Render(label)
		hits = append(hits, solutionHit{x, y, n, i})
		rows[y] += "  "
		x += n + 2
	}
	if m.solutionWorkspace() && m.opts.Command != "solution" && (m.checkErr != nil || m.checks.Blocking()) {
		label := "[ " + ansi.Truncate(m.opts.Text.T("solutions.continue"), max(1, buttonWidth-4), "…") + " ]"
		if x+ansi.StringWidth(label) > width {
			rows = append(rows, "")
			y++
		}
		if y < 2 {
			rows[y] += muted.Render(label)
		}
	}
	for i := range rows {
		rows[i] = strings.TrimRight(rows[i], " ")
	}
	return strings.Join(rows, "\n"), hits
}

type solutionGeometry struct {
	width, bodyHeight, detailWidth, detailHeight, listWidth int
	labelRows, issueRows                                    int
	header, footer                                          string
	hits                                                    []solutionHit
}

func (m *model) solutionLayout() solutionGeometry {
	t := m.opts.Text.T
	g := solutionGeometry{width: max(12, m.width-4)}
	title := t("solutions.title")
	if m.page == repairConfirmPage {
		title = t("repair.review")
	}
	summary := strings.Split(ansi.Wrap(m.solutionSummary(), g.width, ""), "\n")
	if len(summary) > 2 {
		summary = summary[:2]
		summary[1] = ansi.Truncate(summary[1], g.width, "…")
	}
	g.header = m.solutionHeader() + "\n\n" + lipgloss.NewStyle().Bold(true).Render(ansi.Truncate(title, g.width, "…")) + "\n" + strings.Join(summary, "\n")
	g.header += "\n" + solutionLine.Render(strings.Repeat("─", g.width))
	menu, hits := m.solutionMenu(g.width)
	g.hits = hits
	hint := ""
	if item, ok := m.menu.SelectedItem().(menuItem); ok {
		hint = ansi.Truncate(item.title+" · "+item.description, g.width, "…")
		if len(m.menu.Items()) > 6 {
			hint = ansi.Truncate(fmt.Sprintf("‹ %d/%d › ", m.menu.Index()+1, len(m.menu.Items()))+item.title, g.width, "…")
		}
	}
	helpKey := "solutions.help"
	if m.page == repairConfirmPage {
		helpKey = "solutions.help.review"
	}
	help := ansi.Wrap(t(helpKey), g.width, "")
	g.footer = solutionLine.Render(strings.Repeat("─", g.width)) + "\n" + menu + "\n" + muted.Render(hint) + "\n" + muted.Render(help)
	available := max(1, m.height-2)
	if lipgloss.Height(g.header)+lipgloss.Height(g.footer) >= available {
		g.header = strings.Replace(g.header, "\n\n", "\n", 1)
		g.footer = solutionLine.Render(strings.Repeat("─", g.width)) + "\n" + menu + "\n" + muted.Render(ansi.Truncate(t("solutions.help.compact"), g.width, ""))
	}
	g.bodyHeight = max(1, available-lipgloss.Height(g.header)-lipgloss.Height(g.footer))
	if m.solutionWorkspace() && m.solutionFindingCount() > 0 {
		if m.width >= 104 {
			g.listWidth = 28
		} else if g.bodyHeight >= 3 {
			g.issueRows = 1
		}
	}
	if g.bodyHeight-g.issueRows >= 2 {
		g.labelRows = 1
	}
	g.detailWidth = g.width
	if g.listWidth > 0 {
		g.detailWidth -= g.listWidth + 3
	}
	g.detailHeight = max(1, g.bodyHeight-g.labelRows-g.issueRows)
	return g
}

func solutionFit(text string, width, height int) string {
	lines := strings.Split(text, "\n")
	for len(lines) < height {
		lines = append(lines, "")
	}
	lines = lines[:height]
	for i, line := range lines {
		line = ansi.Truncate(line, width, "")
		lines[i] = line + strings.Repeat(" ", max(0, width-ansi.StringWidth(line)))
	}
	return strings.Join(lines, "\n")
}

func (m *model) solutionBody(g solutionGeometry) string {
	t := m.opts.Text.T
	label := t("solutions.detail")
	if m.solutionView.focus == solutionDetails {
		label = solutionBlue.Render("› " + label)
	} else {
		label = muted.Render(label)
	}
	v := m.solutionView.viewport
	if v.TotalLineCount() > v.Height {
		position := t("solutions.scroll", v.YOffset+1, min(v.TotalLineCount(), v.YOffset+v.Height), v.TotalLineCount())
		if lipgloss.Width(label)+ansi.StringWidth(position)+2 <= g.detailWidth {
			label += strings.Repeat(" ", g.detailWidth-lipgloss.Width(label)-ansi.StringWidth(position)) + muted.Render(position)
		}
	}
	var right []string
	if g.issueRows > 0 {
		title, _, _ := m.solutionFinding(m.solutionIndex)
		count := fmt.Sprintf(" %d/%d", m.solutionIndex+1, m.solutionFindingCount())
		right = append(right, solutionPick.Render(ansi.Truncate("› "+title, g.width-ansi.StringWidth(count), "…")+count))
	}
	if g.labelRows > 0 {
		right = append(right, label)
	}
	right = append(right, v.View())
	content := solutionFit(strings.Join(right, "\n"), g.detailWidth, g.bodyHeight)
	if g.listWidth == 0 {
		return content
	}
	left := []string{muted.Render(t("solutions.results"))}
	if m.solutionView.focus == solutionIssues {
		left[0] = solutionBlue.Render("› " + t("solutions.results"))
	}
	visible := max(1, (g.bodyHeight-1)/2)
	start := max(0, m.solutionIndex-visible+1)
	for i := start; i < min(m.solutionFindingCount(), start+visible); i++ {
		title, level, style := m.solutionFinding(i)
		title = ansi.Truncate(title, g.listWidth-2, "…")
		if i == m.solutionIndex {
			left = append(left, solutionPick.Width(g.listWidth).Render("› "+title))
		} else {
			left = append(left, "  "+title)
		}
		left = append(left, style.Render("  "+ansi.Truncate(level, g.listWidth-2, "…")))
	}
	separator := strings.Repeat(solutionLine.Render(" │ ")+"\n", g.bodyHeight)
	return lipgloss.JoinHorizontal(lipgloss.Top, solutionFit(strings.Join(left, "\n"), g.listWidth, g.bodyHeight), strings.TrimSuffix(separator, "\n"), content)
}

func (m *model) updateSolutionKey(msg tea.KeyMsg) tea.Cmd {
	switch msg.String() {
	case "ctrl+c":
		return m.exit()
	case "esc":
		if m.solutionWorkspace() && m.solutionFindingCount() > 0 && m.solutionView.focus != solutionIssues {
			m.solutionView.focus = solutionIssues
			return nil
		}
		return m.back()
	case "tab", "shift+tab":
		groups := []solutionFocus{solutionDetails, solutionActions}
		if m.solutionWorkspace() && m.solutionFindingCount() > 0 {
			groups = append([]solutionFocus{solutionIssues}, groups...)
		}
		for i, group := range groups {
			if group == m.solutionView.focus {
				delta := 1
				if msg.String() == "shift+tab" {
					delta = len(groups) - 1
				}
				m.solutionView.focus = groups[(i+delta)%len(groups)]
				break
			}
		}
	case "enter":
		switch m.solutionView.focus {
		case solutionIssues:
			m.solutionView.focus = solutionDetails
		case solutionDetails:
			m.solutionView.focus = solutionActions
		case solutionActions:
			return m.selectItem()
		}
	case "r", "R":
		if m.solutionWorkspace() || m.page == repairDonePage {
			return m.startChecks(m.checkNext)
		}
	case "c", "C":
		if m.solutionWorkspace() {
			return m.selectIssue("continue")
		}
	case "up", "down", "left", "right":
		delta := 1
		if msg.String() == "up" || msg.String() == "left" {
			delta = -1
		}
		switch m.solutionView.focus {
		case solutionIssues:
			m.chooseSolutionIssue(m.solutionIndex + delta)
		case solutionDetails:
			if delta < 0 {
				m.solutionView.viewport.LineUp(1)
			} else {
				m.solutionView.viewport.LineDown(1)
			}
		case solutionActions:
			if n := len(m.menu.Items()); n > 0 {
				m.menu.Select((m.menu.Index() + delta + n) % n)
				m.resizeSolution()
			}
		}
	case "home":
		m.solutionView.focus = solutionDetails
		m.solutionView.viewport.GotoTop()
	case "end":
		m.solutionView.focus = solutionDetails
		m.solutionView.viewport.GotoBottom()
	case "pgup", "pgdown":
		m.solutionView.focus = solutionDetails
		var cmd tea.Cmd
		m.solutionView.viewport, cmd = m.solutionView.viewport.Update(msg)
		return cmd
	}
	return nil
}

func (m *model) updateSolutionMouse(msg tea.MouseMsg) tea.Cmd {
	g := m.solutionLayout()
	x, y := msg.X-2, msg.Y-1
	bodyY := lipgloss.Height(g.header)
	if msg.Button == tea.MouseButtonWheelUp || msg.Button == tea.MouseButtonWheelDown {
		delta := 1
		if msg.Button == tea.MouseButtonWheelUp {
			delta = -1
		}
		if g.listWidth > 0 && x < g.listWidth && y >= bodyY && y < bodyY+g.bodyHeight {
			m.chooseSolutionIssue(m.solutionIndex + delta)
			m.solutionView.focus = solutionIssues
			return nil
		}
		m.solutionView.focus = solutionDetails
		var cmd tea.Cmd
		m.solutionView.viewport, cmd = m.solutionView.viewport.Update(msg)
		return cmd
	}
	if msg.Button != tea.MouseButtonLeft || msg.Action != tea.MouseActionPress {
		return nil
	}
	menuY := bodyY + g.bodyHeight + 1
	for _, hit := range g.hits {
		if y == menuY+hit.y && x >= hit.x && x < hit.x+hit.width {
			m.solutionView.focus = solutionActions
			m.menu.Select(hit.index)
			return m.selectItem()
		}
	}
	if y >= bodyY && y < bodyY+g.bodyHeight {
		if g.listWidth > 0 && x < g.listWidth {
			visible := max(1, (g.bodyHeight-1)/2)
			start := max(0, m.solutionIndex-visible+1)
			if y > bodyY {
				m.chooseSolutionIssue(start + (y-bodyY-1)/2)
			}
			m.solutionView.focus = solutionIssues
		} else if g.issueRows > 0 && y == bodyY {
			m.solutionView.focus = solutionIssues
		} else {
			m.solutionView.focus = solutionDetails
		}
	}
	return nil
}
