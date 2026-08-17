package ui

import (
	"errors"
	"strings"
	"time"

	"github.com/merefield/termcourse/internal/discourse"
)

func (u *UI) showError(err error) bool {
	if err == nil {
		return true
	}
	message := err.Error()
	var httpErr *discourse.HTTPError
	if errors.As(err, &httpErr) {
		message = discourse.ErrorMessage(httpErr.Body)
	}
	width, height := u.terminal.Size()
	inner := max(width-4, 1)
	lines := wrapLines(strings.TrimSpace(message), inner, u.linksEnabled)
	maxMessageLines := max(height-7, 1)
	if len(lines) > maxMessageLines {
		lines = append(lines[:maxMessageLines-1], truncate(u.t("ui.scroll.more_below"), inner))
	}
	lines = append(lines, "", u.t("ui.errors.continue"))
	panel := u.style.AppHeader(u.t("ui.errors.title"), u.displayURL, lines, width, height)
	screen := make([]string, height)
	copy(screen, panel[:min(len(panel), height)])
	u.renderer.Render(screen, width, height, "error", -1, -1, true)
	key, readErr := u.terminal.ReadKey(24 * time.Hour)
	return readErr == nil && key != "q"
}
