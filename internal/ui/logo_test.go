package ui

import (
	"bytes"
	"fmt"
	"strings"
	"testing"
	"time"

	tea "charm.land/bubbletea/v2"
	"github.com/merefield/termcourse/internal/discourse"
)

func TestLogoMouseTargetsMatchVisibleHeader(t *testing.T) {
	for _, tc := range []struct {
		width, height int
		host          string
		logoWidths    []int
	}{
		{10, 24, "forum.example", nil},
		{20, 24, "forum.example", []int{6}},
		{40, 24, "forum.example", []int{displayWidth(compactHeaderLogo)}},
		{63, 32, "forum.example", []int{displayWidth(compactHeaderLogo)}},
		{64, 24, strings.Repeat("x", 60), []int{3}},
		{64, 30, "forum.example", []int{displayWidth(headerLogo[0]), displayWidth(headerLogo[1])}},
		{80, 32, "forum.example", []int{displayWidth(headerLogo[0]), displayWidth(headerLogo[1])}},
		{120, 40, "forum.example", []int{displayWidth(headerLogo[0]), displayWidth(headerLogo[1])}},
	} {
		t.Run(fmt.Sprintf("%dx%d", tc.width, tc.height), func(t *testing.T) {
			u := &UI{style: NewStyle(testTheme(), &bytes.Buffer{}), locale: "en", mouseEnabled: true, displayURL: tc.host}
			header := u.navigationHeader("", primaryTopics, "topics", "top", "yearly", nil, tc.width, tc.height)
			// Stop before the first tab row; status, subtitle and padding must
			// never activate Home, even where a larger logo used to be.
			titleRows := len(header) - 2
			for y := -1; y < titleRows; y++ {
				for x := -1; x <= tc.width; x++ {
					want := y >= 0 && y < len(tc.logoWidths) && x >= 0 && x < tc.logoWidths[y]
					got := u.mouseKey(tea.MouseClickMsg{X: x, Y: y, Button: tea.MouseLeft}) == keyLogoHome
					if got != want {
						t.Fatalf("logo target at (%d,%d) = %v, want %v", x, y, got, want)
					}
				}
			}
		})
	}
}

func TestLogoMouseTargetsRebuiltAfterResizeAndRespectNavigationLock(t *testing.T) {
	u := &UI{style: NewStyle(testTheme(), &bytes.Buffer{}), locale: "en", mouseEnabled: true, displayURL: "forum.example"}
	u.navigationHeader("", primaryTopics, "", "", "", nil, 120, 32)
	if u.mouseKey(tea.MouseClickMsg{X: 30, Y: 1, Button: tea.MouseLeft}) != keyLogoHome {
		t.Fatal("wide logo second line is not clickable")
	}
	u.navigationHeader("", primaryTopics, "", "", "", nil, 20, 24)
	if u.mouseKey(tea.MouseClickMsg{X: 30, Y: 1, Button: tea.MouseLeft}) == keyLogoHome {
		t.Fatal("resize retained an invisible logo target")
	}
	for _, tc := range []struct{ mouse, locked, allowed, want bool }{
		{true, false, false, true},
		{false, false, false, false},
		{true, true, false, false},
		{true, true, true, true},
	} {
		u.mouseEnabled, u.navigationLocked, u.primaryNavAllowed = tc.mouse, tc.locked, tc.allowed
		u.navigationHeader("", primaryCompose, "compose", "title", "", nil, 88, 24)
		got := u.mouseKey(tea.MouseClickMsg{X: 0, Y: 0, Button: tea.MouseLeft}) == keyLogoHome
		if got != tc.want {
			t.Fatalf("mouse=%v locked=%v allowed=%v: clickable=%v", tc.mouse, tc.locked, tc.allowed, got)
		}
	}
}

func newLogoTestUI() *UI {
	u := New(&logoTestClient{}, Options{Theme: testTheme(), Locale: "en", Output: &bytes.Buffer{}})
	u.mouseEnabled = true
	u.tick = time.Second
	u.terminal.state.width.Store(88)
	u.terminal.state.height.Store(24)
	u.lastStatusRefresh = time.Now()
	u.siteCache = discourse.JSON{}
	return u
}

func queueLogoClick(u *UI) {
	u.terminal.state.inputs <- terminalInput{msg: tea.MouseClickMsg{X: 0, Y: 0, Button: tea.MouseLeft}}
	// End each loop if navigation fails rather than waiting indefinitely.
	u.terminal.state.inputs <- terminalInput{msg: tea.KeyPressMsg(tea.Key{Code: tea.KeyEscape})}
}

func TestOnlyLogoLeftClickRequestsLatest(t *testing.T) {
	for _, msg := range []tea.MouseMsg{
		tea.MouseMotionMsg{X: 0, Y: 0},
		tea.MouseReleaseMsg{X: 0, Y: 0, Button: tea.MouseLeft},
		tea.MouseClickMsg{X: 0, Y: 0, Button: tea.MouseRight},
		tea.MouseClickMsg{X: 0, Y: 0, Button: tea.MouseLeft},
	} {
		u := newLogoTestUI()
		u.navigationHeader("", primarySearch, "search", "results", "", nil, 88, 24)
		u.terminal.state.inputs <- terminalInput{msg: msg}
		key, err := u.readKey(time.Second)
		click, clicked := msg.(tea.MouseClickMsg)
		want := clicked && click.Button == tea.MouseLeft
		if err != nil || u.latestRequested != want || (u.requestedPrimary != nil) != want {
			t.Fatalf("%T: key=%q err=%v home=%v primary=%v", msg, key, err, u.latestRequested, u.requestedPrimary)
		}
		if want && key != primaryTabKey(primaryTopics) {
			t.Fatalf("logo click produced %q", key)
		}
	}
}

func TestLogoReturnsFilteredTopicsToLatest(t *testing.T) {
	for _, filter := range []string{"unread", "private", "hot", "new", "top", "latest"} {
		t.Run(filter, func(t *testing.T) {
			u := newLogoTestUI()
			queueLogoClick(u)
			if result := u.topicListLoop(discourse.JSON{}, filter, "yearly"); result.kind != "navigate" {
				t.Fatalf("logo did not exit %s: %+v", filter, result)
			}
			assertLogoNavigation(t, u)
		})
	}
}

func assertLogoNavigation(t *testing.T, u *UI) {
	t.Helper()
	if !u.latestRequested || u.requestedPrimary == nil || *u.requestedPrimary != primaryTopics {
		t.Fatal("logo did not request Latest Topics")
	}
	// Nested flows may consume the primary request before the outer loop.
	u.takeRequestedPrimary(primarySearch)
	active, filter, period := u.takeRequestedNavigation(primarySearch, "top", "yearly")
	if active != primaryTopics || filter != "latest" || period != "monthly" || u.latestRequested {
		t.Fatalf("navigation = %v/%s/%s, pending=%v", active, filter, period, u.latestRequested)
	}
	// Ordinary tab navigation continues to remember the selected filter.
	u.requestPrimary(primaryNotifications)
	active, filter, period = u.takeRequestedNavigation(primaryTopics, "unread", "yearly")
	if active != primaryNotifications || filter != "unread" || period != "yearly" {
		t.Fatal("ordinary navigation reset the topic filter")
	}
}

func TestLogoExitsOpenTopicFromEveryOrigin(t *testing.T) {
	for _, origin := range []primaryTabID{primaryTopics, primarySearch, primaryNotifications} {
		t.Run(fmt.Sprint(origin), func(t *testing.T) {
			u := newLogoTestUI()
			u.activePrimary = origin
			u.topicCache[42] = discourse.JSON{"id": 42, "title": "Topic", "post_stream": discourse.JSON{
				"stream": []any{101}, "posts": []any{discourse.JSON{"id": 101, "post_number": 1, "username": "member", "raw": "Post"}},
			}}
			u.lastRead[42] = 1
			queueLogoClick(u)
			if quit, result := u.topicLoop(42, 1, "search"); quit || result != "navigate" {
				t.Fatalf("topic logo: quit=%v result=%q", quit, result)
			}
			assertLogoNavigation(t, u)
		})
	}
}

func TestLogoExitsSearchNotificationsAndImages(t *testing.T) {
	for _, kind := range []string{"search", "notifications", "image"} {
		t.Run(kind, func(t *testing.T) {
			u := newLogoTestUI()
			queueLogoClick(u)
			switch kind {
			case "search":
				u.searchLoop("query")
			case "notifications":
				u.notificationsLoop()
			case "image":
				u.activePrimary = primaryTopics
				u.imageScreenHeader(88, 24)
				key, err := u.readKey(time.Second)
				if err != nil || !u.leaveImageForKey(key) {
					t.Fatal("logo did not exit an image opened from Topics", err)
				}
			}
			assertLogoNavigation(t, u)
		})
	}
}

func TestLogoPromptNavigationProtectsDrafts(t *testing.T) {
	for _, kind := range []string{"search", "blank-draft", "entered-draft"} {
		t.Run(kind, func(t *testing.T) {
			u := newLogoTestUI()
			active, title, prompt := primaryCompose, "ui.composer.new_topic_title", "ui.composer.enter_title"
			if kind == "search" {
				active, title, prompt = primarySearch, "ui.search.title", "ui.search.prompt"
			}
			if kind == "entered-draft" {
				u.terminal.state.inputs <- terminalInput{msg: tea.KeyPressMsg(tea.Key{Code: 'D', Text: "D"})}
			}
			queueLogoClick(u)
			u.promptSingleLine(title, prompt, "Input: ", active, "", "")
			if kind == "entered-draft" {
				if u.latestRequested || u.requestedPrimary != nil {
					t.Fatal("logo discarded an entered draft title")
				}
			} else {
				assertLogoNavigation(t, u)
			}
		})
	}
}

type logoTestClient struct{ Client }

func (*logoTestClient) Search(string) (discourse.JSON, error) { return discourse.JSON{}, nil }
func (*logoTestClient) Notifications(int, int, string) (discourse.JSON, error) {
	return discourse.JSON{}, nil
}
