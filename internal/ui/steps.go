package ui

import (
	"strings"

	"github.com/charmbracelet/x/ansi"
	"github.com/onekvm/onekvm-installer/internal/i18n"
)

type stepState uint8

const (
	stepPending stepState = iota
	stepRunning
	stepDone
	stepFailed
)

type taskStep struct {
	key   string
	state stepState
}

func newTaskStatus(kind, label string, download bool) *taskStatus {
	keys := []string{label}
	switch kind {
	case "download", "remote-download":
		keys = []string{"op.download", "op.verify"}
	case "prepare", "prepare-downloaded":
		keys = []string{"op.prepare", "op.cleanup"}
		if download && kind != "prepare-downloaded" {
			keys = append(keys, "op.download")
		}
		keys = append(keys, "op.verify")
		if !download && kind != "prepare-downloaded" {
			keys = append(keys, "op.stage", "op.staged_verify")
		}
		keys = append(keys, "op.extract", "op.finalize")
	case "direct":
		keys = []string{"op.prepare", "op.direct_check", "op.direct_write", "op.direct_config"}
	case "arm":
		keys = []string{"op.recheck", "op.boot_check", "op.boot_cleanup", "op.arm"}
	}
	s := &taskStatus{label: label}
	for _, key := range keys {
		s.steps = append(s.steps, taskStep{key: i18n.OperationKey(key)})
	}
	s.startStep(label)
	return s
}

// Called while holding the task mutex, or before publishing a new task.
func (s *taskStatus) startStep(label string) int {
	key := i18n.OperationKey(label)
	for i := range s.steps {
		if s.steps[i].key == key {
			s.steps[i].state = stepRunning
			return i
		}
	}
	s.steps = append(s.steps, taskStep{key, stepRunning})
	return len(s.steps) - 1
}

func (s *taskStatus) stepSnapshot() []taskStep {
	s.mu.Lock()
	defer s.mu.Unlock()
	return append([]taskStep(nil), s.steps...)
}

func (s *taskStatus) finish(err error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	for i := range s.steps {
		if s.steps[i].state == stepRunning {
			s.steps[i].state = stepDone
			if err != nil {
				s.steps[i].state = stepFailed
			}
		}
	}
}

func (m *model) stepsView() string {
	var rows []string
	for _, step := range m.task.stepSnapshot() {
		label := ansi.Truncate(m.opts.Text.Operation(step.key), max(12, m.width-10), "…")
		switch step.state {
		case stepDone:
			rows = append(rows, success.Render("✓ "+label))
		case stepRunning:
			rows = append(rows, m.spin.View()+" "+accent.Render(label))
		case stepFailed:
			rows = append(rows, danger.Render("✗ "+label))
		default:
			rows = append(rows, muted.Render("○ "+label))
		}
	}
	return strings.Join(rows, "\n")
}

// A moving segment signals work with an unknown total, without inventing a
// percentage. The normal progress bar is used as soon as a total is reported.
func (m *model) indeterminateBar() string {
	width := max(10, min(m.bar.Width, m.width-10))
	segment := max(3, width/8)
	span := width - segment
	position := m.pulse % (2 * span)
	if position > span {
		position = 2*span - position
	}
	return muted.Render(strings.Repeat("─", position)) +
		accent.Render(strings.Repeat("━", segment)) +
		muted.Render(strings.Repeat("─", span-position))
}
