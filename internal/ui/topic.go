package ui

import (
	"fmt"
	"os"
	"sort"
	"strconv"
	"strings"

	"github.com/merefield/termcourse/internal/discourse"
)

const progressKeyPrefix = "__progress:"

func progressKey(index int) string { return progressKeyPrefix + strconv.Itoa(index) }

func parseProgressKey(key string) (int, bool) {
	value, found := strings.CutPrefix(key, progressKeyPrefix)
	if !found {
		return 0, false
	}
	index, err := strconv.Atoi(value)
	return index, err == nil && index >= 0
}

func topicProgressPosition(topic discourse.JSON, all []discourse.JSON, selected int) (current, total int) {
	stream := discourse.Slice(discourse.Map(topic["post_stream"])["stream"])
	if len(stream) == 0 || selected < 0 || selected >= len(all) {
		return min(selected+1, len(all)), len(all)
	}
	selectedID := discourse.Int(all[selected]["id"])
	for index, rawID := range stream {
		if discourse.Int(rawID) == selectedID {
			return index + 1, len(stream)
		}
	}
	return min(selected+1, len(all)), len(stream)
}

func (u *UI) seekTopicProgress(topicID int, topic discourse.JSON, target int) (int, error) {
	streamData := discourse.Map(topic["post_stream"])
	stream := discourse.Slice(streamData["stream"])
	if len(stream) == 0 {
		return 0, fmt.Errorf("topic has no post stream")
	}
	target = min(max(target, 0), len(stream)-1)
	targetID := discourse.Int(stream[target])
	for index, post := range posts(topic) {
		if discourse.Int(post["id"]) == targetID {
			return index, nil
		}
	}
	start, end := max(target-10, 0), min(target+11, len(stream))
	ids := make([]int, 0, end-start)
	for _, rawID := range stream[start:end] {
		ids = append(ids, discourse.Int(rawID))
	}
	data, err := u.client.TopicPosts(topicID, ids, true)
	if err != nil {
		return 0, err
	}
	incoming := posts(data)
	if len(incoming) == 0 {
		return 0, fmt.Errorf("post stream returned no posts near position %d", target+1)
	}
	sort.Slice(incoming, func(i, j int) bool {
		return discourse.Int(incoming[i]["post_number"]) < discourse.Int(incoming[j]["post_number"])
	})
	values := make([]any, len(incoming))
	for index := range incoming {
		values[index] = incoming[index]
	}
	streamData["posts"] = values
	for index, post := range incoming {
		if discourse.Int(post["id"]) == targetID {
			return index, nil
		}
	}
	return 0, fmt.Errorf("post at position %d was not returned", target+1)
}

func (u *UI) topicLoop(topicID, selectedPostNumber int, back string) (quit bool, result string) {
	origin := u.activePrimary
	if origin != primaryTopics && origin != primarySearch && origin != primaryNotifications {
		origin = primaryTopics
	}
	topic, err := u.loadTopic(topicID, selectedPostNumber, selectedPostNumber == 0)
	if err != nil {
		u.showError(err)
		return false, back
	}
	all := posts(topic)
	selected := initialSelected(all, topic, selectedPostNumber)
	scroll := map[int]int{}
	if u.live != nil {
		position := discourse.Int(topic["message_bus_last_id"])
		if position == 0 {
			u.live.WatchTopic(topicID, nil)
		} else {
			u.live.WatchTopic(topicID, &position)
		}
		defer u.live.ClearTopic()
	}
	for {
		if u.requestedPrimary != nil {
			return false, "navigate"
		}
		changed := u.applyLiveTopic(topicID, topic, &selected)
		before, total := u.ensureChunks(topicID, topic, selected)
		if total > 0 {
			selected += before
			changed = true
		}
		all = posts(topic)
		selected = min(selected, max(len(all)-1, 0))
		if selected < len(all) {
			postNumber := discourse.Int(all[selected]["post_number"])
			if postNumber > 0 && u.lastRead[topicID] != postNumber {
				if u.client.UpdateTopicReadState(topicID, postNumber, 1200) {
					u.lastRead[topicID] = postNumber
				}
			}
		}
		u.renderTopic(topic, all, selected, scroll[selected], changed, origin)
		key, readErr := u.readKey(u.tick)
		if readErr != nil {
			u.quitRequested = true
			return true, ""
		}
		if tab, ok := parsePrimaryTabKey(key); ok {
			if tab == origin {
				continue
			}
			u.requestPrimary(tab)
			return false, "navigate"
		}
		if row, ok := parseRowKey(key); ok {
			if row >= 0 && row < len(all) {
				selected = row
			}
			continue
		}
		if target, ok := parseProgressKey(key); ok {
			position, seekErr := u.seekTopicProgress(topicID, topic, target)
			if seekErr != nil {
				u.showError(seekErr)
				continue
			}
			selected = position
			scroll = map[int]int{}
			continue
		}
		switch key {
		case keyTick:
			continue
		case "up":
			selected = max(selected-1, 0)
		case "down":
			selected = min(selected+1, max(len(all)-1, 0))
		case "right", "wheeldown":
			scroll[selected] += 3
		case "left", "wheelup":
			scroll[selected] = max(scroll[selected]-3, 0)
		case "l":
			if selected < len(all) {
				u.toggleLike(all[selected])
			}
		case "r":
			if body := u.compose(u.t("ui.composer.reply_to_topic"), nil, u.categoryLabel(topic), "reply_topic"); body != "" {
				if created, createErr := u.client.CreatePost(topicID, body, 0); createErr != nil {
					u.showError(createErr)
				} else {
					appendCreated(topic, created)
					u.listCache = map[string]discourse.JSON{}
					selected = len(posts(topic)) - 1
				}
			}
		case "p":
			if selected < len(all) {
				post := all[selected]
				context := u.replyContext(post)
				title := u.t("ui.composer.reply_to_post", "post_number", discourse.Int(post["post_number"]))
				if body := u.compose(title, context, u.categoryLabel(topic), "reply_post"); body != "" {
					if created, createErr := u.client.CreatePost(topicID, body, discourse.Int(post["post_number"])); createErr != nil {
						u.showError(createErr)
					} else {
						appendCreated(topic, created)
						u.listCache = map[string]discourse.JSON{}
						selected = len(posts(topic)) - 1
					}
				}
			}
		case "s":
			u.requestPrimary(primarySearch)
			return false, "navigate"
		case "n":
			u.requestPrimary(primaryNotifications)
			return false, "navigate"
		case "x":
			if selected < len(all) {
				urls := extractImageURLs(discourse.String(all[selected]["raw"]), u.options.BaseURL)
				if len(urls) > 0 {
					u.fullscreenImage(urls[0])
				}
			}
		case "q":
			u.quitRequested = true
			return true, ""
		case "esc", "backspace":
			return false, back
		}
	}
}

func (u *UI) renderTopic(topic discourse.JSON, all []discourse.JSON, selected, scroll int, force bool, origin primaryTabID) {
	width, height := u.terminal.Size()
	selectedPost := discourse.JSON(nil)
	if selected >= 0 && selected < len(all) {
		selectedPost = all[selected]
	}
	controls := u.t("ui.controls.topic")
	imageURLs := extractImageURLs(discourse.String(selectedPost["raw"]), u.options.BaseURL)
	imagesEnabled := os.Getenv("TERMCOURSE_IMAGES") != "0"
	if imagesEnabled && (u.kittyAvailable() || imageBackend() != "") && len(imageURLs) > 0 {
		controls = u.t("ui.controls.topic_with_image")
	}
	innerWidth, _ := frameInnerWidth(width)
	current, total := topicProgressPosition(topic, all, selected)
	meta := fmt.Sprintf("%d/%d", current, total)
	if category := u.categoryLabel(topic); category != "" {
		meta += " · " + category
	}
	titleWidth := max(innerWidth-visibleWidth(meta)-1, 1)
	title := truncate(discourse.String(topic["title"]), titleWidth)
	header := u.navigationHeader(u.t("ui.headers.topic"), origin, "", "", "", []string{
		headerLine(title, meta, innerWidth),
	}, width, height)
	progress := u.progressFooter(current, total, width)
	footerRows := len(progress) + 1
	available := max(height-len(header)-footerRows, 1)
	body, postIndexes := u.postListLines(all, selected, scroll, available, width)
	screen := make([]string, height)
	copy(screen, header)
	for index := 0; index < available && index < len(body); index++ {
		y := len(header) + index
		if y >= height {
			break
		}
		screen[y] = body[index]
		if index < len(postIndexes) && postIndexes[index] >= 0 {
			u.addMouseRegion(0, y, width, 1, rowKey(postIndexes[index]))
		}
	}
	progressStart := max(height-footerRows, 0)
	copy(screen[progressStart:], progress)
	u.addProgressRegions(progressStart+1, current, total, width, height)
	u.placeControlsFooter(screen, controls, width)
	u.renderer.SetProgress(current, total)
	u.renderer.Render(screen, width, height, "topic-"+formatCount(topic["id"]), -1, -1, force)
}

func (u *UI) postListLines(all []discourse.JSON, selected, scroll, available, width int) ([]string, []int) {
	if len(all) == 0 {
		return []string{u.t("ui.empty.posts")}, []int{-1}
	}
	selectedBlock := u.postBlock(all[selected], true, width)
	maxSelected := min(max(int(float64(available)*0.6), 6), len(selectedBlock))
	maxScroll := max(len(selectedBlock)-maxSelected, 0)
	scroll = min(max(scroll, 0), maxScroll)
	selectedBlock = selectedBlock[scroll:min(scroll+maxSelected, len(selectedBlock))]
	if scroll > 0 && len(selectedBlock) > 0 {
		selectedBlock[0] = u.t("ui.scroll.more_above")
	}
	if scroll < maxScroll && len(selectedBlock) > 0 {
		selectedBlock[len(selectedBlock)-1] = u.t("ui.scroll.more_below")
	}
	type block struct {
		index int
		lines []string
	}
	blocks := []block{{selected, selectedBlock}}
	remaining := available - len(selectedBlock) - 1
	for offset := 1; remaining > 0 && (selected-offset >= 0 || selected+offset < len(all)); offset++ {
		if selected-offset >= 0 {
			lines := u.postBlock(all[selected-offset], false, width)
			if len(lines)+1 > remaining {
				lines = lines[max(len(lines)-(remaining-1), 0):]
			}
			blocks = append([]block{{selected - offset, lines}}, blocks...)
			remaining -= len(lines) + 1
		}
		if remaining <= 0 {
			break
		}
		if selected+offset < len(all) {
			lines := u.postBlock(all[selected+offset], false, width)
			if len(lines)+1 > remaining {
				lines = lines[:max(remaining-1, 0)]
			}
			blocks = append(blocks, block{selected + offset, lines})
			remaining -= len(lines) + 1
		}
	}
	var out []string
	var postIndexes []int
	for index, item := range blocks {
		out = append(out, item.lines...)
		for range item.lines {
			postIndexes = append(postIndexes, item.index)
		}
		if index != len(blocks)-1 {
			out = append(out, u.style.Text(strings.Repeat("─", width), roleSeparator))
			postIndexes = append(postIndexes, -1)
		}
	}
	return out, postIndexes
}

func (u *UI) postBlock(post discourse.JSON, expanded bool, width int) []string {
	bodyWidth := max(width-3, 1)
	raw := discourse.String(post["raw"])
	var body []string
	if expanded {
		body = u.imagePreview(raw, bodyWidth)
		if len(body) > 0 {
			body = append(body, u.style.Text(u.t("ui.posts.expand_image"), roleListMeta))
		}
	}
	text := stripMarkdownImages(raw)
	for _, line := range wrapLines(emojify(text, u.emojiEnabled), bodyWidth, u.linksEnabled) {
		body = append(body, u.style.Text(line, roleListText))
	}
	username := u.style.Text("@"+discourse.String(post["username"]), rolePostUsername)
	header := headerLine(username, u.likeIndicator(post), width)
	if expanded {
		header = u.style.Selected(header)
		return append([]string{header}, body...)
	}
	if len(body) > 3 {
		body = body[:3]
	}
	return append([]string{header}, body...)
}

func (u *UI) progressFooter(current, total, width int) []string {
	hovered := strings.HasPrefix(u.hoveredControl, progressKeyPrefix)
	return u.style.ProgressBox(u.t("ui.status.read_progress"), current, total, width, hovered)
}

func (u *UI) addProgressRegions(y, current, total, width, height int) {
	layout := layoutProgressBox(current, total, width)
	if !u.mouseEnabled || total <= 0 || layout.barWidth <= 0 || y < 0 || y >= height-1 {
		return
	}
	positionAt := func(offset int) int {
		if total <= 1 || layout.barWidth <= 1 {
			return 0
		}
		return (offset*(total-1) + (layout.barWidth-1)/2) / (layout.barWidth - 1)
	}
	start, target := 0, positionAt(0)
	for offset := 1; offset <= layout.barWidth; offset++ {
		next := -1
		if offset < layout.barWidth {
			next = positionAt(offset)
		}
		if next == target {
			continue
		}
		u.addMouseRegion(layout.barX+start, y, offset-start, 1, progressKey(target))
		start, target = offset, next
	}
}

func (u *UI) toggleLike(post discourse.JSON) {
	liked := postLiked(post)
	var err error
	if liked {
		_, err = u.client.UnlikePost(discourse.Int(post["id"]))
	} else {
		_, err = u.client.LikePost(discourse.Int(post["id"]))
	}
	if err != nil {
		u.showError(err)
		return
	}
	action := likeAction(post)
	if action == nil {
		action = discourse.JSON{"id": 2, "count": 0, "acted": false}
		post["actions_summary"] = append(discourse.Slice(post["actions_summary"]), action)
	}
	count := discourse.Int(action["count"])
	if liked {
		count = max(count-1, 0)
	} else {
		count++
	}
	action["count"], action["acted"] = count, !liked
}

func (u *UI) likeIndicator(post discourse.JSON) string {
	action := likeAction(post)
	count := discourse.Int(action["count"])
	heart := "♡"
	if count > 0 {
		heart = "♥"
	}
	if discourse.Bool(action["acted"]) {
		heart = u.style.Liked("♥")
	}
	if count > 1 {
		return strconv.Itoa(count) + " " + heart
	}
	return heart
}

func likeAction(post discourse.JSON) discourse.JSON {
	for _, raw := range discourse.Slice(post["actions_summary"]) {
		action := discourse.Map(raw)
		if discourse.Int(action["id"]) == 2 {
			return action
		}
	}
	return nil
}

func postLiked(post discourse.JSON) bool { return discourse.Bool(likeAction(post)["acted"]) }

func initialSelected(all []discourse.JSON, topic discourse.JSON, requested int) int {
	if requested > 0 {
		return findPost(all, requested)
	}
	lastRead := discourse.Int(topic["last_read_post_number"])
	if lastRead > 0 {
		for index, post := range all {
			if discourse.Int(post["post_number"]) > lastRead {
				return index
			}
		}
		for index := len(all) - 1; index >= 0; index-- {
			if discourse.Int(all[index]["post_number"]) <= lastRead {
				return index
			}
		}
	}
	return 0
}

func findPost(all []discourse.JSON, postNumber int) int {
	for index, post := range all {
		if discourse.Int(post["post_number"]) == postNumber {
			return index
		}
	}
	return 0
}

func appendCreated(topic, post discourse.JSON) {
	if post == nil {
		return
	}
	stream := discourse.Map(topic["post_stream"])
	id := discourse.Int(post["id"])
	values := discourse.Slice(stream["stream"])
	found := false
	for _, value := range values {
		found = found || discourse.Int(value) == id
	}
	if !found {
		stream["stream"] = append(values, id)
	}
	mergePosts(topic, []discourse.JSON{post})
	topic["posts_count"] = len(posts(topic))
}

func (u *UI) categoryLabel(topic discourse.JSON) string {
	id := discourse.Int(topic["category_id"])
	if id == 0 {
		return u.t("ui.composer.category_none")
	}
	for _, raw := range discourse.Slice(u.siteInfo()["categories"]) {
		category := discourse.Map(raw)
		if discourse.Int(category["id"]) == id {
			return u.t("ui.composer.category_named", "name", discourse.String(category["name"]))
		}
	}
	return u.t("ui.composer.category_fallback", "id", id)
}

func (u *UI) applyLiveTopic(topicID int, topic discourse.JSON, selected *int) bool {
	if u.live == nil {
		return false
	}
	if u.live.ConsumeTopicRefreshRequest(topicID) {
		current := 0
		all := posts(topic)
		if *selected < len(all) {
			current = discourse.Int(all[*selected]["post_number"])
		}
		fresh, err := u.loadTopic(topicID, current, true)
		if err != nil {
			u.live.RequeueTopicRefreshRequest(topicID)
			return false
		}
		u.topicCache[topicID] = fresh
		for key := range topic {
			delete(topic, key)
		}
		for key, value := range fresh {
			topic[key] = value
		}
		*selected = findPost(posts(topic), current)
		return true
	}
	changedIDs := u.live.ConsumeTopicChangedPostIDs(topicID)
	createdIDs := u.live.ConsumeTopicPostIDs(topicID)
	ids := append(append([]int{}, changedIDs...), createdIDs...)
	if len(ids) == 0 {
		return false
	}
	data, err := u.client.TopicPosts(topicID, uniqueInts(ids), true)
	if err != nil {
		if len(createdIDs) > 0 {
			u.live.RequeueTopicRefreshRequest(topicID)
		}
		return false
	}
	stream := discourse.Map(topic["post_stream"])
	values := discourse.Slice(stream["stream"])
	for _, id := range createdIDs {
		found := false
		for _, value := range values {
			found = found || discourse.Int(value) == id
		}
		if !found {
			values = append(values, id)
		}
	}
	stream["stream"] = values
	followTail := *selected >= len(posts(topic))-1
	mergePosts(topic, posts(data))
	topic["posts_count"] = len(values)
	if followTail && len(createdIDs) > 0 {
		*selected = len(posts(topic)) - 1
	}
	return true
}

func uniqueInts(values []int) []int {
	seen := map[int]bool{}
	var out []int
	for _, value := range values {
		if value > 0 && !seen[value] {
			seen[value] = true
			out = append(out, value)
		}
	}
	return out
}
