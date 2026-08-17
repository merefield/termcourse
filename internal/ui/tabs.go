package ui

import (
	"strconv"
	"strings"
	"time"

	tea "charm.land/bubbletea/v2"
)

type primaryTabID int

const (
	primaryTopics primaryTabID = iota
	primarySearch
	primaryNotifications
	primaryCompose
	primaryTabCount
)

func (t primaryTabID) next(reverse bool) primaryTabID {
	if reverse {
		return (t - 1 + primaryTabCount) % primaryTabCount
	}
	return (t + 1) % primaryTabCount
}

func primaryTabKey(tab primaryTabID) string {
	return "__tab:" + strconv.Itoa(int(tab))
}

func parsePrimaryTabKey(key string) (primaryTabID, bool) {
	value, found := strings.CutPrefix(key, "__tab:")
	if !found {
		return primaryTopics, false
	}
	parsed, err := strconv.Atoi(value)
	if err != nil || parsed < 0 || parsed >= int(primaryTabCount) {
		return primaryTopics, false
	}
	return primaryTabID(parsed), true
}

func contextTabKey(value string) string { return "__context:" + value }

func parseContextTabKey(key string) (string, bool) {
	return strings.CutPrefix(key, "__context:")
}

func rowKey(index int) string { return "__row:" + strconv.Itoa(index) }

func parseRowKey(key string) (int, bool) {
	value, found := strings.CutPrefix(key, "__row:")
	if !found {
		return 0, false
	}
	parsed, err := strconv.Atoi(value)
	return parsed, err == nil
}

type tabSpec struct {
	id       string
	label    string
	short    string
	micro    string
	badge    int
	selected bool
	enabled  bool
}

type laidOutTab struct {
	id       string
	label    string
	x        int
	width    int
	selected bool
	enabled  bool
}

type tabRail struct {
	tabs      []laidOutTab
	separator string
	width     int
}

func layoutTabRail(specs []tabSpec, width int) tabRail {
	if width <= 0 || len(specs) == 0 {
		return tabRail{}
	}
	type tier struct {
		labels    []string
		separator string
	}
	build := func(format func(tabSpec) string, separator string) tier {
		labels := make([]string, len(specs))
		for index, spec := range specs {
			labels[index] = format(spec)
		}
		return tier{labels: labels, separator: separator}
	}
	badge := func(spec tabSpec) string {
		if spec.badge <= 0 {
			return ""
		}
		return " " + strconv.Itoa(spec.badge)
	}
	tiers := []tier{
		build(func(spec tabSpec) string { return "╭ " + strings.ToUpper(spec.label) + badge(spec) + " ╮" }, " "),
		build(func(spec tabSpec) string { return "╭" + strings.ToUpper(spec.short) + badge(spec) + "╮" }, " "),
		build(func(spec tabSpec) string { return strings.ToUpper(spec.micro) }, " "),
		build(func(spec tabSpec) string { return strings.ToUpper(spec.micro) }, ""),
	}
	chosen := tiers[len(tiers)-1]
	for _, candidate := range tiers {
		total := max(len(candidate.labels)-1, 0) * displayWidth(candidate.separator)
		for _, label := range candidate.labels {
			total += displayWidth(label)
		}
		if total <= width {
			chosen = candidate
			break
		}
	}

	rail := tabRail{separator: chosen.separator}
	x := 0
	for index, label := range chosen.labels {
		tabWidth := displayWidth(label)
		if x+tabWidth > width {
			break
		}
		rail.tabs = append(rail.tabs, laidOutTab{
			id: specs[index].id, label: label, x: x, width: tabWidth,
			selected: specs[index].selected, enabled: specs[index].enabled,
		})
		x += tabWidth
		if index != len(chosen.labels)-1 {
			x += displayWidth(chosen.separator)
		}
	}
	rail.width = min(x, width)
	return rail
}

func (s *Style) renderTabRail(rail tabRail) string {
	parts := make([]string, 0, len(rail.tabs))
	for _, tab := range rail.tabs {
		style := s.tab
		if !tab.enabled {
			style = s.roles[roleListMeta]
		}
		if tab.selected {
			style = s.tabActive
		}
		parts = append(parts, style.Render(tab.label))
	}
	return strings.Join(parts, s.tab.Render(rail.separator))
}

type mouseRegion struct {
	x0, x1 int
	y0, y1 int
	key    string
}

func (u *UI) resetMouseLayout() { u.mouseRegions = u.mouseRegions[:0] }

func (u *UI) addMouseRegion(x, y, width, height int, key string) {
	if !u.mouseEnabled || width <= 0 || height <= 0 || key == "" {
		return
	}
	u.mouseRegions = append(u.mouseRegions, mouseRegion{x0: x, x1: x + width, y0: y, y1: y + height, key: key})
}

func (u *UI) mouseKey(msg tea.MouseMsg) string {
	mouse := msg.Mouse()
	switch mouse.Button {
	case tea.MouseWheelUp:
		return "wheelup"
	case tea.MouseWheelDown:
		return "wheeldown"
	case tea.MouseLeft:
		if _, clicked := msg.(tea.MouseClickMsg); !clicked {
			return keyTick
		}
		for _, region := range u.mouseRegions {
			if mouse.X >= region.x0 && mouse.X < region.x1 && mouse.Y >= region.y0 && mouse.Y < region.y1 {
				return region.key
			}
		}
	}
	return keyTick
}

func (u *UI) readKey(timeout time.Duration) (string, error) {
	msg, err := u.terminal.ReadMsg(timeout)
	if err != nil {
		return "", err
	}
	switch msg := msg.(type) {
	case tea.KeyPressMsg:
		key := msg.String()
		switch key {
		case "tab":
			return primaryTabKey(u.nextEnabledPrimary(false)), nil
		case "shift+tab":
			return primaryTabKey(u.nextEnabledPrimary(true)), nil
		default:
			return key, nil
		}
	case tea.PasteMsg:
		return msg.Content, nil
	case tea.MouseMsg:
		return u.mouseKey(msg), nil
	default:
		return keyTick, nil
	}
}

type navigationLine struct {
	content string
	tabs    []laidOutTab
	rightX  int
	rightW  int
	rightID string
}

func (u *UI) primarySpecs(active primaryTabID) []tabSpec {
	incoming := 0
	if u.live != nil && u.live.HasIncoming() {
		incoming = u.live.IncomingCount()
	}
	enabled := func(tab primaryTabID, available bool) bool {
		return available && (!u.navigationLocked || u.primaryNavAllowed || tab == active)
	}
	return []tabSpec{
		{id: primaryTabKey(primaryTopics), label: u.t("ui.tabs.topics"), short: "TPS", micro: "T", badge: incoming, selected: active == primaryTopics, enabled: enabled(primaryTopics, true)},
		{id: primaryTabKey(primarySearch), label: u.t("ui.tabs.search"), short: "SRC", micro: "S", selected: active == primarySearch, enabled: enabled(primarySearch, true)},
		{id: primaryTabKey(primaryNotifications), label: u.t("ui.tabs.notifications"), short: "NOT", micro: "N", badge: u.notificationUnread, selected: active == primaryNotifications, enabled: enabled(primaryNotifications, true)},
		{id: primaryTabKey(primaryCompose), label: u.t("ui.tabs.compose"), short: "CMP", micro: "C", selected: active == primaryCompose, enabled: enabled(primaryCompose, true)},
	}
}

func (u *UI) nextEnabledPrimary(reverse bool) primaryTabID {
	next := u.activePrimary
	for range primaryTabCount {
		next = next.next(reverse)
		for _, spec := range u.primarySpecs(u.activePrimary) {
			if spec.id == primaryTabKey(next) && spec.enabled {
				return next
			}
		}
	}
	return u.activePrimary
}

func (u *UI) primaryNavigationLine(active primaryTabID, width int) navigationLine {
	status := u.t("ui.status.logged_in", "username", u.options.Username)
	reserve := 0
	if status != "" && width >= 32 {
		reserve = min(visibleWidth(status)+1, max(width/3, 12))
	}
	rail := layoutTabRail(u.primarySpecs(active), max(width-reserve, 1))
	left := u.style.renderTabRail(rail)
	right := truncateVisible(u.style.Text(status, roleListMeta), max(width-rail.width-1, 0))
	line := headerLine(left, right, width)
	return navigationLine{content: line, tabs: rail.tabs}
}

func abbreviated(value string) string {
	return truncateVisible(strings.ToUpper(strings.TrimSpace(value)), 3)
}

func (u *UI) contextNavigationLine(kind, selected, period string, width int) navigationLine {
	var values []string
	micro := map[string]string{}
	switch kind {
	case "topics":
		values = []string{"latest", "unread", "private", "hot", "new", "top"}
		micro = map[string]string{"latest": "L", "unread": "U", "private": "P", "hot": "H", "new": "N", "top": "T"}
	case "notifications":
		values = notificationFilters
		micro = map[string]string{"all": "A", "responses": "R", "likes": "K", "mentions": "@", "edits": "E", "links": "L", "messages": "M"}
	case "compose":
		values = []string{"title", "category", "new_topic", "reply_topic", "reply_post"}
		micro = map[string]string{"title": "1", "category": "2", "new_topic": "3", "reply_topic": "R", "reply_post": "P"}
	case "search":
		values = []string{"query", "results"}
		micro = map[string]string{"query": "Q", "results": "R"}
	default:
		return navigationLine{}
	}
	specs := make([]tabSpec, 0, len(values))
	for _, value := range values {
		key := "ui." + kind + ".filters." + value
		if kind == "topics" {
			key = "ui.topic_list.filters." + value
		} else if kind == "compose" {
			key = "ui.composer.variants." + value
		} else if kind == "search" {
			key = "ui.search.variants." + value
		}
		label := u.t(key)
		badge := 0
		if kind == "topics" && value == "private" {
			badge = u.pmUnread
		}
		enabled := !u.navigationLocked || value == selected
		if kind == "compose" {
			enabled = value == selected
		}
		specs = append(specs, tabSpec{
			id: contextTabKey(value), label: label, short: abbreviated(label), micro: micro[value], badge: badge, selected: value == selected, enabled: enabled,
		})
	}

	right := ""
	rightID := ""
	reserve := 0
	if kind == "topics" && selected == "top" && period != "" {
		right = u.t("ui.tabs.period", "period", u.t("ui.topic_list.periods."+period))
		if width >= 48 {
			reserve = min(visibleWidth(right)+1, width/3)
			rightID = contextTabKey("period")
		}
	}
	rail := layoutTabRail(specs, max(width-reserve, 1))
	left := u.style.renderTabRail(rail)
	renderedRight := ""
	rightX, rightW := 0, 0
	if reserve > 0 {
		renderedRight = u.style.label.Render(truncateVisible(right, reserve-1))
		rightW = visibleWidth(renderedRight)
		rightX = width - rightW
	}
	return navigationLine{
		content: headerLine(left, renderedRight, width), tabs: rail.tabs,
		rightX: rightX, rightW: rightW, rightID: rightID,
	}
}

func (u *UI) navigationHeader(section string, active primaryTabID, context, selected, period string, extra []string, width, height int) []string {
	u.resetMouseLayout()
	u.activePrimary = active
	u.activeContext, u.activeContextValue, u.activePeriod = context, selected, period
	lines := []navigationLine{u.primaryNavigationLine(active, width)}
	if context != "" {
		lines = append(lines, u.contextNavigationLine(context, selected, period, width))
	}
	content := make([]string, 0, len(lines))
	for _, line := range lines {
		content = append(content, line.content)
	}
	title := u.style.AppTitle(u.displayURL, width, height)
	header := append([]string{}, title...)
	header = append(header, strings.Repeat(" ", max(width, 0)))
	header = append(header, content...)
	header = append(header, u.style.HeaderBox(section, extra, width)...)
	tabStartY := len(title) + 1
	for lineIndex, line := range lines {
		y := tabStartY + lineIndex
		for _, tab := range line.tabs {
			if tab.enabled {
				u.addMouseRegion(tab.x, y, tab.width, 1, tab.id)
			}
		}
		if line.rightID != "" {
			u.addMouseRegion(line.rightX, y, line.rightW, 1, line.rightID)
		}
	}
	return header
}

func (u *UI) requestPrimary(tab primaryTabID) {
	u.requestedPrimary = new(primaryTabID)
	*u.requestedPrimary = tab
}

func (u *UI) takeRequestedPrimary(fallback primaryTabID) primaryTabID {
	if u.requestedPrimary == nil {
		return fallback
	}
	tab := *u.requestedPrimary
	u.requestedPrimary = nil
	return tab
}
