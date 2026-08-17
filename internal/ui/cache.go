package ui

import (
	"net/url"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/merefield/termcourse/internal/discourse"
)

func cacheKey(filter, period string) string { return filter + ":" + period }

func (u *UI) loadList(filter, period string, force bool) (discourse.JSON, error) {
	key := cacheKey(filter, period)
	if cached := u.listCache[key]; cached != nil && !force {
		u.applyIncoming(filter, period, cached)
		u.mergeUsers(cached)
		_ = u.siteInfo()
		return cached, nil
	}
	data, err := u.client.ListTopics(filter, period, u.options.Username, nil)
	if err != nil {
		return nil, err
	}
	u.listCache[key] = data
	u.applyIncoming(filter, period, data)
	u.mergeUsers(data)
	_ = u.siteInfo()
	return data, nil
}

func (u *UI) applyIncoming(filter, period string, data discourse.JSON) {
	if u.live == nil || !u.live.HasIncoming() || filter == "top" {
		return
	}
	ids := u.live.IncomingTopicIDs()
	if len(ids) == 0 {
		return
	}
	params := url.Values{}
	for _, id := range ids {
		params.Add("topic_ids[]", strconv.Itoa(id))
	}
	incoming, err := u.client.ListTopics(filter, period, u.options.Username, params)
	if err != nil {
		return
	}
	mergeTopicLists(data, incoming, true)
	u.mergeUsers(incoming)
	u.live.ClearIncoming(ids)
}

func mergeTopicLists(target, incoming discourse.JSON, prepend bool) {
	if target == nil || incoming == nil {
		return
	}
	targetList := discourse.Map(target["topic_list"])
	if targetList == nil {
		targetList = discourse.JSON{}
		target["topic_list"] = targetList
	}
	incomingList := discourse.Map(incoming["topic_list"])
	existing, additions := topicList(target), topicList(incoming)
	ordered := append([]discourse.JSON{}, existing...)
	if prepend {
		ordered = append(append([]discourse.JSON{}, additions...), existing...)
	} else {
		ordered = append(ordered, additions...)
	}
	seen := map[int]bool{}
	merged := make([]any, 0, len(ordered))
	for _, topic := range ordered {
		id := discourse.Int(topic["id"])
		if !seen[id] {
			seen[id] = true
			merged = append(merged, topic)
		}
	}
	targetList["topics"] = merged
	if incomingList != nil {
		if next, present := incomingList["more_topics_url"]; present {
			targetList["more_topics_url"] = next
		}
	}
	mergeJSONLists(target, incoming, "users", "id")
}

func mergeJSONLists(target, incoming discourse.JSON, key, idKey string) {
	values := map[int]any{}
	for _, value := range discourse.Slice(incoming[key]) {
		values[discourse.Int(discourse.Map(value)[idKey])] = value
	}
	for _, value := range discourse.Slice(target[key]) {
		id := discourse.Int(discourse.Map(value)[idKey])
		if _, present := values[id]; !present {
			values[id] = value
		}
	}
	ids := make([]int, 0, len(values))
	for id := range values {
		ids = append(ids, id)
	}
	sort.Ints(ids)
	result := make([]any, 0, len(ids))
	for _, id := range ids {
		result = append(result, values[id])
	}
	if len(result) > 0 {
		target[key] = result
	}
}

func (u *UI) loadTopic(topicID, nearPost int, force bool) (discourse.JSON, error) {
	cached := u.topicCache[topicID]
	if cached != nil && !force && (nearPost == 0 || hasPostNumber(cached, nearPost)) {
		return cached, nil
	}
	fresh, err := u.client.Topic(topicID, nearPost)
	if err != nil {
		if cached != nil {
			return cached, nil
		}
		return nil, err
	}
	if cached != nil {
		fresh = mergeTopicData(cached, fresh)
	}
	u.topicCache[topicID] = fresh
	return fresh, nil
}

func mergeTopicData(existing, fresh discourse.JSON) discourse.JSON {
	merged := discourse.JSON{}
	for key, value := range existing {
		merged[key] = value
	}
	for key, value := range fresh {
		merged[key] = value
	}
	oldStream, newStream := discourse.Map(existing["post_stream"]), discourse.Map(fresh["post_stream"])
	stream := discourse.JSON{}
	for key, value := range oldStream {
		stream[key] = value
	}
	for key, value := range newStream {
		stream[key] = value
	}
	merged["post_stream"] = stream
	mergePosts(merged, posts(fresh))
	return merged
}

func mergePosts(topic discourse.JSON, incoming []discourse.JSON) {
	stream := discourse.Map(topic["post_stream"])
	if stream == nil {
		stream = discourse.JSON{}
		topic["post_stream"] = stream
	}
	byID := map[int]discourse.JSON{}
	for _, post := range posts(topic) {
		byID[discourse.Int(post["id"])] = post
	}
	for _, post := range incoming {
		byID[discourse.Int(post["id"])] = post
	}
	all := make([]discourse.JSON, 0, len(byID))
	for _, post := range byID {
		all = append(all, post)
	}
	sort.Slice(all, func(i, j int) bool {
		return discourse.Int(all[i]["post_number"]) < discourse.Int(all[j]["post_number"])
	})
	values := make([]any, len(all))
	for index := range all {
		values[index] = all[index]
	}
	stream["posts"] = values
}

func (u *UI) ensureChunks(topicID int, topic discourse.JSON, selected int) (before, total int) {
	stream := discourse.Slice(discourse.Map(topic["post_stream"])["stream"])
	loaded := posts(topic)
	if len(stream) == 0 || len(loaded) == 0 {
		return 0, 0
	}
	load := func(ids []int) int {
		if len(ids) == 0 {
			return 0
		}
		data, err := u.client.TopicPosts(topicID, ids, true)
		if err != nil {
			return 0
		}
		incoming := posts(data)
		mergePosts(topic, incoming)
		return len(incoming)
	}
	streamIndex := func(id int) int {
		for index, raw := range stream {
			if discourse.Int(raw) == id {
				return index
			}
		}
		return -1
	}
	firstIndex := streamIndex(discourse.Int(loaded[0]["id"]))
	if selected <= 2 && firstIndex > 0 {
		var ids []int
		start := max(firstIndex-20, 0)
		for _, raw := range stream[start:firstIndex] {
			ids = append(ids, discourse.Int(raw))
		}
		before = load(ids)
		total += before
	}
	loaded = posts(topic)
	lastIndex := streamIndex(discourse.Int(loaded[len(loaded)-1]["id"]))
	if selected+before >= len(loaded)-3 && lastIndex >= 0 && lastIndex < len(stream)-1 {
		var ids []int
		end := min(lastIndex+21, len(stream))
		for _, raw := range stream[lastIndex+1 : end] {
			ids = append(ids, discourse.Int(raw))
		}
		total += load(ids)
	}
	return before, total
}

func (u *UI) refreshStatusCounts(force bool) bool {
	now := time.Now()
	if u.live != nil && u.live.ConsumeResyncRequest() {
		force = true
	}
	if !force && now.Sub(u.lastStatusRefresh) < 30*time.Second {
		if u.live != nil {
			if value, ok := u.live.UnreadNotificationCount(); ok {
				u.notificationUnread = value
			}
			if value, ok := u.live.PMUnreadCount(); ok {
				u.pmUnread = value
			}
		}
		return false
	}
	changed := false
	if totals, err := u.client.NotificationTotals(); err == nil {
		notifications := 0
		if value, present := totals["unread_notifications"]; present {
			notifications = max(discourse.Int(value), 0)
		} else {
			all := discourse.Int(totals["all_unread_notifications_count"])
			pm := discourse.Int(totals["new_personal_messages_notifications_count"])
			if _, present := totals["unread_personal_messages"]; present {
				pm = discourse.Int(totals["unread_personal_messages"])
			}
			notifications = max(all-pm, 0)
		}
		pm := discourse.Int(totals["unread_personal_messages"])
		if _, present := totals["unread_personal_messages"]; !present {
			pm = discourse.Int(totals["new_personal_messages_notifications_count"])
		}
		changed = notifications != u.notificationUnread || pm != u.pmUnread
		u.notificationUnread, u.pmUnread = notifications, pm
		if u.live != nil {
			u.live.SetUnreadNotificationCount(notifications)
			u.live.SetPMUnreadCount(pm)
		}
	} else if data, fallbackErr := u.client.GetURL("/topics/private-messages-unread.json"); fallbackErr == nil {
		pm := unreadPrivateTopics(data)
		changed = changed || pm != u.pmUnread
		u.pmUnread = pm
		if u.live != nil {
			u.live.SetPMUnreadCount(pm)
		}
	}
	u.lastStatusRefresh = now
	return changed
}

func unreadPrivateTopics(data discourse.JSON) int {
	count := 0
	for _, topic := range topicList(data) {
		unread := discourse.Bool(topic["unread"]) || discourse.Bool(topic["unseen"]) || discourse.Int(topic["unread_posts"]) > 0
		if !unread {
			highest, lastRead := discourse.Int(topic["highest_post_number"]), discourse.Int(topic["last_read_post_number"])
			unread = highest > 0 && highest > lastRead
		}
		if unread {
			count++
		}
	}
	return count
}

func (u *UI) trackLive(filter string) {
	if u.live != nil && u.lastLiveFilter != filter {
		u.live.Track(filter)
		u.lastLiveFilter = filter
	}
}

func (u *UI) mergeUsers(data discourse.JSON) {
	for _, raw := range discourse.Slice(data["users"]) {
		user := discourse.Map(raw)
		u.users[discourse.Int(user["id"])] = user
	}
}

func topicList(data discourse.JSON) []discourse.JSON {
	if data == nil {
		return nil
	}
	list := discourse.Map(data["topic_list"])
	var out []discourse.JSON
	for _, value := range discourse.Slice(list["topics"]) {
		if item := discourse.Map(value); item != nil {
			out = append(out, item)
		}
	}
	return out
}

func posts(data discourse.JSON) []discourse.JSON {
	if data == nil {
		return nil
	}
	stream := discourse.Map(data["post_stream"])
	var out []discourse.JSON
	for _, value := range discourse.Slice(stream["posts"]) {
		if item := discourse.Map(value); item != nil {
			out = append(out, item)
		}
	}
	return out
}

func hasPostNumber(topic discourse.JSON, number int) bool {
	for _, post := range posts(topic) {
		if discourse.Int(post["post_number"]) == number {
			return true
		}
	}
	return false
}

func (u *UI) siteInfo() discourse.JSON {
	if u.siteCache != nil {
		return u.siteCache
	}
	data, err := u.client.SiteInfo()
	if err == nil {
		u.siteCache = data
	}
	return u.siteCache
}

func (u *UI) debugLog(message string) {
	if !u.debug {
		return
	}
	file, err := os.OpenFile(filepath.Join(os.TempDir(), "termcourse_debug.txt"), os.O_CREATE|os.O_APPEND|os.O_WRONLY, 0o600)
	if err == nil {
		_, _ = file.WriteString("[" + time.Now().UTC().Format(time.RFC3339) + "] " + strings.TrimSpace(message) + "\n")
		_ = file.Close()
	}
}
