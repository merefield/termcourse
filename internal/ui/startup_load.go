package ui

import (
	"io"
	"strings"
	"time"

	tea "charm.land/bubbletea/v2"
	"github.com/merefield/termcourse/internal/discourse"
)

type startupData struct {
	topics, totals, private, site discourse.JSON
	topicsErr, totalsErr          error
	privateOK                     bool
}

// Fetch without touching UI state. The terminal owns the animation and stays
// responsive while the forum loads. Apply results only on the UI goroutine.
func (u *UI) loadInitialData() (startupData, error) {
	u.renderStartupLoading()
	width, height := u.terminal.Size()
	resize := time.NewTicker(u.tick)
	defer resize.Stop()
	client, username := u.client, u.options.Username
	started := time.Now()
	results := make(chan startupData, 1)
	cancel := make(chan struct{})
	defer close(cancel)
	cancelled := func() bool {
		select {
		case <-cancel:
			return true
		default:
			return false
		}
	}
	go func() {
		var result startupData
		result.totals, result.totalsErr = client.NotificationTotals()
		if cancelled() {
			return
		}
		if result.totalsErr != nil {
			var err error
			result.private, err = client.GetURL("/topics/private-messages-unread.json")
			result.privateOK = err == nil
			if cancelled() {
				return
			}
		}
		result.topics, result.topicsErr = client.ListTopics("latest", "monthly", username, nil)
		if cancelled() {
			return
		}
		if result.topicsErr == nil {
			result.site, _ = client.SiteInfo()
			if cancelled() {
				return
			}
		}
		results <- result
	}()
	for {
		select {
		case result := <-results:
			if result.totalsErr == nil {
				u.applyNotificationTotals(result.totals)
			} else if result.privateOK {
				u.pmUnread = unreadPrivateTopics(result.private)
				if u.live != nil {
					u.live.SetPMUnreadCount(u.pmUnread)
				}
			}
			u.lastStatusRefresh = started
			if result.topicsErr == nil {
				u.listCache[cacheKey("latest", "monthly")] = result.topics
				u.mergeUsers(result.topics)
				u.siteCache = result.site
			}
			return result, nil
		case err := <-u.terminal.state.done:
			if err == nil {
				err = io.EOF
			}
			return startupData{}, err
		case <-resize.C:
			nextWidth, nextHeight := u.terminal.Size()
			if nextWidth != width || nextHeight != height {
				width, height = nextWidth, nextHeight
				u.renderStartupLoading()
			}
		case input := <-u.terminal.state.inputs:
			if key, ok := input.msg.(tea.KeyPressMsg); ok && (strings.EqualFold(key.String(), "q") || key.String() == "ctrl+c") {
				return startupData{}, io.EOF
			}
			// Loading has no actionable controls. Never replay keys or clicks
			// against a screen that was hidden when they arrived.
		}
	}
}

func (u *UI) renderStartupLoading() {
	width, height := u.terminal.Size()
	header := u.navigationHeader("", primaryTopics, "topics", "latest", "monthly", nil, width, height)
	screen, pane := u.mainPane(header, width, height, 0)
	u.drawMainPane(screen, pane, []string{u.style.Text(u.t("ui.status.loading"), roleListMeta)})
	u.renderer.Render(screen, width, height, "startup-loading", -1, -1, false)
}
