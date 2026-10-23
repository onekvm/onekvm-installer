package ui

import (
	"context"
	"fmt"
	"os"
	"strings"
	"testing"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
	"github.com/charmbracelet/x/ansi"
	"github.com/onekvm/onekvm-installer/internal/i18n"
	"github.com/onekvm/onekvm-installer/internal/installer"
)

func TestSolutionsFitTerminalAndShowGuidanceImmediately(t *testing.T) {
	for _, language := range []string{"en", "zh", "zh-TW"} {
		for _, size := range [][2]int{{48, 18}, {80, 24}, {100, 30}, {120, 36}} {
			for _, mode := range []string{"advisory", "blocking", "failed", "clear", "unsupported", "review", "repaired"} {
				t.Run(fmt.Sprintf("%s/%dx%d/%s", language, size[0], size[1], mode), func(t *testing.T) {
					m := testModel(t)
					m.opts.Text, _ = i18n.New(language, os.Getenv)
					m.checks = installer.CheckResult{Supported: true, Issues: []installer.Issue{guideIssue(mode == "blocking")}}
					p := issuesPage
					switch mode {
					case "failed":
						m.checks.Checks = []installer.CheckOutcome{{ID: "boot", Error: "cannot read startup media"}}
					case "clear", "unsupported":
						m.checks = installer.CheckResult{Supported: mode == "clear"}
					case "review":
						p = repairConfirmPage
						m.repairPlan = &installer.RepairPlan{SummaryKey: "repair.pro.spl.summary", Facts: []installer.RepairFact{{LabelKey: "repair.target", Value: strings.Repeat("a", 64)}}}
					case "repaired":
						p = repairDonePage
					}
					m.width, m.height = size[0], size[1]
					m.goPage(p)
					view := m.View()
					if lipgloss.Width(view) > m.width || lipgloss.Height(view) > m.height {
						t.Fatalf("layout exceeds terminal: %dx%d > %dx%d\n%s", lipgloss.Width(view), lipgloss.Height(view), m.width, m.height, ansi.Strip(view))
					}
					if mode == "advisory" && size[0] >= 80 && !strings.Contains(ansi.Strip(view), m.opts.Text.T(m.checks.Issues[0].TitleKey)) {
						t.Fatal("selected issue is not immediately readable", ansi.Strip(view))
					}
					if mode == "clear" || mode == "unsupported" {
						key := "solutions." + mode
						if !strings.Contains(m.solutionContent(), m.opts.Text.T(key)) {
							t.Fatal("unsupported checks were reported as passed")
						}
					}
					key(m, tea.KeyEnd)
					if !m.solutionView.viewport.AtBottom() {
						t.Fatal("End did not reach the final guide or fact")
					}
					key(m, tea.KeyHome)
					if !m.solutionView.viewport.AtTop() {
						t.Fatal("Home did not return to the beginning")
					}
				})
			}
		}
	}
}

func TestSolutionFocusAndRepairFollowSelectedIssue(t *testing.T) {
	m := testModel(t)
	first, second := guideIssue(false), guideIssue(true)
	first.Solutions[0].RepairID, second.Solutions[0].RepairID = "first", "second"
	m.checks = installer.CheckResult{Supported: true, Issues: []installer.Issue{first, second}}
	m.goPage(issuesPage)
	key(m, tea.KeyTab)
	if m.solutionView.focus != solutionIssues {
		t.Fatal("Tab did not enter the issue list")
	}
	key(m, tea.KeyDown)
	if m.solutionIndex != 1 || m.menu.Items()[0].(menuItem).id != "repair:second" {
		t.Fatal("repair actions did not follow the selected issue")
	}
	if cmd := m.selectIssue("repair:first"); cmd != nil {
		t.Fatal("a stale action could prepare a different issue's repair")
	}
	prepared, writes := 0, 0
	m.deps.repair = func(_ context.Context, _ installer.CheckContext, id string) (installer.RepairPlan, error) {
		prepared++
		if id != "second" {
			t.Fatal("wrong repair selected", id)
		}
		return installer.RepairPlan{SummaryKey: "repair.pro.spl.summary", Apply: func(context.Context, installer.Reporter) error { writes++; return nil }}, nil
	}
	key(m, tea.KeyEnter)
	if m.solutionView.focus != solutionDetails || prepared != 0 {
		t.Fatal("reading a solution triggered repair preparation")
	}
	key(m, tea.KeyPgDown)
	if m.solutionView.viewport.YOffset == 0 {
		t.Fatal("inline solution could not scroll")
	}
	key(m, tea.KeyEnter) // Move to the actions; this must not execute them.
	runJobs(m, key(m, tea.KeyEnter))
	if m.page != repairConfirmPage || prepared != 1 || writes != 0 {
		t.Fatal("preparation skipped the separate confirmation")
	}
	key(m, tea.KeyEnter) // Back is still the safe default.
	if m.page != issuesPage || m.solutionIndex != 1 || writes != 0 {
		t.Fatal("confirmation did not return to the same issue safely")
	}
}

func TestFailedOutcomeRetainsGuidanceAndDiagnostic(t *testing.T) {
	m := testModel(t)
	m.checks = installer.CheckResult{Supported: true, Issues: []installer.Issue{guideIssue(false)}, Checks: []installer.CheckOutcome{{ID: "boot", Error: "startup media is unreadable"}}}
	m.goPage(issuesPage)
	if m.solutionFindingCount() != 2 {
		t.Fatal("partial results or the failed check disappeared")
	}
	for _, solution := range m.checks.Issues[0].Solutions {
		if !strings.Contains(m.solutionContent(), m.opts.Text.T(solution.BodyKey)) {
			t.Fatal("an inline solution was omitted", solution.ID)
		}
	}
	m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'c'}})
	if m.page != issuesPage {
		t.Fatal("C bypassed a failed check outcome")
	}
	key(m, tea.KeyTab)
	key(m, tea.KeyDown)
	if m.solutionIndex != 1 || !strings.Contains(m.solutionContent(), "startup media is unreadable") {
		t.Fatal("diagnostic is not reachable from the issue list")
	}
	for _, item := range m.menu.Items() {
		if strings.HasPrefix(item.(menuItem).id, "repair:") || item.(menuItem).id == "continue" {
			t.Fatal("diagnostic inherited an issue repair or allowed continuing")
		}
	}
}

func TestSolutionEscapeReturnsToIssuesBeforeExiting(t *testing.T) {
	m := testModel(t)
	m.checks = installer.CheckResult{Supported: true, Issues: []installer.Issue{guideIssue(true)}}
	m.goPage(issuesPage)
	m.selectIssue("0")
	if cmd := key(m, tea.KeyEsc); cmd != nil || m.cancelled || m.solutionView.focus != solutionIssues || m.page != issuesPage {
		t.Fatal("Escape exited while reading a solution or bypassed its blocker")
	}
	if !isQuit(key(m, tea.KeyEsc)) {
		t.Fatal("Escape from the issue list did not exit")
	}
}
