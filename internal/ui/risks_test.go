package ui

import (
	"context"
	"fmt"
	"strings"
	"testing"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
	"github.com/charmbracelet/x/ansi"
	"github.com/onekvm/onekvm-installer/internal/i18n"
)

func riskModel(t *testing.T, language string) *model {
	t.Helper()
	tr, err := i18n.New(language, func(string) string { return "" })
	if err != nil {
		t.Fatal(err)
	}
	m := newModel(context.Background(), Options{Command: "install", Text: tr}, services{})
	m.goPage(languagePage)
	m.selectItem()
	if m.page != riskPage || m.task != nil {
		t.Fatal("risk review did not precede installation work")
	}
	return m
}

func TestEveryDisabledRiskBlocksAllContinueInputs(t *testing.T) {
	for i, item := range riskOptions {
		t.Run(item.id, func(t *testing.T) {
			m := riskModel(t, "en")
			m.risks.index = i
			key(m, tea.KeySpace)
			if len(m.disabledRisks()) != 1 || !m.risks.disabled[item.id] {
				t.Fatal("option was not disabled")
			}
			m.risks.focus = 2
			if key(m, tea.KeyEnter) != nil || m.acceptRisks() != nil {
				t.Fatal("disabled Next produced a command")
			}
			m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'n'}})
			g := m.riskLayout()
			m.Update(tea.MouseMsg{X: 3, Y: g.footerY + 2, Button: tea.MouseButtonLeft, Action: tea.MouseActionPress})
			if m.page != riskPage || m.task != nil || m.risks.accepted {
				t.Fatal("an input path bypassed a disabled option")
			}
			if !strings.Contains(ansi.Strip(m.View()), "Cannot continue: 1 disabled.") {
				t.Fatal("missing disabled reason", ansi.Strip(m.View()))
			}
			m.risks.focus = 0
			key(m, tea.KeySpace)
			m.risks.focus = 2
			key(m, tea.KeyEnter)
			if m.page != sourcePage || !m.risks.accepted {
				t.Fatal("reenabling the final disabled option did not restore Next")
			}
		})
	}
}

func TestRiskReviewCannotBeSkippedByCommandFlags(t *testing.T) {
	for _, command := range []string{"install", "prepare", "arm", "solution"} {
		for _, source := range []string{"none", "image", "url"} {
			t.Run(command+"/"+source, func(t *testing.T) {
				m := riskModel(t, "en")
				m.opts.Command = command
				if source == "image" {
					m.opts.Image = "/image.img"
				} else if source == "url" {
					m.opts.URL = "https://example.org/image.img"
				}
				m.risks.disabled["dns"] = true
				m.startWizard()
				if m.acceptRisks() != nil || m.task != nil || m.page != riskPage {
					t.Fatal("command flags started work before all options were allowed")
				}
			})
		}
	}
}

func TestDetailsCountsTranslationsAndPhysicalRiskQualification(t *testing.T) {
	for _, language := range []string{"en", "zh", "zh-TW"} {
		m := riskModel(t, language)
		for i, item := range riskOptions {
			m.risks.index = i
			m.openRiskDetails()
			content := m.riskContent()
			for n, key := range item.points {
				if strings.Contains(content, key) || !strings.Contains(content, fmt.Sprintf("%d. ", n+1)) {
					t.Fatalf("%s/%s: missing numbered translation %s", language, item.id, key)
				}
			}
			if !strings.Contains(content, m.opts.Text.T("risk.points", len(item.points))) {
				t.Fatal("details count differs from the numbered notices")
			}
			key(m, tea.KeyEsc)
			if m.page != riskPage || m.risks.index != i || m.risks.focus != 1 {
				t.Fatal("details return lost selection or focus")
			}
			m.risks.focus = 0
			m.keepRiskVisible(m.riskLayout().rows)
			if !strings.Contains(ansi.Strip(m.View()), m.opts.Text.T("risk.details", len(item.points))) {
				t.Fatal("details button lost its actual notice count")
			}
		}
	}
	m := riskModel(t, "en")
	for _, item := range riskOptions {
		if item.id != "cpu" && item.id != "memory" && item.id != "storage" {
			continue
		}
		text := m.opts.Text.T(item.points[0])
		if !strings.Contains(text, "specific") || !strings.Contains(text, "noise") && !strings.Contains(text, "signals") {
			t.Fatal("hardware side-channel risk lost its conditions", text)
		}
	}
}

func TestRiskDetailsScrollAndMouseButtons(t *testing.T) {
	m := riskModel(t, "zh")
	m.Update(tea.WindowSizeMsg{Width: 80, Height: 24})
	m.risks.index = 2 // NTP: long target list plus four numbered risks.
	m.keepRiskVisible(m.riskLayout().rows)
	g := m.riskLayout()
	y := g.bodyY + (m.risks.index-m.risks.start)*2 + 1
	m.Update(tea.MouseMsg{X: g.detailX + 3, Y: y, Button: tea.MouseButtonLeft, Action: tea.MouseActionPress})
	if m.page != riskDetailsPage {
		t.Fatal("mouse details button did not open")
	}
	key(m, tea.KeyPgDown)
	if m.risks.viewport.YOffset == 0 {
		t.Fatal("long details did not scroll")
	}
	g = m.riskLayout()
	m.Update(tea.MouseMsg{X: 3, Y: g.footerY + 1, Button: tea.MouseButtonLeft, Action: tea.MouseActionPress})
	if m.page != riskPage || m.risks.index != 2 {
		t.Fatal("mouse Back did not return to the selected row")
	}
}

func TestRiskPagesFitTerminalsAndRetainDisabledState(t *testing.T) {
	for _, language := range []string{"en", "zh", "zh-TW"} {
		m := riskModel(t, language)
		for _, size := range [][2]int{{80, 24}, {120, 40}, {44, 16}, {30, 12}} {
			m.Update(tea.WindowSizeMsg{Width: size[0], Height: size[1]})
			for _, p := range []page{riskPage, riskDetailsPage} {
				m.goPage(p)
				view := m.View()
				if lipgloss.Height(view) > size[1] {
					t.Fatalf("%s page %v exceeds %v height", language, p, size)
				}
				for _, line := range strings.Split(view, "\n") {
					if ansi.StringWidth(line) > size[0] {
						t.Fatalf("%s page %v exceeds %v width", language, p, size)
					}
				}
			}
		}
		m.goPage(riskPage)
		m.risks.disabled["cpu"] = true
		m.updateRiskKey(tea.KeyMsg{Type: tea.KeyEsc})
		m.selectItem() // Reselect language; a disabled choice must survive.
		if m.page != riskPage || !m.risks.disabled["cpu"] || m.acceptRisks() != nil {
			t.Fatal("language navigation bypassed the disabled processor")
		}
	}
}

func TestRiskDetailsDoNotExposeURLCredentials(t *testing.T) {
	m := riskModel(t, "en")
	m.opts.URL = "https://user:private-password@example.org/image.img?token=private-token#secret"
	m.risks.index = 1
	content := m.riskContent()
	if !strings.Contains(content, "https://example.org/image.img") {
		t.Fatal("missing selected image destination")
	}
	for _, secret := range []string{"private-password", "private-token", "#secret"} {
		if strings.Contains(content, secret) {
			t.Fatal("image credentials leaked in risk details")
		}
	}
}
