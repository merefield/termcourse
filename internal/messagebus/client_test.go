package messagebus

import (
	"encoding/json"
	"testing"
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
