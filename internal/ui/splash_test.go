package ui

import (
	"context"
	"os"
	"strings"
	"testing"
	"time"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
	"github.com/charmbracelet/x/ansi"
	"github.com/muesli/termenv"
	"github.com/onekvm/onekvm-installer/internal/i18n"
	"github.com/onekvm/onekvm-installer/internal/installer"
)

func splashModel(t *testing.T) *model {
	t.Helper()
	tr, err := i18n.New("en", os.Getenv)
	if err != nil {
		t.Fatal(err)
	}
	m := newModel(context.Background(), Options{Command: "install", Text: tr}, services{
		check: func(context.Context, installer.CheckContext) (installer.CheckResult, error) {
			return installer.CheckResult{}, nil
		},
	})
	m.splash.started = time.Now()
	return m
}

func TestSplashLastsThreeSecondsBeforeLanguageSelection(t *testing.T) {
	m := splashModel(t)
	if m.page != splashPage || strings.Contains(m.View(), "English") {
		t.Fatal("wizard appeared before splash")
	}
	for _, keyType := range []tea.KeyType{tea.KeyEnter, tea.KeyDown, tea.KeyTab} {
		if key(m, keyType) != nil || m.page != splashPage || m.task != nil {
			t.Fatal("splash input started the wizard or installation")
		}
	}
	started := m.splash.started
	m.Update(splashTickMsg{started.Add(2999 * time.Millisecond)})
	if m.page != splashPage {
		t.Fatal("splash ended early")
	}
	m.Update(tea.WindowSizeMsg{Width: 60, Height: 20})
	if m.splash.started != started {
		t.Fatal("resizing restarted the animation")
	}
	m.Update(splashTickMsg{started.Add(3 * time.Second)})
	if m.page != languagePage || !strings.Contains(m.View(), "English") || m.task != nil {
		t.Fatal("splash did not finish on the idle language menu")
	}
	// A late timer must not replay the splash after the user starts navigating.
	m.Update(key(m, tea.KeyEnter)())
	m.Update(splashTickMsg{started.Add(4 * time.Second)})
	if m.page != sourcePage {
		t.Fatal("late splash tick interrupted the wizard")
	}
}

func TestSplashCanExitImmediately(t *testing.T) {
	for _, interrupt := range []tea.Msg{tea.KeyMsg{Type: tea.KeyCtrlC}, interrupted{}} {
		m := splashModel(t)
		_, cmd := m.Update(interrupt)
		if !isQuit(cmd) || m.View() != "" || m.task != nil {
			t.Fatal("splash cancellation did not exit immediately")
		}
		m.Update(splashTickMsg{m.splash.started.Add(splashDuration)})
		if m.page != splashPage || m.View() != "" {
			t.Fatal("timer revived a cancelled splash")
		}
	}
}

func TestSplashFitsTerminalDuringColorAndScatter(t *testing.T) {
	m := splashModel(t)
	for _, dimensions := range [][2]int{{80, 24}, {120, 40}, {30, 12}, {8, 4}, {1, 1}, {0, 0}} {
		m.Update(tea.WindowSizeMsg{Width: dimensions[0], Height: dimensions[1]})
		for _, elapsed := range []time.Duration{0, time.Second, 1750 * time.Millisecond, 2300 * time.Millisecond, 2999 * time.Millisecond} {
			m.splash.elapsed = elapsed
			view := m.View()
			if dimensions[0] == 0 || dimensions[1] == 0 {
				if view != "" {
					t.Fatal("empty terminal produced output")
				}
				continue
			}
			if lipgloss.Height(view) > dimensions[1] {
				t.Fatalf("splash scrolls %v terminal at %v", dimensions, elapsed)
			}
			for _, line := range strings.Split(view, "\n") {
				if ansi.StringWidth(line) > dimensions[0] {
					t.Fatalf("splash wraps %v terminal at %v", dimensions, elapsed)
				}
			}
		}
	}
}

func TestSplashSupportsTerminalColorProfiles(t *testing.T) {
	previous := lipgloss.ColorProfile()
	t.Cleanup(func() { lipgloss.SetColorProfile(previous) })
	t.Setenv("COLORFGBG", "15;0")
	for _, tc := range []struct {
		profile termenv.Profile
		color   string
		absent  string
	}{
		{termenv.TrueColor, "\x1b[38;2;22;119;255m", ";5;"},
		{termenv.ANSI256, "\x1b[38;5;", ";2;"},
		{termenv.ANSI, "\x1b[94m", ";5;"},
		{termenv.Ascii, "█", "\x1b"},
	} {
		t.Run(tc.profile.Name(), func(t *testing.T) {
			lipgloss.SetColorProfile(tc.profile)
			s := splash{elapsed: 1650 * time.Millisecond}
			view := s.view(80, 24)
			if !strings.Contains(view, tc.color) || strings.Contains(view, tc.absent) {
				t.Fatal("splash did not honor terminal color support")
			}
			// The terminal renderer skips unchanged rows, so color state must
			// never carry from a colored row into the next row or wizard.
			for _, line := range strings.Split(view, "\n") {
				if strings.Contains(line, "\x1b[") && !strings.HasSuffix(line, "\x1b[0m") {
					t.Fatal("row left terminal colors active")
				}
			}
		})
	}
}

func TestSplashRendererClearsBackgroundBetweenParticles(t *testing.T) {
	// A two-color half-block followed by empty space and a single pixel.
	pixels := []splashPixel{{0xff0000, true}, {}, {0xff0000, true}, {0x0000ff, true}, {}, {}}
	view := splashRender(pixels, 3, 1, termenv.TrueColor)
	if ansi.Strip(view) != "▀ ▀" || !strings.Contains(view, "\x1b[49m ") || !strings.HasSuffix(view, "\x1b[0m") {
		t.Fatal("particle background bled into empty space")
	}
}

func BenchmarkSplashView(b *testing.B) {
	previous := lipgloss.ColorProfile()
	b.Cleanup(func() { lipgloss.SetColorProfile(previous) })
	for _, profile := range []termenv.Profile{termenv.TrueColor, termenv.ANSI256} {
		b.Run(profile.Name(), func(b *testing.B) {
			lipgloss.SetColorProfile(profile)
			s := splash{}
			s.view(80, 24)
			b.ReportAllocs()
			b.ResetTimer()
			for i := 0; i < b.N; i++ {
				s.elapsed = time.Duration(i%180) * splashDuration / 180
				s.view(80, 24)
			}
		})
	}
}
