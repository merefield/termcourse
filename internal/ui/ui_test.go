package ui

import (
	"bytes"
	"image"
	"image/color"
	"net/http"
	"strconv"
	"strings"
	"testing"
	"time"

	"charm.land/bubbles/v2/textarea"
	"charm.land/bubbles/v2/textinput"
	tea "charm.land/bubbletea/v2"
	ultraviolet "github.com/charmbracelet/ultraviolet"
	"github.com/merefield/termcourse/internal/discourse"
	"github.com/merefield/termcourse/internal/theme"
)

func TestRateLimitErrorShowsRetryDurationDeadlineAndDebugCode(t *testing.T) {
	receivedAt := time.Date(2026, 8, 17, 12, 0, 0, 0, time.UTC)
	u := &UI{locale: "en", debug: true}
	err := &discourse.HTTPError{
		Status: http.StatusTooManyRequests,
		Body:   []byte(`{"errors":["You’ve performed this action too many times."],"error_type":"rate_limit","extras":{"wait_seconds":45}}`),
		Header: http.Header{
			"Retry-After":                     []string{"90"},
			"Discourse-Rate-Limit-Error-Code": []string{"topic_view"},
		},
		ReceivedAt: receivedAt,
	}
	message := u.errorMessage(err, receivedAt.Add(1500*time.Millisecond))
	retryAt := receivedAt.Add(90 * time.Second).Local().Format("15:04:05")
	for _, expected := range []string{
		"You’ve performed this action too many times.",
		"Retry available in 1m 29s (at " + retryAt + ").",
		"Rate-limit code: topic_view",
	} {
		if !strings.Contains(message, expected) {
			t.Fatalf("message missing %q: %q", expected, message)
		}
	}

	u.debug = false
	if message := u.errorMessage(err, receivedAt); strings.Contains(message, "topic_view") {
		t.Fatalf("non-debug message exposed limiter code: %q", message)
	}
}

func TestRateLimitErrorExplainsZeroHumanAndMissingTiming(t *testing.T) {
	receivedAt := time.Date(2026, 8, 17, 12, 0, 0, 0, time.UTC)
	u := &UI{locale: "en"}
	tests := []struct {
		name     string
		body     string
		expected string
	}{
		{
			"zero",
			`{"errors":["Slow down"],"error_type":"rate_limit","extras":{"wait_seconds":0}}`,
			"The server reports that you can retry now.",
		},
		{
			"human",
			`{"errors":["Slow down"],"error_type":"rate_limit","extras":{"time_left":"about 2 minutes"}}`,
			"Retry available in about 2 minutes.",
		},
		{
			"missing",
			`{"errors":["Slow down"],"error_type":"rate_limit"}`,
			"The server did not provide a retry time.",
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			message := u.errorMessage(&discourse.HTTPError{
				Status: http.StatusTooManyRequests, Body: []byte(test.body), ReceivedAt: receivedAt,
			}, receivedAt)
			if !strings.Contains(message, test.expected) {
				t.Fatalf("message missing %q: %q", test.expected, message)
			}
		})
	}
}

func TestScreenRendererBuildsCompleteCharmFrame(t *testing.T) {
	content := normalizeScreen([]string{"one", "two"}, 5, 3)
	if content != "one  \ntwo  \n     " {
		t.Fatalf("normalized frame = %q", content)
	}
}

func TestCharmTerminalModelOwnsResizeInputCursorAndQueries(t *testing.T) {
	state := &terminalState{inputs: make(chan terminalInput, 4), ready: make(chan struct{}), done: make(chan error, 1)}
	state.mouse.Store(true)
	model := terminalModel{state: state}

	updated, _ := model.Update(tea.WindowSizeMsg{Width: 149, Height: 40})
	model = updated.(terminalModel)
	if width, height := int(state.width.Load()), int(state.height.Load()); width != 149 || height != 40 {
		t.Fatalf("Charm resize = %dx%d", width, height)
	}

	updated, _ = model.Update(tea.KeyPressMsg(tea.Key{Code: tea.KeyUp}))
	model = updated.(terminalModel)
	if input := <-state.inputs; input.msg.(tea.KeyPressMsg).String() != "up" {
		t.Fatalf("Charm key event = %#v", input.msg)
	}
	updated, _ = model.Update(tea.MouseClickMsg{X: 4, Y: 2, Button: tea.MouseLeft})
	model = updated.(terminalModel)
	if input := <-state.inputs; input.msg.(tea.MouseClickMsg).X != 4 {
		t.Fatalf("Charm mouse event = %#v", input.msg)
	}

	progress := tea.NewProgressBar(tea.ProgressBarDefault, 75)
	updated, _ = model.Update(terminalRenderMsg{content: "themed", cursorX: 3, cursorY: 2, progress: progress})
	model = updated.(terminalModel)
	view := model.View()
	if view.Content != "themed" || !view.AltScreen || view.MouseMode != tea.MouseModeAllMotion || view.Cursor == nil || view.Cursor.X != 3 || view.Cursor.Y != 2 || view.ProgressBar.Value != 75 {
		t.Fatalf("Charm view = %#v", view)
	}

	// A renderer invalidation must retain the current model. Raw terminal
	// graphics are drawn outside the cell renderer and would otherwise be
	// immediately covered by a blank frame.
	updated, _ = model.Update(terminalInvalidateMsg{})
	model = updated.(terminalModel)
	if stripANSI(model.View().Content) != "themed" || model.View().Content == "themed" {
		t.Fatalf("renderer invalidation cleared model content: %#v", model.View())
	}

	response := make(chan any, 1)
	updated, cmd := model.Update(terminalQueryMsg{
		sequence: "\x1b[c",
		accept: func(msg any) bool {
			_, ok := msg.(ultraviolet.PrimaryDeviceAttributesEvent)
			return ok
		},
		response: response,
	})
	model = updated.(terminalModel)
	if raw, ok := cmd().(tea.RawMsg); !ok || raw.Msg != "\x1b[c" {
		t.Fatalf("Charm raw query command = %#v", raw)
	}
	attributes := ultraviolet.PrimaryDeviceAttributesEvent{1, 4, 6}
	updated, _ = model.Update(attributes)
	model = updated.(terminalModel)
	if got := (<-response).(ultraviolet.PrimaryDeviceAttributesEvent); len(got) != 3 || got[1] != 4 || len(model.queries) != 0 {
		t.Fatalf("Charm query response = %#v, pending=%d", got, len(model.queries))
	}

	timedOut := make(chan any, 1)
	updated, _ = model.Update(terminalQueryMsg{sequence: "query", accept: func(any) bool { return true }, response: timedOut})
	model = updated.(terminalModel)
	updated, _ = model.Update(terminalCancelQueryMsg{response: timedOut})
	model = updated.(terminalModel)
	if len(model.queries) != 0 {
		t.Fatalf("timed-out terminal query was retained: %#v", model.queries)
	}
}

func TestTabRailsStayCompleteAndResponsive(t *testing.T) {
	specs := []tabSpec{
		{id: "latest", label: "Latest", short: "LAT", micro: "L", selected: true, enabled: true},
		{id: "unread", label: "Unread", short: "UNR", micro: "U", enabled: true},
		{id: "private", label: "Private Messages", short: "PRI", micro: "P", enabled: true},
		{id: "hot", label: "Hot", short: "HOT", micro: "H", enabled: true},
		{id: "new", label: "New", short: "NEW", micro: "N", enabled: true},
		{id: "top", label: "Top", short: "TOP", micro: "T", enabled: true},
	}
	for _, width := range []int{6, 12, 30, 60, 100} {
		rail := layoutTabRail(specs, width)
		if len(rail.tabs) != len(specs) {
			t.Fatalf("width %d rendered %d tabs, want %d: %#v", width, len(rail.tabs), len(specs), rail)
		}
		if rail.width > width {
			t.Fatalf("width %d produced rail width %d", width, rail.width)
		}
		for _, tab := range rail.tabs {
			if tab.x < 0 || tab.x+tab.width > width {
				t.Fatalf("width %d tab %q has invalid geometry %#v", width, tab.id, tab)
			}
		}
	}
}

func TestControlsFooterIsResponsiveAndMouseClickable(t *testing.T) {
	t.Setenv("TERMCOURSE_COLOR_MODE", "truecolor")
	value := "arrows: move | ↵, 1-0: open | f: filter | g: refresh | q: quit"
	for _, width := range []int{20, 40, 100} {
		controls := layoutControls(value, width)
		if len(controls) != 5 {
			t.Fatalf("width %d rendered %d controls, want 5: %#v", width, len(controls), controls)
		}
		for _, control := range controls {
			if control.x < 0 || control.x+control.width > width {
				t.Fatalf("width %d control has invalid geometry: %#v", width, control)
			}
		}
	}

	u := &UI{style: NewStyle(testTheme(), &bytes.Buffer{}), locale: "en", mouseEnabled: true}
	screen := make([]string, 8)
	u.placeControlsFooter(screen, value, 140)
	normal := screen[7]
	if !strings.Contains(stripANSI(screen[7]), "(F) FILTER") {
		t.Fatalf("full footer did not render button labels: %q", stripANSI(screen[7]))
	}
	if !strings.HasSuffix(stripANSI(screen[7]), "THEME // TEST") {
		t.Fatalf("theme status is not anchored at bottom right: %q", stripANSI(screen[7]))
	}
	wanted := map[string]bool{"t": false, "enter": false, "f": false, "g": false, "q": false}
	for _, region := range u.mouseRegions {
		if _, ok := wanted[region.key]; !ok {
			continue
		}
		if key := u.mouseKey(tea.MouseClickMsg{X: region.x0, Y: region.y0, Button: tea.MouseLeft}); key != region.key {
			t.Fatalf("footer region %q produced %q", region.key, key)
		}
		wanted[region.key] = true
	}
	for key, found := range wanted {
		if !found {
			t.Fatalf("footer button %q was not clickable: %#v", key, u.mouseRegions)
		}
	}
	var filter mouseRegion
	for _, region := range u.mouseRegions {
		if region.key == "f" {
			filter = region
			break
		}
	}
	if key := u.mouseKey(tea.MouseMotionMsg{X: filter.x0, Y: filter.y0}); key != keyHoverChanged || u.hoveredControl != "f" {
		t.Fatalf("filter hover = %q, %q", key, u.hoveredControl)
	}
	u.resetMouseLayout()
	u.placeControlsFooter(screen, value, 140)
	if screen[7] == normal {
		t.Fatalf("hover did not change footer appearance: %q", screen[7])
	}
	if key := u.mouseKey(tea.MouseMotionMsg{X: 99, Y: 0}); key != keyHoverChanged || u.hoveredControl != "" {
		t.Fatalf("leaving controls did not clear hover: %q, %q", key, u.hoveredControl)
	}
}

func TestThemeControlCyclesConfiguredCatalog(t *testing.T) {
	first := testTheme()
	first.Name = "first"
	second := testTheme()
	second.Name = "second"
	second.Border = "#1f8f5f"
	output := &bytes.Buffer{}
	terminal := NewTerminal(nil, output)
	u := &UI{
		terminal: terminal, style: NewStyle(first, output), locale: "en",
		options: Options{Theme: first, Themes: []theme.Theme{first, second}, Output: output},
	}
	terminal.state.inputs <- terminalInput{msg: tea.KeyPressMsg(tea.Key{Code: 't', Text: "t"})}
	key, err := u.readKey(time.Second)
	if err != nil || key != keyThemeChanged || u.style.Theme.Name != "second" {
		t.Fatalf("theme key = %q, %v; theme = %q", key, err, u.style.Theme.Name)
	}
	terminal.state.inputs <- terminalInput{msg: tea.KeyPressMsg(tea.Key{Code: 't', Text: "t"})}
	key, err = u.readKey(time.Second)
	if err != nil || key != keyThemeChanged || u.style.Theme.Name != "first" {
		t.Fatalf("wrapped theme key = %q, %v; theme = %q", key, err, u.style.Theme.Name)
	}
}

func TestTabsAndControlsUseThemeStructuralColour(t *testing.T) {
	t.Setenv("TERMCOURSE_COLOR_MODE", "truecolor")
	style := NewStyle(testTheme(), &bytes.Buffer{})
	if style.tab.GetForeground() != style.border.GetForeground() {
		t.Fatalf("tab foreground %v does not use structural theme colour %v", style.tab.GetForeground(), style.border.GetForeground())
	}
	if style.tabActive.GetBackground() != style.border.GetForeground() {
		t.Fatalf("active tab background %v does not use structural theme colour %v", style.tabActive.GetBackground(), style.border.GetForeground())
	}
	if style.control.GetForeground() != style.border.GetForeground() {
		t.Fatalf("control foreground %v does not use structural theme colour %v", style.control.GetForeground(), style.border.GetForeground())
	}
	if style.controlHot.GetForeground() != style.roles[roleAccent].GetForeground() {
		t.Fatalf("hover foreground %v does not use accent colour %v", style.controlHot.GetForeground(), style.roles[roleAccent].GetForeground())
	}
	rail := layoutTabRail([]tabSpec{
		{id: "topics", label: "Topics", short: "TPS", micro: "T", selected: true, enabled: true},
		{id: "search", label: "Search", short: "SRC", micro: "S", enabled: true},
	}, 40)
	if style.renderTabRail(rail) == style.renderTabRail(rail, "search") {
		t.Fatal("hover did not change tab appearance")
	}
}

func TestNavigationTabsRenderAndHitTestFromSameGeometry(t *testing.T) {
	t.Setenv("TERMCOURSE_COLOR_MODE", "truecolor")
	style := NewStyle(testTheme(), &bytes.Buffer{})
	u := &UI{
		style: style, locale: "en", displayURL: "community.example", mouseEnabled: true,
		options: Options{Username: "member"}, notificationUnread: 3, pmUnread: 2,
	}
	header := u.navigationHeader("Topic List", primaryTopics, "topics", "top", "monthly", nil, 88, 24)
	for index, line := range header {
		if visibleWidth(line) != 88 {
			t.Fatalf("header line %d width = %d: %q", index, visibleWidth(line), line)
		}
	}
	plain := make([]string, len(header))
	for index, line := range header {
		plain[index] = stripANSI(line)
	}
	if strings.TrimSpace(plain[1]) != "" {
		t.Fatalf("masthead spacer row = %q", plain[1])
	}
	if !strings.Contains(plain[2], "╭ TOPICS ╮") || !strings.Contains(plain[2], "╭ SEARCH ╮") {
		t.Fatalf("primary screen rail is not above the panel: %#v", plain)
	}
	if !strings.Contains(plain[3], "TOP") || !strings.HasPrefix(plain[4], "╭─ TOPIC LIST") {
		t.Fatalf("context rail/header order is wrong: %#v", plain)
	}

	wanted := map[string]bool{
		primaryTabKey(primarySearch): false,
		contextTabKey("private"):     false,
		contextTabKey("period"):      false,
	}
	for _, region := range u.mouseRegions {
		if _, ok := wanted[region.key]; !ok {
			continue
		}
		key := u.mouseKey(tea.MouseClickMsg{X: region.x0, Y: region.y0, Button: tea.MouseLeft})
		if key != region.key {
			t.Fatalf("region %q produced %q at (%d,%d)", region.key, key, region.x0, region.y0)
		}
		wanted[region.key] = true
	}
	for key, hit := range wanted {
		if !hit {
			t.Fatalf("navigation region %q was not rendered: %#v", key, u.mouseRegions)
		}
	}
}

func TestWideMastheadKeepsPaddingBeforePersistentRails(t *testing.T) {
	t.Setenv("TERMCOURSE_COLOR_MODE", "truecolor")
	u := &UI{
		style: NewStyle(testTheme(), &bytes.Buffer{}), locale: "en", displayURL: "community.example",
		options: Options{Username: "member"},
	}
	header := u.navigationHeader("Search", primarySearch, "search", "results", "", nil, 100, 32)
	if len(header) < 8 || strings.TrimSpace(stripANSI(header[3])) != "" {
		t.Fatalf("wide masthead does not have one padding row before rails: %#v", header)
	}
	if !strings.Contains(stripANSI(header[4]), "SEARCH") || !strings.Contains(stripANSI(header[5]), "RESULTS") || !strings.HasPrefix(stripANSI(header[6]), "╭─ SEARCH") {
		t.Fatalf("wide rail/header order is wrong: %#v", header)
	}
}

func TestSearchResultsHeaderKeepsQueryInsideStableBox(t *testing.T) {
	t.Setenv("TERMCOURSE_COLOR_MODE", "truecolor")
	u := &UI{
		style: NewStyle(testTheme(), &bytes.Buffer{}), locale: "en", displayURL: "community.example",
		options: Options{Username: "member"},
	}
	header := u.searchResultsHeader("rate limit debugging", 88, 24)
	plain := make([]string, len(header))
	for index := range header {
		plain[index] = stripANSI(header[index])
	}
	joined := strings.Join(plain, "\n")
	if !strings.Contains(joined, "╭─ SEARCH ") {
		t.Fatalf("search border has no stable title: %q", joined)
	}
	if strings.Contains(joined, "╭─ SEARCH: rate limit debugging") {
		t.Fatalf("query leaked into search border: %q", joined)
	}
	if !strings.Contains(joined, "│ rate limit debugging") {
		t.Fatalf("query is not inside search header: %q", joined)
	}
}

func TestTopicsListSuppressesRedundantHeaderAndUsesRoundedBody(t *testing.T) {
	t.Setenv("TERMCOURSE_COLOR_MODE", "truecolor")
	terminal := NewTerminal(nil, &bytes.Buffer{})
	terminal.state.width.Store(88)
	terminal.state.height.Store(24)
	u := &UI{
		terminal: terminal, renderer: NewScreenRenderer(terminal), style: NewStyle(testTheme(), &bytes.Buffer{}),
		locale: "en", mouseEnabled: true,
	}
	header := u.navigationHeader("", primaryTopics, "topics", "latest", "", nil, 88, 24)
	if len(header) != 4 {
		t.Fatalf("Topics chrome has %d rows, want title, spacer, and two rails: %#v", len(header), header)
	}
	box := u.style.Box([]string{"A topic"}, 40)
	if len(box) != 3 || !strings.HasPrefix(stripANSI(box[0]), "╭") || !strings.HasPrefix(stripANSI(box[1]), "│") || !strings.HasPrefix(stripANSI(box[2]), "╰") {
		t.Fatalf("topic body is not rounded: %#v", box)
	}

	terminal.state.height.Store(12)
	u.renderTopicList([]discourse.JSON{{"id": 1, "title": "One", "posts_count": 1}}, 0, "latest", "monthly", false)
	foundRow := false
	for _, region := range u.mouseRegions {
		if region.key == rowKey(0) {
			foundRow = true
			if region.y0 != 6 {
				t.Fatalf("topic row y = %d, want 6 below the rounded top edge", region.y0)
			}
		}
	}
	if !foundRow {
		t.Fatalf("boxed topic row lost mouse geometry: %#v", u.mouseRegions)
	}
}

func TestPrimaryAndContextRailsClassifyEveryScreen(t *testing.T) {
	u := &UI{
		style: NewStyle(testTheme(), &bytes.Buffer{}), locale: "en",
	}
	specs := u.primarySpecs(primaryCompose)
	if len(specs) != int(primaryTabCount) {
		t.Fatalf("primary screens = %d, want %d: %#v", len(specs), primaryTabCount, specs)
	}
	wanted := []string{"Topics", "Search", "Notifications", "Compose"}
	for index, label := range wanted {
		if specs[index].label != label || !specs[index].enabled || specs[index].selected != (index == int(primaryCompose)) {
			t.Fatalf("primary screen %d = %#v, want label %q", index, specs[index], label)
		}
	}
	compose := u.contextNavigationLine("compose", "reply_post", "", 80)
	if len(compose.tabs) != 5 || compose.tabs[4].id != contextTabKey("reply_post") || !compose.tabs[4].selected {
		t.Fatalf("compose subclasses = %#v", compose.tabs)
	}
}

func TestImageDrillDownRetainsOriginatingPrimaryDestination(t *testing.T) {
	u := &UI{
		style: NewStyle(testTheme(), &bytes.Buffer{}), locale: "en", activePrimary: primarySearch,
		options: Options{Username: "member"}, mouseEnabled: true,
	}
	header := u.imageScreenHeader(88, 24)
	if u.activePrimary != primarySearch {
		t.Fatalf("image changed active destination to %v", u.activePrimary)
	}
	plain := strings.Join(header, "\n")
	if !strings.Contains(stripANSI(plain), "╭ SEARCH ╮") {
		t.Fatalf("image header did not retain Search destination: %q", stripANSI(plain))
	}
	if len(u.primarySpecs(primarySearch)) != 4 {
		t.Fatalf("content drill-down leaked into primary destinations: %#v", u.primarySpecs(primarySearch))
	}
}

func TestTabbedListScreensRemainSafeAtShortTerminalHeights(t *testing.T) {
	t.Setenv("TERMCOURSE_COLOR_MODE", "truecolor")
	for _, height := range []int{3, 5, 8} {
		terminal := NewTerminal(nil, &bytes.Buffer{})
		terminal.state.width.Store(52)
		terminal.state.height.Store(int32(height))
		u := &UI{
			terminal: terminal, renderer: NewScreenRenderer(terminal), style: NewStyle(testTheme(), &bytes.Buffer{}),
			locale: "en", displayURL: "community.example", mouseEnabled: true, options: Options{Username: "member"},
		}
		t.Run(strconv.Itoa(height), func(t *testing.T) {
			u.renderTopicList([]discourse.JSON{{"id": 1, "title": "One", "posts_count": 1}}, 0, "latest", "monthly", false)
			u.renderSearch("one", []discourse.JSON{{"topic_id": 1, "blurb": "One"}}, map[int]string{1: "Topic"}, 0)
			u.renderNotifications([]discourse.JSON{{"topic_id": 1, "data": discourse.JSON{"display_username": "member"}}}, 0, "all", false)
		})
	}
}

func TestTabKeyboardAndMouseWheelUseUnifiedInput(t *testing.T) {
	terminal := NewTerminal(nil, &bytes.Buffer{})
	u := &UI{terminal: terminal, activePrimary: primaryTopics, mouseEnabled: true}
	terminal.state.inputs <- terminalInput{msg: tea.KeyPressMsg(tea.Key{Code: tea.KeyTab})}
	key, err := u.readKey(time.Second)
	if err != nil || key != primaryTabKey(primarySearch) {
		t.Fatalf("Tab input = %q, %v", key, err)
	}
	terminal.state.inputs <- terminalInput{msg: tea.KeyPressMsg(tea.Key{Code: tea.KeyTab, Mod: tea.ModShift})}
	key, err = u.readKey(time.Second)
	if err != nil || key != primaryTabKey(primaryCompose) {
		t.Fatalf("Shift+Tab input = %q, %v", key, err)
	}
	terminal.state.inputs <- terminalInput{msg: tea.MouseWheelMsg{Button: tea.MouseWheelDown}}
	key, err = u.readKey(time.Second)
	if err != nil || key != "wheeldown" {
		t.Fatalf("wheel input = %q, %v", key, err)
	}
}

func TestSearchPromptAllowsPrimaryTabNavigation(t *testing.T) {
	terminal := NewTerminal(nil, &bytes.Buffer{})
	terminal.state.width.Store(88)
	terminal.state.height.Store(24)
	u := &UI{
		terminal: terminal, renderer: NewScreenRenderer(terminal), style: NewStyle(testTheme(), &bytes.Buffer{}),
		locale: "en", activePrimary: primarySearch, mouseEnabled: true,
	}
	terminal.state.inputs <- terminalInput{msg: tea.KeyPressMsg(tea.Key{Code: tea.KeyTab})}
	if query := u.promptSingleLine("ui.search.title", "ui.search.prompt", "Search: ", primarySearch, "search", "query"); query != "" {
		t.Fatalf("search query = %q, want navigation", query)
	}
	if u.requestedPrimary == nil || *u.requestedPrimary != primaryNotifications {
		t.Fatalf("requested primary = %v, want Notifications", u.requestedPrimary)
	}
	if u.navigationLocked || u.primaryNavAllowed {
		t.Fatalf("prompt navigation state was not restored: locked=%v allowed=%v", u.navigationLocked, u.primaryNavAllowed)
	}
}

func TestBlankDraftTitlePromptAllowsPrimaryTabNavigation(t *testing.T) {
	terminal := NewTerminal(nil, &bytes.Buffer{})
	terminal.state.width.Store(88)
	terminal.state.height.Store(24)
	u := &UI{
		terminal: terminal, renderer: NewScreenRenderer(terminal), style: NewStyle(testTheme(), &bytes.Buffer{}),
		locale: "en", activePrimary: primaryCompose, mouseEnabled: true,
	}
	terminal.state.inputs <- terminalInput{msg: tea.KeyPressMsg(tea.Key{Code: tea.KeyTab})}
	u.promptSingleLine("ui.composer.new_topic_title", "ui.composer.enter_title", "Title: ", primaryCompose, "compose", "title")
	if u.requestedPrimary == nil || *u.requestedPrimary != primaryTopics {
		t.Fatalf("blank Compose prompt did not wrap navigation to Topics: %v", u.requestedPrimary)
	}
}

func TestEnteredDraftTitlePromptKeepsPrimaryNavigationLocked(t *testing.T) {
	terminal := NewTerminal(nil, &bytes.Buffer{})
	terminal.state.width.Store(88)
	terminal.state.height.Store(24)
	u := &UI{
		terminal: terminal, renderer: NewScreenRenderer(terminal), style: NewStyle(testTheme(), &bytes.Buffer{}),
		locale: "en", activePrimary: primaryCompose, mouseEnabled: true,
	}
	terminal.state.inputs <- terminalInput{msg: tea.KeyPressMsg(tea.Key{Code: 'D', Text: "D"})}
	terminal.state.inputs <- terminalInput{msg: tea.KeyPressMsg(tea.Key{Code: tea.KeyTab})}
	terminal.state.inputs <- terminalInput{msg: tea.KeyPressMsg(tea.Key{Code: tea.KeyEscape})}
	u.promptSingleLine("ui.composer.new_topic_title", "ui.composer.enter_title", "Title: ", primaryCompose, "compose", "title")
	if u.requestedPrimary != nil {
		t.Fatalf("entered draft title allowed primary navigation to %v", *u.requestedPrimary)
	}
}

func TestPostRenderingRetainsMouseSelectionGeometry(t *testing.T) {
	u := &UI{style: NewStyle(testTheme(), &bytes.Buffer{}), locale: "en"}
	posts := []discourse.JSON{
		{"username": "one", "raw": "First"},
		{"username": "two", "raw": "Second\n\nwith detail"},
		{"username": "three", "raw": "Third"},
	}
	lines, indexes := u.postListLines(posts, 1, 0, 18, 52)
	if len(lines) != len(indexes) {
		t.Fatalf("post lines=%d, hit indexes=%d", len(lines), len(indexes))
	}
	found := map[int]bool{}
	for _, index := range indexes {
		if index >= 0 {
			found[index] = true
		}
	}
	for index := range posts {
		if !found[index] {
			t.Fatalf("post %d has no selectable rendered cells: %#v", index, indexes)
		}
	}
}

func TestBubblesInputsPreserveEditingAndTermcourseTheme(t *testing.T) {
	t.Setenv("TERMCOURSE_COLOR_MODE", "truecolor")
	style := NewStyle(testTheme(), &bytes.Buffer{})
	u := &UI{style: style}

	input := textinput.New()
	input.Prompt = "Search: "
	u.styleTextInput(&input)
	_ = input.Focus()
	input, _ = input.Update(tea.KeyPressMsg(tea.Key{Code: 'a', Text: "a"}))
	if input.Value() != "a" || !strings.Contains(input.View(), "38;2;108;196;255") {
		t.Fatalf("themed Bubbles text input = %q, value=%q", input.View(), input.Value())
	}

	area := textarea.New()
	area.ShowLineNumbers = false
	u.styleTextArea(&area)
	_ = area.Focus()
	area, _ = area.Update(tea.PasteMsg{Content: "first\nsecond"})
	if area.Value() != "first\nsecond" || !strings.Contains(area.View(), "38;2;230;230;230") {
		t.Fatalf("themed Bubbles textarea = %q, value=%q", area.View(), area.Value())
	}
}

func TestDisplayWidthAndTruncation(t *testing.T) {
	if displayWidth("abc") != 3 || displayWidth("界") != 2 || displayWidth("e\u0301") != 1 || displayWidth("♥") != 1 {
		t.Fatalf("widths: ascii=%d wide=%d combining=%d heart=%d", displayWidth("abc"), displayWidth("界"), displayWidth("e\u0301"), displayWidth("♥"))
	}
	if actual := truncate("abcdefgh", 6); actual != "abc..." {
		t.Fatalf("truncate = %q", actual)
	}
}

func TestPadLinePreservesOSC8LinkTerminators(t *testing.T) {
	link := linkify("https://example.com", true)
	padded := padLine(link, 24)
	if !strings.Contains(padded, "\a") || visibleWidth(padded) != 24 {
		t.Fatalf("padded OSC8 link = %q width=%d", padded, visibleWidth(padded))
	}
}

func TestHeaderLineRightAlignsStatusAndStaysWithinWidth(t *testing.T) {
	const width = 48
	status := "Logged in: member · [3]"
	line := headerLine("arrows: move · q: quit", status, width)
	if visibleWidth(line) != width || !strings.HasSuffix(stripANSI(line), status) {
		t.Fatalf("header line = %q, width=%d", line, visibleWidth(line))
	}

	narrow := headerLine("controls", "Logged in: a-very-long-username · [12]", 12)
	if visibleWidth(narrow) != 12 {
		t.Fatalf("narrow header width = %d: %q", visibleWidth(narrow), narrow)
	}
}

func TestMarkdownRenderingUsesGFMAndTerminalLinks(t *testing.T) {
	lines := wrapLines("## Heading\n\n- first\n- second\n\n[site](https://example.com)", 40, true)
	joined := strings.Join(lines, "\n")
	for _, expected := range []string{"## Heading", "first", "second", "\x1b]8;", "https://example.com"} {
		if !strings.Contains(joined, expected) {
			t.Fatalf("rendered Markdown %q missing %q", joined, expected)
		}
	}
	for _, line := range lines {
		if visibleWidth(line) > 40 {
			t.Fatalf("Markdown line width = %d: %q", visibleWidth(line), line)
		}
	}
}

func TestStyleUsesRequestedColorProfile(t *testing.T) {
	t.Setenv("TERMCOURSE_COLOR_MODE", "16")
	style := NewStyle(testTheme(), &bytes.Buffer{})
	styled := style.Text("value", rolePrimary)
	if !strings.Contains(styled, "\x1b[") || strings.Contains(styled, "38;2;") {
		t.Fatalf("16-color styled value = %q", styled)
	}
	for _, line := range style.Box([]string{"body"}, 20) {
		if visibleWidth(line) != 20 {
			t.Fatalf("box line width = %d: %q", visibleWidth(line), line)
		}
	}
}

func TestSemanticHeaderStylesUseHeaderBackgroundAndSeparators(t *testing.T) {
	t.Setenv("TERMCOURSE_COLOR_MODE", "truecolor")
	style := NewStyle(testTheme(), &bytes.Buffer{})
	joined := strings.Join(style.HeaderBox("Section", []string{"left | right", "--------", "status"}, 20), "\n")
	for _, sequence := range []string{
		"48;2;31;31;31m",
		"38;2;111;111;111;48;2;31;31;31m",
		"\x1b[38;2;168;168;168m",
	} {
		if !strings.Contains(joined, sequence) {
			t.Fatalf("themed header %q missing %q", joined, sequence)
		}
	}
}

func TestPanelsUseRoundedIntegratedTitlesAndResponsiveBranding(t *testing.T) {
	t.Setenv("TERMCOURSE_COLOR_MODE", "truecolor")
	style := NewStyle(testTheme(), &bytes.Buffer{})
	compact := style.AppHeader("Latest Topics", "community.example", []string{"arrows: move | q: quit", "Logged in: member"}, 52, 24)
	plain := stripANSI(strings.Join(compact, "\n"))
	if !strings.HasPrefix(plain, "▰ TERMCOURSE ▰") || !strings.Contains(plain, "╭─ LATEST TOPICS ") || !strings.HasSuffix(plain, "╰"+strings.Repeat("─", 50)+"╯") {
		t.Fatalf("compact app header =\n%s", plain)
	}
	if !strings.Contains(plain, " · ") || strings.Contains(plain, " | ") || strings.Contains(plain, " // ") {
		t.Fatalf("control separators were not restyled: %q", plain)
	}
	for index, line := range compact {
		if visibleWidth(line) != 52 {
			t.Fatalf("compact line %d width = %d: %q", index, visibleWidth(line), line)
		}
	}

	wide := stripANSI(strings.Join(style.AppHeader("Latest", "community.example", []string{"controls", "status"}, 90, 32), "\n"))
	if !strings.Contains(wide, "▀█▀ █▀▀ █▀█ █▀▄▀█") || !strings.Contains(wide, "◉ DISCOURSE TERMINAL · TEST") || !strings.Contains(wide, "● ONLINE") {
		t.Fatalf("wide app header =\n%s", wide)
	}
}

func TestPanelsRemainExactWidthAtTinyTerminalSizes(t *testing.T) {
	t.Setenv("TERMCOURSE_COLOR_MODE", "truecolor")
	style := NewStyle(testTheme(), &bytes.Buffer{})
	for width := 1; width <= 12; width++ {
		lines := style.HeaderBox("A title that is too long", []string{"content"}, width)
		for index, line := range lines {
			if visibleWidth(line) != width {
				t.Fatalf("width %d line %d rendered at %d: %q", width, index, visibleWidth(line), line)
			}
		}
	}
}

func TestProgressPanelUsesBlockGaugeAndThemeRoles(t *testing.T) {
	t.Setenv("TERMCOURSE_COLOR_MODE", "truecolor")
	style := NewStyle(testTheme(), &bytes.Buffer{})
	lines := style.ProgressBox("Read Progress", 3, 4, 32)
	plain := stripANSI(strings.Join(lines, "\n"))
	if !strings.Contains(plain, "╭─ READ PROGRESS ") || !strings.Contains(plain, "███") ||
		!strings.Contains(plain, "░") || !strings.Contains(plain, "3/4") {
		t.Fatalf("progress panel =\n%s", plain)
	}
	for index, line := range lines {
		if visibleWidth(line) != 32 {
			t.Fatalf("progress line %d width = %d: %q", index, visibleWidth(line), line)
		}
	}
	joined := strings.Join(lines, "\n")
	if !strings.Contains(joined, "38;2;108;196;255") || !strings.Contains(joined, "38;2;111;111;111") {
		t.Fatalf("progress panel does not use accent and separator roles: %q", joined)
	}
}

func TestTopicProgressClickMapsTrackEndpointsAndHover(t *testing.T) {
	t.Setenv("TERMCOURSE_COLOR_MODE", "truecolor")
	u := &UI{style: NewStyle(testTheme(), &bytes.Buffer{}), locale: "en", mouseEnabled: true}
	u.addProgressRegions(10, 37, 100, 80, 24)
	var first, last mouseRegion
	for _, region := range u.mouseRegions {
		if region.key == progressKey(0) {
			first = region
		}
		if region.key == progressKey(99) {
			last = region
		}
	}
	if first.x1 <= first.x0 || last.x1 <= last.x0 {
		t.Fatalf("progress endpoints are missing: %#v", u.mouseRegions)
	}
	if key := u.mouseKey(tea.MouseClickMsg{X: first.x0, Y: first.y0, Button: tea.MouseLeft}); key != progressKey(0) {
		t.Fatalf("first progress cell produced %q", key)
	}
	if key := u.mouseKey(tea.MouseClickMsg{X: last.x1 - 1, Y: last.y0, Button: tea.MouseLeft}); key != progressKey(99) {
		t.Fatalf("last progress cell produced %q", key)
	}
	u.hoveredControl = ""
	normal := strings.Join(u.progressFooter(37, 100, 80), "\n")
	if key := u.mouseKey(tea.MouseMotionMsg{X: first.x0, Y: first.y0}); key != keyHoverChanged {
		t.Fatalf("progress hover produced %q", key)
	}
	hovered := strings.Join(u.progressFooter(37, 100, 80), "\n")
	if normal == hovered {
		t.Fatal("progress hover did not change the track appearance")
	}
}

func TestTopicProgressUsesCompletePostStream(t *testing.T) {
	stream := make([]any, 100)
	for index := range stream {
		stream[index] = index + 1
	}
	topic := discourse.JSON{"post_stream": discourse.JSON{"stream": stream}}
	loaded := []discourse.JSON{
		{"id": 1, "post_number": 1},
		{"id": 51, "post_number": 51},
	}
	current, total := topicProgressPosition(topic, loaded, 1)
	if current != 51 || total != 100 {
		t.Fatalf("progress = %d/%d, want 51/100", current, total)
	}
}

type topicProgressClient struct {
	Client
	topicID    int
	postIDs    []int
	includeRaw bool
	response   discourse.JSON
}

func (c *topicProgressClient) TopicPosts(topicID int, postIDs []int, includeRaw bool) (discourse.JSON, error) {
	c.topicID = topicID
	c.postIDs = append([]int(nil), postIDs...)
	c.includeRaw = includeRaw
	return c.response, nil
}

func TestTopicProgressFetchesChunkAroundUnloadedPosition(t *testing.T) {
	stream := make([]any, 100)
	for index := range stream {
		stream[index] = index + 1
	}
	incoming := make([]any, 21)
	for index := range incoming {
		id := index + 41
		incoming[index] = discourse.JSON{"id": id, "post_number": id}
	}
	topic := discourse.JSON{"post_stream": discourse.JSON{
		"stream": stream,
		"posts":  []any{discourse.JSON{"id": 1, "post_number": 1}},
	}}
	client := &topicProgressClient{response: discourse.JSON{
		"post_stream": discourse.JSON{"posts": incoming},
	}}
	u := &UI{client: client}

	selected, err := u.seekTopicProgress(314, topic, 50)
	if err != nil {
		t.Fatal(err)
	}
	if client.topicID != 314 || !client.includeRaw {
		t.Fatalf("request = topic %d, include raw %t", client.topicID, client.includeRaw)
	}
	if len(client.postIDs) != 21 || client.postIDs[0] != 41 || client.postIDs[20] != 61 {
		t.Fatalf("requested post IDs = %#v", client.postIDs)
	}
	loaded := posts(topic)
	if selected != 10 || discourse.Int(loaded[selected]["id"]) != 51 {
		t.Fatalf("selected loaded post = %d/%v", selected, loaded)
	}
}

func TestTopicRowsKeepThemeColorsAcrossResponsiveBreakpoints(t *testing.T) {
	t.Setenv("TERMCOURSE_COLOR_MODE", "truecolor")
	var output bytes.Buffer
	style := NewStyle(testTheme(), &output)
	u := &UI{style: style, emojiEnabled: true}
	topic := map[string]any{
		"title": "Responsive topic", "posts_count": 8, "category_name": "Support", "views": 1234,
	}

	for _, test := range []struct {
		width int
		mode  string
	}{
		{124, "compact"},
		{125, "category"},
		{149, "stats"},
	} {
		t.Run(test.mode, func(t *testing.T) {
			row := u.topicRow(topic, 1, test.width, "latest", test.mode)
			for _, role := range []textRole{roleListNumber, roleListText, roleListMeta} {
				sequence := style.Text("x", role)
				sequence = sequence[:strings.Index(sequence, "x")]
				if !strings.Contains(row, sequence) {
					t.Fatalf("%s row lost role %d styling after resize: %q", test.mode, role, row)
				}
			}
			if test.mode != "compact" && visibleWidth(row) != test.width {
				t.Fatalf("%s row width = %d, terminal width = %d", test.mode, visibleWidth(row), test.width)
			}
		})
	}
}

func TestEveryBuiltinThemeRendersInEveryExplicitColorMode(t *testing.T) {
	t.Setenv("XDG_CONFIG_HOME", t.TempDir())
	catalog, err := theme.Load("")
	if err != nil {
		t.Fatal(err)
	}
	for _, value := range catalog.All() {
		for _, mode := range []string{"truecolor", "256", "16"} {
			t.Run(value.Name+"/"+mode, func(t *testing.T) {
				t.Setenv("TERMCOURSE_COLOR_MODE", mode)
				style := NewStyle(value, &bytes.Buffer{})
				lines := style.HeaderBox("Section", []string{"left | right", "--------", "status"}, 24)
				lines = append(lines, style.Selected("selected"))
				joined := strings.Join(lines, "\n")
				if !strings.Contains(joined, "\x1b[") || style.ColorMode != mode {
					t.Fatalf("theme output = %q, color mode = %q", joined, style.ColorMode)
				}
				switch mode {
				case "truecolor":
					if !strings.Contains(joined, "38;2;") || !strings.Contains(joined, "48;2;") {
						t.Fatalf("truecolor output = %q", joined)
					}
				case "256":
					if !strings.Contains(joined, "38;5;") || !strings.Contains(joined, "48;5;") {
						t.Fatalf("256-color output = %q", joined)
					}
				case "16":
					if strings.Contains(joined, ";2;") || strings.Contains(joined, ";5;") {
						t.Fatalf("16-color output = %q", joined)
					}
				}
				for _, line := range lines[:5] {
					if visibleWidth(line) != 24 {
						t.Fatalf("header width = %d: %q", visibleWidth(line), line)
					}
				}
			})
		}
	}
}

func testTheme() theme.Theme {
	return theme.Theme{
		Name: "test", Primary: "#f2f2f2", Border: "#a8a8a8", HeaderBackground: "#1f1f1f",
		Separator: "#6f6f6f", Selected: "#2a5ea8", SelectedText: "#ffffff",
		ListNumber: "#f2f2f2", ListText: "#e6e6e6", PostUsername: "#b5b5b5",
		ListMeta: "#b5b5b5", Accent: "#6cc4ff",
	}
}

func TestImageExtractionAndModes(t *testing.T) {
	raw := "![one](upload://abc.png) /uploads/default/two.jpg https://x.test/three.webp?q=1"
	urls := extractImageURLs(raw, "https://meta.example")
	if len(urls) != 3 || urls[0] != "https://meta.example/uploads/short-url/abc.png" {
		t.Fatalf("URLs = %#v", urls)
	}
	t.Setenv("TERMCOURSE_IMAGE_MODE", "balanced")
	t.Setenv("TERMCOURSE_IMAGE_COLORS", "full")
	args := strings.Join(chafaArgs("/tmp/example.png", 40, 12, false), " ")
	for _, expected := range []string{"--symbols vhalf", "--colors full", "--optimize 5", "--work 5", "--size 40x12"} {
		if !strings.Contains(args, expected) {
			t.Fatalf("args %q missing %q", args, expected)
		}
	}
	t.Setenv("TERMCOURSE_IMAGE_MODE", "")
	if defaults := strings.Join(chafaArgs("/tmp/example.png", 32, 6, false), " "); !strings.Contains(defaults, "--symbols vhalf") || !strings.Contains(defaults, "--colors full") {
		t.Fatalf("default Chafa fallback is not colored and balanced: %q", defaults)
	}
}

func TestKittyThumbnailGeometryPreservesPixelAspectRatio(t *testing.T) {
	columns, rows := kittyCellGeometry(1600, 900, 48, 8, 8, 16)
	if columns != 28 || rows != 8 {
		t.Fatalf("landscape Kitty geometry = %dx%d", columns, rows)
	}
	columns, rows = kittyCellGeometry(600, 1200, 48, 8, 8, 16)
	if columns != 8 || rows != 8 {
		t.Fatalf("portrait Kitty geometry = %dx%d", columns, rows)
	}
	width, height := boundedPixelSize(8000, 4000, 8_000_000)
	if width != 4000 || height != 2000 {
		t.Fatalf("bounded Kitty pixels = %dx%d", width, height)
	}
}

func TestKittyPlaceholdersOccupyManagedTerminalCells(t *testing.T) {
	lines := kittyPlaceholderLines(0x123456, 4, 3)
	if len(lines) != 3 {
		t.Fatalf("Kitty placeholder rows = %d", len(lines))
	}
	for index, line := range lines {
		if visibleWidth(line) != 4 || !strings.Contains(line, "38;2;18;52;86m") {
			t.Fatalf("Kitty placeholder row %d = %q, width=%d", index, line, visibleWidth(line))
		}
	}
	if kittyPlaceholderLines(0, 4, 3) != nil || kittyPlaceholderLines(1, 0, 3) != nil {
		t.Fatal("invalid Kitty placeholder geometry was accepted")
	}
}

func TestKittyPlacementUsesChunkedPNGVirtualCells(t *testing.T) {
	source := image.NewRGBA(image.Rect(0, 0, 2, 2))
	source.Set(0, 0, color.RGBA{R: 255, A: 255})
	encoded, err := encodeKittyPlacement(source, 0x123456, 4, 3, func(sequence string) string {
		return "<passthrough>" + sequence + "</passthrough>"
	})
	if err != nil {
		t.Fatal(err)
	}
	for _, expected := range []string{"<passthrough>", "\x1b_G", "f=100", "i=1193046", "U=1", "c=4", "r=3", "a=T"} {
		if !strings.Contains(encoded, expected) {
			t.Fatalf("Kitty transmission %q missing %q", encoded, expected)
		}
	}
}

func TestSixelAndPixelResponses(t *testing.T) {
	if !parseDA1Sixel("\x1b[?1;2;4;6c") || parseDA1Sixel("\x1b[?1;2;6c") {
		t.Fatal("DA1 sixel detection mismatch")
	}
	width, height, ok := parsePixelResponse("\x1b[4;900;1440t", "4")
	if !ok || width != 1440 || height != 900 {
		t.Fatalf("pixel response = %dx%d/%v", width, height, ok)
	}
	width, height, ok = parseGraphicsResponse("\x1b[?2;0;1200;800S", "2")
	if !ok || width != 1200 || height != 800 {
		t.Fatalf("graphics response = %dx%d/%v", width, height, ok)
	}
}

func TestMergeTopicLists(t *testing.T) {
	target := map[string]any{"topic_list": map[string]any{"topics": []any{
		map[string]any{"id": 1}, map[string]any{"id": 2},
	}}}
	incoming := map[string]any{"topic_list": map[string]any{"topics": []any{
		map[string]any{"id": 2}, map[string]any{"id": 3},
	}, "more_topics_url": "/latest?page=2"}}
	mergeTopicLists(target, incoming, false)
	topics := topicList(target)
	if len(topics) != 3 || intNumber(topics[2]["id"]) != 3 {
		t.Fatalf("topics = %#v", topics)
	}
}
