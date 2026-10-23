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

type solutionView struct {
	viewport viewport.Model
	focus    solutionFocus
}

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
		if id, ok := strings.CutPrefix(id, "repair:"); ok {
			issue, valid := m.activeSolutionIssue()
			if !valid {
				return nil
			}
			for _, solution := range issue.Solutions {
				if id != "" && solution.RepairID == id {
					in, deps := m.checkContext(), m.deps
					return m.begin("repair-prepare", "op.repair_prepare", func(ctx context.Context, _ installer.Reporter) (any, error) {
						return deps.repair(ctx, in, id)
					})
				}
			}
			return nil
		}
		index, err := strconv.Atoi(id)
		if err == nil && index >= 0 && index < m.solutionFindingCount() {
			if !m.solutionWorkspace() {
				m.goPage(issuesPage)
			}
			m.chooseSolutionIssue(index)
			m.solutionView.focus = solutionDetails
		}
	}
	return nil
}

func (m *model) solutionHeader() string {
	return accent.Render(m.opts.Text.T("app.title") + " · v" + buildinfo.Version)
}

func (m *model) resizeSolution() {
	if m.solutionView.viewport.Width == 0 {
		m.solutionView.viewport = viewport.New(1, 1)
	}
	layout := m.solutionLayout()
	v := &m.solutionView.viewport
	v.Width, v.Height = layout.detailWidth, layout.detailHeight
	offset := v.YOffset
	v.SetContent(lipgloss.NewStyle().Width(v.Width).Render(m.solutionContent()))
	v.SetYOffset(offset)
}

func (m *model) solutionsView() string {
	layout := m.solutionLayout()
	body := m.solutionBody(layout)
	return lipgloss.NewStyle().Margin(1, 2).Render(layout.header + "\n" + body + "\n" + layout.footer)
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
