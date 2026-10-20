package ui

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
	"github.com/charmbracelet/x/ansi"
	"github.com/onekvm/onekvm-installer/internal/i18n"
	"github.com/onekvm/onekvm-installer/internal/installer"
)

func guideIssue(blocking bool) installer.Issue {
	return installer.Issue{ID: "pro.sd-boot", TitleKey: "issue.pro.sd-boot.title", DescriptionKey: "issue.pro.sd-boot.description", Blocking: blocking,
		Solutions: []installer.Solution{
			{ID: "boot", TitleKey: "solutions.guide", BodyKey: "issue.pro.sd-boot.solution"},
			{ID: "firmware", TitleKey: "issue.pro.sd-boot.firmware.title", BodyKey: "issue.pro.sd-boot.firmware.solution"},
			{ID: "files", TitleKey: "issue.pro.sd-files.title", BodyKey: "issue.pro.sd-files.solution"},
		}}
}

func TestChecksWaitForVerifiedImageAndSupportAdvisoryContinue(t *testing.T) {
	m := testModel(t)
	verified, checked := false, 0
	source := filepath.Join(t.TempDir(), "sd.img")
	if err := os.WriteFile(source, []byte("fixture"), 0600); err != nil {
		t.Fatal(err)
	}
	m.deps.verify = func(context.Context, string, string, int64, installer.Progress) (installer.Image, error) {
		verified = true
		return installer.Image{Profile: "nanokvm", BuildID: "selected"}, nil
	}
	m.deps.check = func(_ context.Context, in installer.CheckContext) (installer.CheckResult, error) {
		checked++
		if !verified || in.Phase != installer.CheckImageReady || in.ImagePath != source || in.Image.BuildID != "selected" || in.Target.Profile != "nanokvm" {
			t.Fatal("check ran without the verified selection", in)
		}
		return installer.CheckResult{Supported: true, Issues: []installer.Issue{guideIssue(false)}}, nil
	}
	m.startWizard()
	if m.page != sourcePage || checked != 0 {
		t.Fatal("checked before an image was selected")
	}
	for _, item := range m.menu.Items() {
		if item.(menuItem).id == "solutions" {
			t.Fatal("offered image-independent checks")
		}
	}
	m.goPage(localPage)
	m.fields[0].input.SetValue(source)
	runJobs(m, m.submitForm())
	if m.page != issuesPage {
		t.Fatal("issue did not interrupt account setup")
	}
	m.selectIssue("continue")
	if m.page != accountPage {
		t.Fatal("advisory prevented continuing")
	}
	m.deps.check = func(context.Context, installer.CheckContext) (installer.CheckResult, error) {
		return installer.CheckResult{Supported: true}, nil
	}
	runJobs(m, m.startChecks("account"))
	if m.page != accountPage {
		t.Fatal("empty checks opened an unnecessary issues page")
	}
}

func TestBlockingIssueCannotBeBypassedAndRechecksBeforeConfirmation(t *testing.T) {
	m := testModel(t)
	blocked := true
	m.deps.check = func(_ context.Context, in installer.CheckContext) (installer.CheckResult, error) {
		if in.Phase != installer.CheckBeforeInstall {
			t.Fatal("wrong preinstall phase", in)
		}
		if blocked {
			return installer.CheckResult{Supported: true, Issues: []installer.Issue{guideIssue(true)}}, nil
		}
		return installer.CheckResult{Supported: true}, nil
	}
	m.Update(m.startChecks("confirm")())
	for _, item := range m.menu.Items() {
		if item.(menuItem).id == "continue" {
			t.Fatal("blocking issues offered Continue")
		}
	}
	m.selectIssue("continue")
	if m.page != issuesPage {
		t.Fatal("hidden action bypassed blocker")
	}
	m.selectIssue("0")
	if m.page != solutionPage {
		t.Fatal("could not view solution")
	}
	key(m, tea.KeyEnter)
	if m.page != issuesPage {
		t.Fatal("viewing guidance bypassed blocker")
	}
	if !isQuit(key(m, tea.KeyEsc)) {
		t.Fatal("escape bypassed blocker instead of exiting")
	}
	m.cancelled = false
	blocked = false
	m.Update(m.selectIssue("recheck")())
	if m.page != confirmPage || len(m.fields) != 1 || m.fields[0].input.Value() != "" {
		t.Fatal("resolved issues skipped explicit erase confirmation")
	}
}

func TestFailedCheckPreservesOtherFindingsAndCannotContinue(t *testing.T) {
	m := testModel(t)
	m.deps.check = func(context.Context, installer.CheckContext) (installer.CheckResult, error) {
		return installer.CheckResult{Supported: true, Issues: []installer.Issue{guideIssue(false)}}, errors.New("check failed")
	}
	m.image, m.source = installer.Image{Profile: "nanokvm"}, "/fixture.img"
	runJobs(m, m.startChecks("account"))
	if m.page != issuesPage || len(m.checks.Issues) != 1 || m.checkErr == nil {
		t.Fatal("lost partial results")
	}
	m.selectIssue("continue")
	if m.page != issuesPage {
		t.Fatal("failed check was treated as healthy")
	}
	for _, item := range m.menu.Items() {
		if item.(menuItem).id == "continue" {
			t.Fatal("failed checks offered Continue")
		}
	}
}

func TestInvalidLocalImageAndMissingSDNeverReachChecks(t *testing.T) {
	for _, state := range []string{"invalid-image", "missing-sd"} {
		t.Run(state, func(t *testing.T) {
			m := proModel(t)
			checks := 0
			m.source = "/selected/OneKVM.img"
			m.deps.check = func(context.Context, installer.CheckContext) (installer.CheckResult, error) {
				checks++
				return installer.CheckResult{}, nil
			}
			m.deps.verify = func(context.Context, string, string, int64, installer.Progress) (installer.Image, error) {
				if state == "invalid-image" {
					return installer.Image{}, errors.New("payload SHA-256 mismatch")
				}
				return installer.Image{Profile: "pro-sd"}, nil
			}
			m.deps.target = func(string, installer.Device, string) (installer.Device, error) {
				return installer.Device{}, errors.New("insert an SD card before selecting a Pro SD image")
			}
			runJobs(m, m.verifyLocalImage())
			if m.page != localPage || m.errKey == "" || checks != 0 || m.image.Profile != "" {
				t.Fatal("invalid selection continued to checks or account setup", m.page, checks)
			}
			if state == "missing-sd" && !strings.Contains(m.opts.Text.Diagnostic(m.errArgs[0].(string)), "No SD card detected") {
				t.Fatal("missing SD error is not actionable")
			}
		})
	}
}

func TestChangingImageClearsBlockingFindingsAndSelection(t *testing.T) {
	m := testModel(t)
	m.image, m.source = installer.Image{Profile: "nanokvm"}, "/old.img"
	m.checks = installer.CheckResult{Supported: true, Issues: []installer.Issue{guideIssue(true)}}
	m.goPage(issuesPage)
	m.selectIssue("change-image")
	if m.page != sourcePage || m.image.Profile != "" || m.source != "" || len(m.checks.Issues) != 0 {
		t.Fatal("old image findings survived changing the image")
	}
}

func TestEverySolutionIsRenderedAndScrollableInAllLanguages(t *testing.T) {
	for _, language := range []string{"en", "zh", "zh-TW"} {
		m := testModel(t)
		m.opts.Text, _ = i18n.New(language, os.Getenv)
		m.checks = installer.CheckResult{Supported: true, Issues: []installer.Issue{guideIssue(false)}}
		report := WriteSolutions(m.checks, m.opts.Text.T)
		for _, solution := range m.checks.Issues[0].Solutions {
			if !strings.Contains(report, m.opts.Text.T(solution.BodyKey)) || m.opts.Text.T(solution.BodyKey) == solution.BodyKey {
				t.Fatal("lost or untranslated solution", language, solution.ID)
			}
		}
		m.goPage(issuesPage)
		m.selectIssue("0")
		m.Update(tea.WindowSizeMsg{Width: 48, Height: 18})
		key(m, tea.KeyPgDown)
		if m.solutionView.viewport.YOffset == 0 {
			t.Fatal("guide cannot scroll", language)
		}
		key(m, tea.KeyEnd)
		if !strings.Contains(ansi.Strip(m.View()), "ctrl+c") {
			t.Fatal("guide lost navigation help")
		}
		if strings.Contains(ansi.Strip(m.View()), "issue.pro.") {
			t.Fatal("untranslated guide")
		}
		if lipgloss.Height(m.View()) > m.height {
			t.Fatal("guide navigation extends below the terminal", language, lipgloss.Height(m.View()), m.height)
		}
	}
}

func TestRepairRequiresSeparateConfirmationAndRechecksAfterwards(t *testing.T) {
	m := testModel(t)
	issue := guideIssue(false)
	issue.Solutions[1].RepairID = "pro.sd-boot.firmware"
	m.checks = installer.CheckResult{Supported: true, Issues: []installer.Issue{issue}}
	writes, prepared, checks := 0, 0, 0
	m.deps.repair = func(context.Context, installer.CheckContext, string) (installer.RepairPlan, error) {
		prepared++
		return installer.RepairPlan{ID: "pro.sd-boot.firmware", TitleKey: "issue.pro.sd-boot.firmware.title", SummaryKey: "repair.pro.spl.summary", Apply: func(_ context.Context, report installer.Reporter) error {
			return report("op.firmware_write", func(installer.Progress) error { writes++; return nil })
		}}, nil
	}
	m.deps.check = func(context.Context, installer.CheckContext) (installer.CheckResult, error) {
		checks++
		return installer.CheckResult{Supported: true}, nil
	}
	m.selectIssue("0")
	m.Update(key(m, tea.KeyEnter)())
	if m.page != repairConfirmPage || prepared != 1 || writes != 0 {
		t.Fatal("repair wrote before confirmation")
	}
	key(m, tea.KeyEnter) // Back is the safe default.
	if m.page != solutionPage || writes != 0 {
		t.Fatal("default review action wrote firmware")
	}
	m.Update(key(m, tea.KeyEnter)())
	key(m, tea.KeyDown)
	m.Update(key(m, tea.KeyEnter)())
	if m.page != repairDonePage || writes != 1 {
		t.Fatal("confirmed repair did not run exactly once")
	}
	m.Update(key(m, tea.KeyEnter)())
	if checks != 1 {
		t.Fatal("repair did not rerun the issue checks")
	}
}
