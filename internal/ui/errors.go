package ui

import (
	"errors"
	"math"
	"strings"
	"time"

	"github.com/merefield/termcourse/internal/discourse"
)

func (u *UI) showError(err error) bool {
	if err == nil {
		return true
	}
	message := u.errorMessage(err, time.Now())
	width, height := u.terminal.Size()
	u.resetMouseLayout()
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
	u.addMouseRegion(0, max(height-2, 0), width, min(2, height), "enter")
	key, readErr := u.readKey(24 * time.Hour)
	return readErr == nil && key != "q"
}

func (u *UI) errorMessage(err error, now time.Time) string {
	message := err.Error()
	var httpErr *discourse.HTTPError
	if !errors.As(err, &httpErr) {
		return message
	}
	message = discourse.ErrorMessage(httpErr.Body)
	limit, ok := httpErr.RateLimit()
	if !ok {
		return message
	}
	remaining := limit.RetryAt.Sub(now)
	switch {
	case limit.Wait > 0 && remaining > 0:
		message += "\n\n" + u.t(
			"ui.errors.rate_limit_retry",
			"duration", u.retryDuration(remaining),
			"time", limit.RetryAt.Local().Format("15:04:05"),
		)
	case limit.ServerTimeLeft != "":
		message += "\n\n" + u.t("ui.errors.rate_limit_retry_server", "duration", limit.ServerTimeLeft)
	case limit.TimingProvided:
		message += "\n\n" + u.t("ui.errors.rate_limit_retry_now")
	default:
		message += "\n\n" + u.t("ui.errors.rate_limit_retry_unknown")
	}
	if u.debug && limit.Code != "" {
		message += "\n" + u.t("ui.errors.rate_limit_code", "code", limit.Code)
	}
	return message
}

func (u *UI) retryDuration(duration time.Duration) string {
	seconds := max(int(math.Ceil(duration.Seconds())), 1)
	parts := make([]string, 0, 2)
	units := []struct {
		seconds int
		key     string
	}{
		{24 * 60 * 60, "ui.time.days"},
		{60 * 60, "ui.time.hours"},
		{60, "ui.time.minutes"},
		{1, "ui.time.seconds"},
	}
	for _, unit := range units {
		if seconds < unit.seconds {
			continue
		}
		count := seconds / unit.seconds
		seconds %= unit.seconds
		parts = append(parts, u.t(unit.key, "count", count))
		if len(parts) == 2 {
			break
		}
	}
	return strings.Join(parts, " ")
}
