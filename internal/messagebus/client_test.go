package messagebus

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"
)

func TestSplitChunks(t *testing.T) {
	input := []byte("[{\"channel\":\"/one\"}]\r\n|\r\ntrailing")
	advance, token, err := splitChunks(input, false)
	if err != nil || advance == 0 || string(token) != `[{"channel":"/one"}]` {
		t.Fatalf("split = %d %q %v", advance, token, err)
	}
}

func TestNotifyUpdatesPositionAndCallsCallback(t *testing.T) {
	client := New("https://example.com", nil)
	called := false
	if err := client.Subscribe("/latest", -1, func(data map[string]any, messageID, globalID int) {
		called = data["topic_id"].(float64) == 42 && messageID == 7 && globalID == 9
	}); err != nil {
		t.Fatal(err)
	}
	body, _ := json.Marshal([]map[string]any{{
		"channel": "/latest", "message_id": 7, "global_id": 9, "data": map[string]any{"topic_id": 42},
	}})
	if err := client.notify(body); err != nil {
		t.Fatal(err)
	}
	if !called || client.Positions()["/latest"] != 7 {
		t.Fatalf("called=%v positions=%v", called, client.Positions())
	}
}

func TestPollHonorsReadTimeout(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		writer.WriteHeader(http.StatusOK)
		if flusher, ok := writer.(http.Flusher); ok {
			flusher.Flush()
		}
		<-request.Context().Done()
	}))
	defer server.Close()

	client := New(server.URL, nil)
	client.OpenTimeout = 20 * time.Millisecond
	client.ReadTimeout = 40 * time.Millisecond
	started := time.Now()
	err := client.poll(context.Background())
	if !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("poll error = %v, want context deadline exceeded", err)
	}
	if elapsed := time.Since(started); elapsed > time.Second {
		t.Fatalf("poll took %v despite configured timeout", elapsed)
	}
}
