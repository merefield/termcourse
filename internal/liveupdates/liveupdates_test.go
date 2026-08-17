package liveupdates

import (
	"net/http"
	"testing"
	"time"

	"github.com/merefield/termcourse/internal/messagebus"
)

type fakeBus struct {
	subscriptions map[string]struct {
		position int
		callback messagebus.Callback
	}
	status  int
	success int64
	starts  int
	stops   int
}

func newFakeBus() *fakeBus {
	return &fakeBus{subscriptions: map[string]struct {
		position int
		callback messagebus.Callback
	}{}}
}

func (f *fakeBus) Subscribe(channel string, position int, callback messagebus.Callback) error {
	f.subscriptions[channel] = struct {
		position int
		callback messagebus.Callback
	}{position, callback}
	return nil
}
func (f *fakeBus) Unsubscribe(channel string) { delete(f.subscriptions, channel) }
func (f *fakeBus) Start() error               { f.status, f.starts = messagebus.Started, f.starts+1; return nil }
func (f *fakeBus) Stop()                      { f.status, f.stops = messagebus.Stopped, f.stops+1 }
func (f *fakeBus) Status() int                { return f.status }
func (f *fakeBus) Success() int64             { return f.success }
func (f *fakeBus) emit(channel string, data map[string]any, id int) {
	f.subscriptions[channel].callback(data, id, id)
}

func TestExpectedSubscriptionsAndPositions(t *testing.T) {
	client := newFakeBus()
	position := 123
	updates := New("https://example.com", http.Header{}, Options{
		CurrentUserID: 42, NotificationChannelPosition: &position, Client: client,
		WatchdogInterval: time.Hour,
	})
	expected := []string{"/latest", "/new", "/unread", "/unread/42", "/notification/42"}
	for _, channel := range expected {
		if _, present := client.subscriptions[channel]; !present {
			t.Fatalf("missing subscription %s", channel)
		}
	}
	if client.subscriptions["/notification/42"].position != 123 {
		t.Fatalf("notification position = %d", client.subscriptions["/notification/42"].position)
	}
	if err := updates.Start(); err != nil {
		t.Fatal(err)
	}
	updates.Stop()
	if client.starts != 1 || client.stops != 1 {
		t.Fatalf("starts=%d stops=%d", client.starts, client.stops)
	}
}

func TestFilterCountingDedupAndBound(t *testing.T) {
	client := newFakeBus()
	updates := New("https://example.com", nil, Options{
		CurrentUserID: 42, Client: client, MaxIncomingTopicIDs: 3,
	})
	updates.Track("latest")
	client.emit("/latest", map[string]any{"topic_id": 1, "message_type": "latest"}, 1)
	client.emit("/latest", map[string]any{"topic_id": 1, "message_type": "latest"}, 2)
	client.emit("/new", map[string]any{"topic_id": 2, "message_type": "new_topic"}, 3)
	client.emit("/latest", map[string]any{"topic_id": 3, "message_type": "latest"}, 4)
	client.emit("/latest", map[string]any{"topic_id": 4, "message_type": "latest"}, 5)
	if updates.IncomingCount() != 3 {
		t.Fatalf("count = %d, IDs=%v", updates.IncomingCount(), updates.IncomingTopicIDs())
	}
	updates.Track("unread")
	client.emit("/unread", map[string]any{"topic_id": 9, "message_type": "unread", "payload": map[string]any{"archetype": "regular"}}, 6)
	client.emit("/unread/42", map[string]any{"topic_id": 10, "message_type": "unread", "payload": map[string]any{"archetype": "private_message"}}, 7)
	if updates.IncomingCount() != 1 || updates.IncomingTopicIDs()[0] != 9 {
		t.Fatalf("unread IDs = %v", updates.IncomingTopicIDs())
	}
}

func TestNotificationCountsPreservePartialPayload(t *testing.T) {
	client := newFakeBus()
	updates := New("https://example.com", nil, Options{CurrentUserID: 42, Client: client})
	updates.SetUnreadNotificationCount(4)
	updates.SetPMUnreadCount(1)
	client.emit("/notification/42", map[string]any{"all_unread_notifications_count": 7}, 1)
	unread, unreadOK := updates.UnreadNotificationCount()
	pm, pmOK := updates.PMUnreadCount()
	if !unreadOK || !pmOK || unread != 4 || pm != 1 {
		t.Fatalf("unread=%d/%v pm=%d/%v", unread, unreadOK, pm, pmOK)
	}
	client.emit("/notification/42", map[string]any{
		"all_unread_notifications_count":            7,
		"new_personal_messages_notifications_count": 2,
	}, 2)
	unread, _ = updates.UnreadNotificationCount()
	pm, _ = updates.PMUnreadCount()
	if unread != 5 || pm != 2 {
		t.Fatalf("unread=%d pm=%d", unread, pm)
	}
}

func TestTopicMessages(t *testing.T) {
	client := newFakeBus()
	updates := New("https://example.com", nil, Options{CurrentUserID: 42, Client: client})
	position := 888
	updates.WatchTopic(55, &position)
	if client.subscriptions["/topic/55"].position != 888 {
		t.Fatal("topic position not used")
	}
	client.emit("/topic/55", map[string]any{"type": "created", "id": 90}, 889)
	client.emit("/topic/55", map[string]any{"type": "liked", "id": 80}, 890)
	if ids := updates.ConsumeTopicPostIDs(55); len(ids) != 1 || ids[0] != 90 {
		t.Fatalf("created = %v", ids)
	}
	if ids := updates.ConsumeTopicChangedPostIDs(55); len(ids) != 1 || ids[0] != 80 {
		t.Fatalf("changed = %v", ids)
	}
	client.emit("/topic/55", map[string]any{"reload_topic": true}, 891)
	if !updates.ConsumeTopicRefreshRequest(55) {
		t.Fatal("expected refresh")
	}
}
