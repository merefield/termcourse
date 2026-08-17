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
}

type laidOutTab struct {
	id       string
	label    string
	x        int
	width    int
	selected bool
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
			id: specs[index].id, label: label, x: x, width: tabWidth, selected: specs[index].selected,
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
		style := s.headerSep
		if tab.selected {
			style = s.selected.Bold(true)
		}
		parts = append(parts, style.Render(tab.label))
	}
	return strings.Join(parts, s.headerSep.Render(rail.separator))
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
			return primaryTabKey(u.activePrimary.next(false)), nil
		case "shift+tab":
			return primaryTabKey(u.activePrimary.next(true)), nil
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
	return []tabSpec{
		{id: primaryTabKey(primaryTopics), label: u.t("ui.tabs.topics"), short: "TOP", micro: "T", badge: incoming, selected: active == primaryTopics},
		{id: primaryTabKey(primarySearch), label: u.t("ui.tabs.search"), short: "SRC", micro: "S", selected: active == primarySearch},
		{id: primaryTabKey(primaryNotifications), label: u.t("ui.tabs.notifications"), short: "NOT", micro: "N", badge: u.notificationUnread, selected: active == primaryNotifications},
	}
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
	default:
		return navigationLine{}
	}
	specs := make([]tabSpec, 0, len(values))
	for _, value := range values {
		key := "ui." + kind + ".filters." + value
		if kind == "topics" {
			key = "ui.topic_list.filters." + value
		}
		label := u.t(key)
		badge := 0
		if kind == "topics" && value == "private" {
			badge = u.pmUnread
		}
		specs = append(specs, tabSpec{
			id: contextTabKey(value), label: label, short: abbreviated(label), micro: micro[value], badge: badge, selected: value == selected,
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
	innerWidth, sidePadding := frameInnerWidth(width)
	lines := []navigationLine{u.primaryNavigationLine(active, innerWidth)}
	if context != "" {
		lines = append(lines, u.contextNavigationLine(context, selected, period, innerWidth))
	}
	content := make([]string, 0, len(lines)+len(extra))
	for _, line := range lines {
		content = append(content, line.content)
	}
	content = append(content, extra...)
	header := u.style.AppHeader(section, u.displayURL, content, width, height)
	brandHeight := len(header) - len(content) - 2
	xOffset := 1
	if sidePadding {
		xOffset = 2
	}
	for lineIndex, line := range lines {
		y := brandHeight + 1 + lineIndex
		for _, tab := range line.tabs {
			u.addMouseRegion(xOffset+tab.x, y, tab.width, 1, tab.id)
		}
		if line.rightID != "" {
			u.addMouseRegion(xOffset+line.rightX, y, line.rightW, 1, line.rightID)
		}
	}
	return header
}

func controlsFooter(style *Style, controls string, width int) string {
	return style.Text(padLine(controls, width), roleListMeta)
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
