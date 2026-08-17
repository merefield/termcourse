package ui

import (
	"strings"
	"time"

	"charm.land/lipgloss/v2"
	"github.com/merefield/termcourse/internal/discourse"
)

func (u *UI) searchFlow(query string) {
	for query != "" {
		selection := u.searchLoop(query)
		if selection == nil {
			return
		}
		quit, back := u.topicLoop(discourse.Int(selection["topic_id"]), discourse.Int(selection["post_number"]), "search")
		if quit || back != "search" {
			return
		}
	}
}

func (u *UI) searchLoop(query string) discourse.JSON {
	data, err := u.client.Search(query)
	if err != nil {
		u.showError(err)
		return nil
	}
	topics := map[int]string{}
	for _, raw := range discourse.Slice(data["topics"]) {
		topic := discourse.Map(raw)
		topics[discourse.Int(topic["id"])] = discourse.String(topic["title"])
	}
	var results []discourse.JSON
	for _, raw := range discourse.Slice(data["posts"]) {
		results = append(results, discourse.Map(raw))
	}
	selected := 0
	for {
		u.renderSearch(query, results, topics, selected)
		key, readErr := u.terminal.ReadKey(u.tick)
		if readErr != nil {
			return nil
		}
		switch key {
		case "up":
			selected = max(selected-1, 0)
		case "down":
			selected = min(selected+1, max(len(results)-1, 0))
		case "enter":
			if selected < len(results) {
				return results[selected]
			}
		case "n":
			if u.notificationsLoop() {
				return nil
			}
		case "q", "esc":
			return nil
		}
	}
}

func (u *UI) renderSearch(query string, results []discourse.JSON, topics map[int]string, selected int) {
	width, height := u.terminal.Size()
	header := u.style.AppHeader(u.t("ui.search.title"), u.displayURL, []string{
		u.t("ui.controls.search_results"),
		u.t("ui.status.search", "query", truncate(query, max(width-4, 1))),
	}, width, height)
	screen := make([]string, height)
	copy(screen, header)
	startRow := len(header) + 1
	if len(results) == 0 {
		screen[min(startRow, height-1)] = u.t("ui.empty.results")
		u.renderer.Render(screen, width, height, "search-"+query, -1, -1, false)
		return
	}
	rows := max(height-startRow-1, 1)
	start := max(selected-rows/2, 0)
	titleWidth := max((width-4)/3, 10)
	blurbWidth := max(width-titleWidth-4, 10)
	for index := start; index < min(start+rows, len(results)); index++ {
		post := results[index]
		title := emojify(topics[discourse.Int(post["topic_id"])], u.emojiEnabled)
		blurb := emojify(stripHTML(discourse.String(post["blurb"])), u.emojiEnabled)
		line := fitCell(title, titleWidth, false) + " - " + fitCell(blurb, blurbWidth, false)
		line = highlightTerm(line, query)
		if index == selected {
			line = u.style.Selected(line)
		}
		screen[startRow+index-start] = line
	}
	u.renderer.Render(screen, width, height, "search-"+query, -1, -1, false)
}

var notificationFilters = []string{"all", "responses", "likes", "mentions", "edits", "links", "messages"}

func (u *UI) notificationsLoop() bool {
	data, err := u.client.Notifications(0, 60, "")
	if err != nil {
		u.showError(err)
		return false
	}
	var all []discourse.JSON
	for _, raw := range discourse.Slice(data["notifications"]) {
		all = append(all, discourse.Map(raw))
	}
	nextURL := discourse.String(data["load_more_notifications"])
	selected, filterIndex, loading := 0, 0, false
	for {
		filter := notificationFilters[filterIndex]
		filtered := u.filterNotifications(all, filter)
		selected = min(selected, max(len(filtered)-1, 0))
		u.renderNotifications(filtered, selected, filter, loading)
		key, readErr := u.terminal.ReadKey(u.tick)
		if readErr != nil {
			return false
		}
		switch key {
		case "up":
			selected = max(selected-1, 0)
		case "down":
			selected = min(selected+1, max(len(filtered)-1, 0))
			if nextURL != "" && selected >= len(filtered)-3 && !loading {
				loading = true
				u.renderNotifications(filtered, selected, filter, loading)
				if more, moreErr := u.client.GetURL(nextURL); moreErr == nil {
					for _, raw := range discourse.Slice(more["notifications"]) {
						all = append(all, discourse.Map(raw))
					}
					nextURL = discourse.String(more["load_more_notifications"])
				}
				loading = false
			}
		case "f":
			filterIndex = (filterIndex + 1) % len(notificationFilters)
			selected = 0
		case "enter":
			if selected >= len(filtered) {
				continue
			}
			notification := filtered[selected]
			u.markNotificationRead(notification)
			topicID := discourse.Int(notification["topic_id"])
			if topicID > 0 {
				quit, _ := u.topicLoop(topicID, discourse.Int(notification["post_number"]), "notifications")
				if quit {
					return true
				}
			}
		case "q":
			return true
		case "esc":
			return false
		}
	}
}

func (u *UI) renderNotifications(notifications []discourse.JSON, selected int, filter string, loading bool) {
	width, height := u.terminal.Size()
	status := u.t("ui.status.notifications", "filter", u.t("ui.notifications.filters."+filter))
	if loading {
		status += " · " + u.t("ui.status.loading_more")
	}
	innerWidth, _ := frameInnerWidth(width)
	header := u.style.AppHeader(status, u.displayURL, []string{
		headerLine(u.t("ui.controls.notifications"), u.rightStatus(), innerWidth),
	}, width, height)
	screen := make([]string, height)
	copy(screen, header)
	startRow := len(header) + 1
	if len(notifications) == 0 {
		screen[min(startRow, height-1)] = u.t("ui.empty.notifications")
		u.renderer.Render(screen, width, height, "notifications-"+filter, -1, -1, false)
		return
	}
	userWidth, typeWidth, timeWidth := min(max(width*18/100, 12), 20), min(max(width*14/100, 10), 14), 6
	titleWidth := max(width-userWidth-typeWidth-timeWidth-7, 12)
	screen[startRow] = u.style.Text(tableRow([]cell{
		{u.t("ui.notifications.columns.user"), userWidth, false},
		{u.t("ui.notifications.columns.type"), typeWidth, false},
		{u.t("ui.notifications.columns.title"), titleWidth, false},
		{u.t("ui.notifications.columns.ago"), timeWidth, true},
	}), roleListMeta)
	startRow++
	rows := max(height-startRow-1, 1)
	start := max(selected-rows/2, 0)
	for index := start; index < min(start+rows, len(notifications)); index++ {
		item := notifications[index]
		marker := "  "
		if !discourse.Bool(item["read"]) {
			marker = u.t("ui.notifications.unread_marker")
		}
		line := tableRow([]cell{
			{marker + u.notificationActor(item), userWidth, false},
			{u.notificationTypeLabel(item), typeWidth, false},
			{u.notificationTitle(item), titleWidth, false},
			{relativeTime(discourse.String(item["created_at"]), u), timeWidth, true},
		})
		if index == selected {
			line = u.style.Selected(line)
		}
		screen[startRow+index-start] = line
	}
	u.renderer.Render(screen, width, height, "notifications-"+filter, -1, -1, false)
}

func (u *UI) filterNotifications(all []discourse.JSON, filter string) []discourse.JSON {
	if filter == "all" {
		return all
	}
	sets := map[string][]string{
		"responses": {"replied", "quoted", "following_replied"},
		"likes":     {"liked", "liked_consolidated", "reaction"},
		"mentions":  {"mentioned", "group_mentioned"},
		"edits":     {"edited"},
		"links":     {"linked", "linked_consolidated"},
		"messages":  {"private_message", "invited_to_private_message", "group_message_summary"},
	}
	allowed := map[string]bool{}
	for _, name := range sets[filter] {
		allowed[name] = true
	}
	var out []discourse.JSON
	for _, item := range all {
		if allowed[u.notificationTypeName(item)] {
			out = append(out, item)
		}
	}
	return out
}

func (u *UI) notificationTypes() map[int]string {
	out := map[int]string{}
	for name, raw := range discourse.Map(u.siteInfo()["notification_types"]) {
		out[discourse.Int(raw)] = name
	}
	return out
}

func (u *UI) notificationTypeName(item discourse.JSON) string {
	return u.notificationTypes()[discourse.Int(item["notification_type"])]
}

func (u *UI) notificationActor(item discourse.JSON) string {
	name := discourse.String(item["acting_user_name"])
	if name == "" {
		name = discourse.String(discourse.Map(item["data"])["display_username"])
	}
	return name
}

func (u *UI) notificationTypeLabel(item discourse.JSON) string {
	raw := discourse.String(discourse.Map(item["data"])["message"])
	if raw == "" {
		raw = u.notificationTypeName(item)
	}
	parts := strings.FieldsFunc(raw, func(r rune) bool { return r == '_' || r == '-' })
	for index := range parts {
		if parts[index] != "" {
			parts[index] = strings.ToUpper(parts[index][:1]) + parts[index][1:]
		}
	}
	return strings.Join(parts, " ")
}

func (u *UI) notificationTitle(item discourse.JSON) string {
	text := discourse.String(item["fancy_title"])
	if text == "" {
		text = discourse.String(discourse.Map(item["data"])["topic_title"])
	}
	return stripHTML(text)
}

func (u *UI) markNotificationRead(item discourse.JSON) {
	if discourse.Bool(item["read"]) {
		return
	}
	if _, err := u.client.MarkNotificationRead(discourse.Int(item["id"])); err != nil {
		u.showError(err)
		return
	}
	item["read"] = true
	u.notificationUnread = max(u.notificationUnread-1, 0)
	if u.live != nil {
		u.live.SetUnreadNotificationCount(u.notificationUnread)
	}
}

func relativeTime(raw string, u *UI) string {
	created, err := time.Parse(time.RFC3339, raw)
	if err != nil {
		return ""
	}
	seconds := max(int(time.Since(created).Seconds()), 0)
	if seconds < 60 {
		return u.t("ui.time.seconds", "count", seconds)
	}
	minutes := seconds / 60
	if minutes < 60 {
		return u.t("ui.time.minutes", "count", minutes)
	}
	hours := minutes / 60
	if hours < 24 {
		return u.t("ui.time.hours", "count", hours)
	}
	days := hours / 24
	if days < 7 {
		return u.t("ui.time.days", "count", days)
	}
	if days < 365 {
		return u.t("ui.time.weeks", "count", days/7)
	}
	return u.t("ui.time.years", "count", days/365)
}

func highlightTerm(value, query string) string {
	if strings.TrimSpace(query) == "" {
		return value
	}
	lower, needle := strings.ToLower(value), strings.ToLower(query)
	var out strings.Builder
	for {
		index := strings.Index(lower, needle)
		if index < 0 {
			out.WriteString(value)
			break
		}
		out.WriteString(value[:index])
		out.WriteString(lipgloss.NewStyle().Bold(true).Render(value[index : index+len(needle)]))
		value, lower = value[index+len(needle):], lower[index+len(needle):]
	}
	return out.String()
}
