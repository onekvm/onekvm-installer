package ui

import (
	"context"
	"fmt"
	"strconv"
	"strings"

	"github.com/charmbracelet/bubbles/textinput"
	"github.com/charmbracelet/bubbles/viewport"
	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
	"github.com/onekvm/onekvm-installer/internal/buildinfo"
	"github.com/onekvm/onekvm-installer/internal/installer"
)

type solutionView struct{ viewport viewport.Model }

func (m *model) startChecks(next string) tea.Cmd {
	m.checkNext = next
	in := m.checkContext()
	deps := m.deps
	return m.begin("check", "op.check", func(ctx context.Context, _ installer.Reporter) (any, error) {
		return deps.check(ctx, in)
	})
}

func (m *model) checkContext() installer.CheckContext {
	phase := installer.CheckImageReady
	if m.checkNext == "confirm" {
		phase = installer.CheckBeforeInstall
	}
	return installer.CheckContext{Root: "/", Phase: phase, Target: m.opts.Device, Image: m.image, ImagePath: m.source}
}

func (m *model) afterChecks() tea.Cmd {
	if m.checkErr != nil || m.checks.Blocking() {
		m.goPage(issuesPage)
		return nil
	}
	switch m.checkNext {
	case "confirm":
		m.goPage(confirmPage)
		return textinput.Blink
	case "account":
		m.goPage(accountPage)
		return textinput.Blink
	default:
		return m.startInstallationWizard()
	}
}

func (m *model) selectIssue(id string) tea.Cmd {
	switch id {
	case "recheck":
		return m.startChecks(m.checkNext)
	case "continue":
		// Also guard the action itself, not just the visibility of the menu item.
		if m.opts.Command != "solution" && m.checkErr == nil && !m.checks.Blocking() {
			return m.afterChecks()
		}
	case "exit":
		return m.exit()
	case "change-image":
		if m.opts.Command != "arm" {
			m.closeDownload()
			m.source, m.opts.Image, m.asset, m.password = "", "", nil, ""
			m.image, m.checks, m.checkErr = installer.Image{}, installer.CheckResult{}, nil
			if m.opts.Command == "solution" {
				m.goPage(localPage)
				return textinput.Blink
			}
			m.goPage(sourcePage)
		}
	default:
		index, err := strconv.Atoi(id)
		if err == nil && index >= 0 && index < len(m.checks.Issues) {
			m.solutionIndex = index
			m.goPage(solutionPage)
			m.solutionView.viewport = viewport.New(1, 1)
			m.resizeSolution()
		}
	}
	return nil
}

func (m *model) solutionHeader() string {
	return accent.Render(m.opts.Text.T("app.title") + " · v" + buildinfo.Version)
}

func (m *model) resizeSolution() {
	issue := m.checks.Issues[m.solutionIndex]
	t := m.opts.Text.T
	width := max(12, m.width-4)
	wrap := lipgloss.NewStyle().Width(width)
	titleKey := issue.TitleKey
	if m.page == repairConfirmPage {
		titleKey = "repair.review"
	}
	title := wrap.Bold(true).Render(t(titleKey))
	help := wrap.Render(t("solutions.help"))
	m.solutionView.viewport.Width = width
	m.solutionView.viewport.Height = max(1, m.height-lipgloss.Height(m.solutionHeader())-lipgloss.Height(title)-lipgloss.Height(help)-lipgloss.Height(m.menu.View())-10)
	content := t(issue.DescriptionKey)
	for _, solution := range issue.Solutions {
		content += "\n\n" + t(solution.TitleKey) + "\n" + t(solution.BodyKey)
	}
	if m.page == repairConfirmPage && m.repairPlan != nil {
		content = t(m.repairPlan.SummaryKey)
		for _, fact := range m.repairPlan.Facts {
			content += "\n\n" + t(fact.LabelKey) + ": " + fact.Value
		}
	}
	content = wrap.Render(content)
	offset := m.solutionView.viewport.YOffset
	m.solutionView.viewport.SetContent(content)
	m.solutionView.viewport.SetYOffset(offset)
}

func (m *model) solutionsView() string {
	t := m.opts.Text.T
	width := max(12, m.width-4)
	wrap := lipgloss.NewStyle().Width(width)
	blocks := []string{m.solutionHeader()}
	if m.page == solutionPage || m.page == repairConfirmPage {
		issue := m.checks.Issues[m.solutionIndex]
		title := t(issue.TitleKey)
		if m.page == repairConfirmPage {
			title = t("repair.review")
		}
		blocks = append(blocks, wrap.Bold(true).Render(title), m.solutionView.viewport.View())
		if m.errKey != "" {
			blocks = append(blocks, danger.Render(wrap.Render(t(m.errKey, m.errArgs...))))
		}
		blocks = append(blocks, m.menu.View(), muted.Render(wrap.Render(t("solutions.help"))))
	} else if m.page == repairDonePage {
		message := t("repair.done")
		if m.repairErr != nil {
			message = t("repair.failed") + "\n" + clean(m.repairErr.Error())
		}
		blocks = append(blocks, wrap.Bold(true).Render(t("solutions.title")), wrap.Render(message), m.menu.View(), muted.Render(t("help.menu")))
	} else {
		blocks = append(blocks, wrap.Bold(true).Render(t("solutions.title")))
		message := "solutions.found"
		switch {
		case m.checkErr != nil:
			blocks = append(blocks, danger.Render(wrap.Render(t("solutions.check_failed")+"\n"+clean(m.opts.Text.Diagnostic(m.checkErr.Error())))))
			message = "solutions.check_failed.note"
		case m.checks.Blocking():
			message = "solutions.blocked"
		case len(m.checks.Issues) == 0 && m.checks.Supported:
			message = "solutions.clear"
		case len(m.checks.Issues) == 0:
			message = "solutions.unsupported"
		}
		blocks = append(blocks, muted.Render(wrap.Render(t(message))), m.menu.View(), muted.Render(t("help.menu")))
	}
	return lipgloss.NewStyle().Width(width).Margin(1, 2).Render(strings.Join(blocks, "\n\n"))
}

// Plain output is useful for a diagnostic command over SSH without a TTY.
func WriteSolutions(report installer.CheckResult, t func(string, ...any) string) string {
	var out strings.Builder
	fmt.Fprintln(&out, t("solutions.title"))
	failed := false
	for _, check := range report.Checks {
		if check.Error != "" {
			failed = true
			fmt.Fprintf(&out, "%s\n%s: %s\n", t("solutions.check_failed"), check.ID, clean(check.Error))
		}
	}
	if len(report.Issues) == 0 && !failed {
		key := "solutions.unsupported"
		if report.Supported {
			key = "solutions.clear"
		}
		fmt.Fprintln(&out, t(key))
	}
	for _, issue := range report.Issues {
		level := "issue.advisory"
		if issue.Blocking {
			level = "issue.blocking"
		}
		fmt.Fprintf(&out, "\n%s · %s\n%s\n", t(issue.TitleKey), t(level), t(issue.DescriptionKey))
		for _, solution := range issue.Solutions {
			fmt.Fprintf(&out, "\n%s\n%s\n", t(solution.TitleKey), t(solution.BodyKey))
		}
	}
	return out.String()
}
