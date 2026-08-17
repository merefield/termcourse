package ui

import (
	"strings"

	tea "charm.land/bubbletea/v2"
)

// ScreenRenderer prepares complete terminal frames. Bubble Tea v2 owns the
// actual diffing, synchronized output, cursor lifecycle, and resize repaint.
type ScreenRenderer struct {
	terminal *Terminal
	progress *tea.ProgressBar
}

func NewScreenRenderer(terminal *Terminal) *ScreenRenderer {
	return &ScreenRenderer{terminal: terminal}
}

func (r *ScreenRenderer) Reset() {
	if r.terminal != nil {
		r.terminal.clear()
	}
}

func (r *ScreenRenderer) Render(lines []string, width, height int, viewKey string, cursorX, cursorY int, force bool) {
	content := normalizeScreen(lines, width, height)
	if !strings.HasPrefix(viewKey, "topic-") {
		r.progress = nil
	}
	if r.terminal != nil {
		r.terminal.render(content, cursorX, cursorY, r.progress, force)
	}
}

func (r *ScreenRenderer) SetProgress(current, total int) {
	if total <= 0 {
		r.progress = nil
		return
	}
	percent := int(float64(current) / float64(total) * 100.0)
	r.progress = tea.NewProgressBar(tea.ProgressBarDefault, percent)
}

func normalizeScreen(lines []string, width, height int) string {
	normalized := make([]string, height)
	for row := range normalized {
		if row < len(lines) {
			normalized[row] = padLine(lines[row], width)
		} else {
			normalized[row] = padLine("", width)
		}
	}
	return strings.Join(normalized, "\n")
}

func (r *ScreenRenderer) Clear() { r.Reset() }
