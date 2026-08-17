package liveupdates

import (
	"fmt"
	"net/http"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/merefield/termcourse/internal/discourse"
	"github.com/merefield/termcourse/internal/messagebus"
)

const (
	MaxIncomingTopicIDs = 500
	WatchdogInterval    = 30 * time.Second
	WatchdogStale       = 240 * time.Second
)

type Bus interface {
	Subscribe(channel string, lastMessageID int, callback messagebus.Callback) error
	Unsubscribe(channel string)
	Start() error
	Stop()
	Status() int
	Success() int64
}

type Options struct {
	CurrentUserID               int
	NotificationChannelPosition *int
	MaxIncomingTopicIDs         int
	WatchdogInterval            time.Duration
	WatchdogStaleThreshold      time.Duration
	Debug                       func(string)
	Now                         func() time.Time
	Client                      Bus
	Factory                     func() Bus
}

type LiveUpdates struct {
	baseURL string
	headers http.Header
	options Options

	mu                   sync.Mutex
	client               Bus
	factory              func() Bus
	filter               string
	incomingSet          map[int]bool
	incomingOrder        []int
	unread               *int
	pmUnread             *int
	positions            map[string]int
	lastSuccessAt        time.Time
	lastSuccessCount     int64
	topicChannel         string
	topicCreated         []int
	topicCreatedSet      map[int]bool
	topicChanged         []int
	topicChangedSet      map[int]bool
	topicRefresh         int
	running              bool
	resyncRequested      bool
	listRefreshRequested bool
	restarting           bool
	stopWatchdog         chan struct{}
}

func New(baseURL string, headers http.Header, options Options) *LiveUpdates {
	if options.MaxIncomingTopicIDs <= 0 {
		options.MaxIncomingTopicIDs = MaxIncomingTopicIDs
	}
	if options.WatchdogInterval <= 0 {
		options.WatchdogInterval = WatchdogInterval
	}
	if options.WatchdogStaleThreshold <= 0 {
		options.WatchdogStaleThreshold = WatchdogStale
	}
	if options.Now == nil {
		options.Now = time.Now
	}
	factory := options.Factory
	if factory == nil {
		factory = func() Bus { return messagebus.New(baseURL, headers) }
	}
	client := options.Client
	if client == nil {
		client = factory()
	}
	result := &LiveUpdates{
		baseURL: baseURL, headers: headers.Clone(), options: options, client: client, factory: factory,
		filter: "latest", incomingSet: map[int]bool{}, positions: map[string]int{},
		topicCreatedSet: map[int]bool{}, topicChangedSet: map[int]bool{},
	}
	result.subscribeInitial(client)
	return result
}

func (l *LiveUpdates) Start() error {
	l.mu.Lock()
	if l.running {
		l.mu.Unlock()
		return nil
	}
	l.running = true
	l.lastSuccessAt = l.options.Now()
	l.lastSuccessCount = l.client.Success()
	stop := make(chan struct{})
	l.stopWatchdog = stop
	client := l.client
	l.mu.Unlock()
	if err := client.Start(); err != nil {
		l.mu.Lock()
		l.running = false
		l.stopWatchdog = nil
		l.mu.Unlock()
		return err
	}
	go l.watchdog(stop)
	return nil
}

func (l *LiveUpdates) Stop() {
	l.mu.Lock()
	if !l.running {
		l.mu.Unlock()
		return
	}
	l.running = false
	stop := l.stopWatchdog
	l.stopWatchdog = nil
	client := l.client
	l.mu.Unlock()
	client.Stop()
	if stop != nil {
		close(stop)
	}
}

func (l *LiveUpdates) Track(filter string) {
	l.mu.Lock()
	l.filter = filter
	l.clearIncomingLocked()
	l.mu.Unlock()
}

func (l *LiveUpdates) IncomingCount() int {
	l.mu.Lock()
	defer l.mu.Unlock()
	return len(l.incomingSet)
}

func (l *LiveUpdates) HasIncoming() bool { return l.IncomingCount() > 0 }

func (l *LiveUpdates) IncomingTopicIDs() []int {
	l.mu.Lock()
	defer l.mu.Unlock()
	return append([]int{}, l.incomingOrder...)
}

func (l *LiveUpdates) ClearIncoming(ids []int) {
	l.mu.Lock()
	defer l.mu.Unlock()
	if ids == nil {
		l.clearIncomingLocked()
		return
	}
	remove := map[int]bool{}
	for _, id := range ids {
		remove[id] = true
		delete(l.incomingSet, id)
	}
	out := l.incomingOrder[:0]
	for _, id := range l.incomingOrder {
		if !remove[id] {
			out = append(out, id)
		}
	}
	l.incomingOrder = out
}

func (l *LiveUpdates) UnreadNotificationCount() (int, bool) {
	l.mu.Lock()
	defer l.mu.Unlock()
	if l.unread == nil {
		return 0, false
	}
	return *l.unread, true
}

func (l *LiveUpdates) SetUnreadNotificationCount(value int) {
	value = max(value, 0)
	l.mu.Lock()
	l.unread = &value
	l.mu.Unlock()
}

func (l *LiveUpdates) PMUnreadCount() (int, bool) {
	l.mu.Lock()
	defer l.mu.Unlock()
	if l.pmUnread == nil {
		return 0, false
	}
	return *l.pmUnread, true
}

func (l *LiveUpdates) SetPMUnreadCount(value int) {
	value = max(value, 0)
	l.mu.Lock()
	l.pmUnread = &value
	l.mu.Unlock()
}

func (l *LiveUpdates) ConsumeResyncRequest() bool {
	l.mu.Lock()
	defer l.mu.Unlock()
	value := l.resyncRequested
	l.resyncRequested = false
	return value
}

func (l *LiveUpdates) ConsumeTopicListRefreshRequest() bool {
	l.mu.Lock()
	defer l.mu.Unlock()
	value := l.listRefreshRequested
	l.listRefreshRequested = false
	return value
}

func (l *LiveUpdates) WatchTopic(topicID int, lastMessageID *int) {
	if topicID <= 0 {
		l.ClearTopic()
		return
	}
	channel := topicChannel(topicID)
	position := -1
	if lastMessageID != nil {
		position = *lastMessageID
	}
	l.mu.Lock()
	if l.topicChannel == channel {
		if lastMessageID != nil {
			l.positions[channel] = position
		}
		l.mu.Unlock()
		return
	}
	previous := l.topicChannel
	l.topicChannel = channel
	l.clearTopicStateLocked()
	if lastMessageID != nil {
		l.positions[channel] = position
	}
	client := l.client
	l.mu.Unlock()
	if previous != "" {
		client.Unsubscribe(previous)
	}
	l.subscribe(client, channel, position)
}

func (l *LiveUpdates) ClearTopic() {
	l.mu.Lock()
	previous := l.topicChannel
	l.topicChannel = ""
	l.clearTopicStateLocked()
	delete(l.positions, previous)
	client := l.client
	l.mu.Unlock()
	if previous != "" {
		client.Unsubscribe(previous)
	}
}

func (l *LiveUpdates) ConsumeTopicPostIDs(topicID int) []int {
	l.mu.Lock()
	defer l.mu.Unlock()
	if l.topicChannel != topicChannel(topicID) {
		return nil
	}
	out := append([]int{}, l.topicCreated...)
	l.topicCreated = nil
	l.topicCreatedSet = map[int]bool{}
	return out
}

func (l *LiveUpdates) ConsumeTopicChangedPostIDs(topicID int) []int {
	l.mu.Lock()
	defer l.mu.Unlock()
	if l.topicChannel != topicChannel(topicID) {
		return nil
	}
	out := append([]int{}, l.topicChanged...)
	l.topicChanged = nil
	l.topicChangedSet = map[int]bool{}
	return out
}

func (l *LiveUpdates) ConsumeTopicRefreshRequest(topicID int) bool {
	l.mu.Lock()
	defer l.mu.Unlock()
	if l.topicRefresh != topicID {
		return false
	}
	l.topicRefresh = 0
	return true
}

func (l *LiveUpdates) RequeueTopicRefreshRequest(topicID int) {
	l.mu.Lock()
	if l.topicChannel == topicChannel(topicID) {
		l.topicRefresh = topicID
	}
	l.mu.Unlock()
}

func (l *LiveUpdates) ChannelPositions() map[string]int {
	l.mu.Lock()
	defer l.mu.Unlock()
	out := make(map[string]int, len(l.positions))
	for key, value := range l.positions {
		out[key] = value
	}
	return out
}

func (l *LiveUpdates) Monitor(now time.Time) {
	l.mu.Lock()
	if !l.running {
		l.mu.Unlock()
		return
	}
	success := l.client.Success()
	if success > l.lastSuccessCount {
		l.lastSuccessCount = success
		l.lastSuccessAt = now
	}
	reason := ""
	if l.client.Status() != messagebus.Started {
		reason = "stopped"
	} else if !l.lastSuccessAt.IsZero() && now.Sub(l.lastSuccessAt) > l.options.WatchdogStaleThreshold {
		reason = "stale"
	}
	l.mu.Unlock()
	if reason != "" {
		l.restart(reason, now)
	}
}

func (l *LiveUpdates) subscribeInitial(client Bus) {
	l.subscribe(client, "/latest", -1)
	if l.options.CurrentUserID <= 0 {
		return
	}
	l.subscribe(client, "/new", -1)
	l.subscribe(client, "/unread", -1)
	l.subscribe(client, fmt.Sprintf("/unread/%d", l.options.CurrentUserID), -1)
	position := -1
	if l.options.NotificationChannelPosition != nil {
		position = *l.options.NotificationChannelPosition
	}
	l.subscribe(client, fmt.Sprintf("/notification/%d", l.options.CurrentUserID), position)
}

func (l *LiveUpdates) subscribe(client Bus, channel string, explicit int) {
	l.mu.Lock()
	position, present := l.positions[channel]
	if !present {
		position = explicit
		l.positions[channel] = position
	}
	l.mu.Unlock()
	err := client.Subscribe(channel, position, func(data map[string]any, messageID, _ int) {
		l.handleMessage(channel, data, messageID)
	})
	if err != nil {
		l.debug("live_updates_subscribe_error channel=%s error=%v", channel, err)
	}
}

func (l *LiveUpdates) handleMessage(channel string, data map[string]any, messageID int) {
	if data == nil {
		data = map[string]any{}
	}
	l.mu.Lock()
	l.positions[channel] = messageID
	isNotification := channel == fmt.Sprintf("/notification/%d", l.options.CurrentUserID)
	isTopic := l.topicChannel == channel && strings.HasPrefix(channel, "/topic/")
	l.mu.Unlock()
	if isNotification {
		l.updateCounts(data)
		return
	}
	if isTopic {
		l.handleTopicMessage(channel, data)
		return
	}
	if !l.countMessage(channel, data) {
		return
	}
	topicID := discourse.Int(data["topic_id"])
	if topicID <= 0 {
		return
	}
	l.mu.Lock()
	l.addIncomingLocked(topicID)
	l.mu.Unlock()
}

func (l *LiveUpdates) countMessage(channel string, data map[string]any) bool {
	l.mu.Lock()
	filter := l.filter
	userID := l.options.CurrentUserID
	l.mu.Unlock()
	messageType := discourse.String(data["message_type"])
	unreadChannel := channel == "/unread" || channel == fmt.Sprintf("/unread/%d", userID)
	payload := discourse.Map(data["payload"])
	private := discourse.String(payload["archetype"]) == "private_message"
	switch filter {
	case "latest":
		return (channel == "/latest" && messageType == "latest") || (channel == "/new" && messageType == "new_topic")
	case "new":
		return channel == "/new" && messageType == "new_topic"
	case "unread":
		return unreadChannel && messageType == "unread" && !private
	case "private":
		return unreadChannel && messageType == "unread" && private
	default:
		return false
	}
}

func (l *LiveUpdates) handleTopicMessage(channel string, data map[string]any) {
	messageType := discourse.String(data["type"])
	if discourse.Bool(data["reload_topic"]) || discourse.Bool(data["refresh_stream"]) || messageType == "destroyed" {
		l.requestTopicRefresh(channel)
		return
	}
	if messageType == "stats" {
		if _, exists := data["posts_count"]; exists {
			l.mu.Lock()
			queued := len(l.topicCreated) > 0
			l.mu.Unlock()
			if !queued {
				l.requestTopicRefresh(channel)
			}
			return
		}
	}
	postID := discourse.Int(data["id"])
	if postID <= 0 {
		return
	}
	l.mu.Lock()
	defer l.mu.Unlock()
	if l.topicChannel != channel {
		return
	}
	switch messageType {
	case "created":
		if !l.topicCreatedSet[postID] {
			l.topicCreatedSet[postID] = true
			l.topicCreated = append(l.topicCreated, postID)
		}
	case "acted", "liked", "unliked", "deleted", "recovered":
		if !l.topicChangedSet[postID] {
			l.topicChangedSet[postID] = true
			l.topicChanged = append(l.topicChanged, postID)
		}
	}
}

func (l *LiveUpdates) updateCounts(data map[string]any) {
	l.mu.Lock()
	defer l.mu.Unlock()
	pm := l.pmUnread
	if value, present := data["new_personal_messages_notifications_count"]; present {
		n := max(discourse.Int(value), 0)
		pm = &n
	} else if value, present := data["unread_personal_messages"]; present {
		n := max(discourse.Int(value), 0)
		pm = &n
	}
	unread := l.unread
	allValue, hasAll := data["all_unread_notifications_count"]
	pmValue, hasPM := data["new_personal_messages_notifications_count"]
	oldPMValue, hasOldPM := data["unread_personal_messages"]
	if hasAll && hasPM {
		n := max(discourse.Int(allValue)-discourse.Int(pmValue), 0)
		unread = &n
	} else if hasAll && hasOldPM {
		n := max(discourse.Int(allValue)-discourse.Int(oldPMValue), 0)
		unread = &n
	} else if value, present := data["unread_notifications"]; present {
		n := max(discourse.Int(value), 0)
		unread = &n
	}
	l.pmUnread, l.unread = pm, unread
}

func (l *LiveUpdates) watchdog(stop <-chan struct{}) {
	ticker := time.NewTicker(l.options.WatchdogInterval)
	defer ticker.Stop()
	for {
		select {
		case <-stop:
			return
		case now := <-ticker.C:
			l.Monitor(now)
		}
	}
}

func (l *LiveUpdates) restart(reason string, now time.Time) {
	l.mu.Lock()
	if !l.running || l.restarting {
		l.mu.Unlock()
		return
	}
	l.restarting = true
	oldClient := l.client
	channels := l.subscribedChannelsLocked()
	trustworthy := true
	for _, channel := range channels {
		if position, present := l.positions[channel]; !present || position < 0 {
			trustworthy = false
		}
	}
	l.mu.Unlock()
	l.debug("live_updates_restart reason=%s", reason)
	oldClient.Stop()
	newClient := l.factory()
	for _, channel := range channels {
		l.mu.Lock()
		position := l.positions[channel]
		l.mu.Unlock()
		l.subscribe(newClient, channel, position)
	}
	if err := newClient.Start(); err != nil {
		l.mu.Lock()
		l.restarting = false
		l.mu.Unlock()
		l.debug("live_updates_restart_error reason=%s error=%v", reason, err)
		return
	}
	l.mu.Lock()
	l.client = newClient
	l.lastSuccessCount = newClient.Success()
	l.lastSuccessAt = now
	l.resyncRequested = true
	if !trustworthy {
		l.clearIncomingLocked()
		l.listRefreshRequested = true
		if l.topicChannel != "" {
			l.topicRefresh = topicID(l.topicChannel)
		}
	}
	l.restarting = false
	l.mu.Unlock()
}

func (l *LiveUpdates) subscribedChannelsLocked() []string {
	channels := []string{"/latest"}
	if l.options.CurrentUserID > 0 {
		channels = append(channels, "/new", "/unread", fmt.Sprintf("/unread/%d", l.options.CurrentUserID), fmt.Sprintf("/notification/%d", l.options.CurrentUserID))
	}
	if l.topicChannel != "" {
		channels = append(channels, l.topicChannel)
	}
	return channels
}

func (l *LiveUpdates) requestTopicRefresh(channel string) {
	id := topicID(channel)
	if id <= 0 {
		return
	}
	l.mu.Lock()
	defer l.mu.Unlock()
	if l.topicChannel != channel {
		return
	}
	l.clearTopicStateLocked()
	l.topicRefresh = id
}

func (l *LiveUpdates) addIncomingLocked(id int) {
	if l.incomingSet[id] {
		return
	}
	l.incomingSet[id] = true
	l.incomingOrder = append(l.incomingOrder, id)
	for len(l.incomingOrder) > l.options.MaxIncomingTopicIDs {
		expired := l.incomingOrder[0]
		l.incomingOrder = l.incomingOrder[1:]
		delete(l.incomingSet, expired)
	}
}

func (l *LiveUpdates) clearIncomingLocked() {
	l.incomingSet = map[int]bool{}
	l.incomingOrder = nil
}

func (l *LiveUpdates) clearTopicStateLocked() {
	l.topicCreated = nil
	l.topicCreatedSet = map[int]bool{}
	l.topicChanged = nil
	l.topicChangedSet = map[int]bool{}
	l.topicRefresh = 0
}

func (l *LiveUpdates) debug(format string, args ...any) {
	if l.options.Debug != nil {
		l.options.Debug(fmt.Sprintf(format, args...))
	}
}

func topicChannel(id int) string { return "/topic/" + strconv.Itoa(id) }

func topicID(channel string) int {
	if !strings.HasPrefix(channel, "/topic/") {
		return 0
	}
	value, _ := strconv.Atoi(strings.TrimPrefix(channel, "/topic/"))
	return value
}
