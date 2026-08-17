package discourse

import (
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

func TestClientListEndpointsAndHeaders(t *testing.T) {
	var path, apiKey, apiUser string
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		path, apiKey, apiUser = r.URL.RequestURI(), r.Header.Get("Api-Key"), r.Header.Get("Api-Username")
		_ = json.NewEncoder(w).Encode(JSON{"topic_list": JSON{"topics": []any{}}})
	}))
	defer server.Close()
	client, _ := NewClient(server.URL, "key", "robert")
	if _, err := client.ListTopics("top", "monthly", "", nil); err != nil {
		t.Fatal(err)
	}
	if path != "/top.json?period=monthly" || apiKey != "key" || apiUser != "robert" {
		t.Fatalf("request = %q key=%q user=%q", path, apiKey, apiUser)
	}
}

func TestTopicUsesStandardChunkedJSONRequest(t *testing.T) {
	var requests []string
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		requests = append(requests, r.URL.RequestURI())
		_, _ = io.WriteString(w, `{"post_stream":{"posts":[],"stream":[]}}`)
	}))
	defer server.Close()
	client, _ := NewClient(server.URL, "", "")

	if _, err := client.Topic(42, 0); err != nil {
		t.Fatal(err)
	}
	if _, err := client.Topic(42, 73); err != nil {
		t.Fatal(err)
	}
	want := []string{"/t/42.json?include_raw=true", "/t/42/73.json?include_raw=true"}
	if len(requests) != len(want) {
		t.Fatalf("requests = %#v, want %#v", requests, want)
	}
	for index := range want {
		if requests[index] != want[index] || strings.Contains(requests[index], "print=") {
			t.Fatalf("request %d = %q, want %q without print mode", index, requests[index], want[index])
		}
	}
}

func TestLoginCSRFAndCookieHeaders(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/session/csrf.json":
			http.SetCookie(w, &http.Cookie{Name: "_forum_session", Value: "abc", Path: "/"})
			_, _ = io.WriteString(w, `{"csrf":"token"}`)
		case "/session.json":
			if r.Header.Get("X-CSRF-Token") != "token" {
				t.Errorf("missing CSRF header")
			}
			_, _ = io.WriteString(w, `{"user":{"username":"robert"}}`)
		}
	}))
	defer server.Close()
	client, _ := NewClient(server.URL, "", "")
	login, err := client.Login("robert", "password", "", 1)
	if err != nil || String(Map(login["user"])["username"]) != "robert" {
		t.Fatalf("login = %#v, %v", login, err)
	}
	if cookie := client.MessageBusHeaders().Get("Cookie"); !strings.Contains(cookie, "_forum_session=abc") {
		t.Fatalf("cookie header = %q", cookie)
	}
}

func TestGetBytesLimit(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_, _ = io.WriteString(w, "12345")
	}))
	defer server.Close()
	client, _ := NewClient(server.URL, "", "")
	if _, err := client.GetBytes("/", 4); err == nil {
		t.Fatal("expected size limit error")
	}
}

func TestClientPreservesRateLimitMetadata(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Retry-After", "90")
		w.Header().Set("Discourse-Rate-Limit-Error-Code", "topic_view")
		w.WriteHeader(http.StatusTooManyRequests)
		_, _ = io.WriteString(w, `{"errors":["Too many requests"],"error_type":"rate_limit","extras":{"wait_seconds":45}}`)
	}))
	defer server.Close()
	client, _ := NewClient(server.URL, "", "")

	_, err := client.Topic(42, 0)
	var httpErr *HTTPError
	if !errors.As(err, &httpErr) {
		t.Fatalf("error = %T %v", err, err)
	}
	limit, ok := httpErr.RateLimit()
	if !ok || limit.Wait != 90*time.Second || limit.Code != "topic_view" {
		t.Fatalf("rate limit = %#v, present=%v", limit, ok)
	}
}

func TestRateLimitFallsBackToDiscourseJSON(t *testing.T) {
	receivedAt := time.Date(2026, 8, 17, 12, 0, 0, 0, time.UTC)
	httpErr := &HTTPError{
		Status:     http.StatusTooManyRequests,
		Body:       []byte(`{"error_type":"rate_limit","extras":{"wait_seconds":45}}`),
		ReceivedAt: receivedAt,
	}
	limit, ok := httpErr.RateLimit()
	if !ok || limit.Wait != 45*time.Second || !limit.RetryAt.Equal(receivedAt.Add(45*time.Second)) {
		t.Fatalf("rate limit = %#v, present=%v", limit, ok)
	}
}

func TestRetryAfterAcceptsSecondsAndHTTPDate(t *testing.T) {
	receivedAt := time.Date(2026, 8, 17, 12, 0, 0, 0, time.UTC)
	if wait, ok := retryAfter("12.5", receivedAt); !ok || wait != 12*time.Second+500*time.Millisecond {
		t.Fatalf("numeric retry-after = %v, present=%v", wait, ok)
	}
	date := receivedAt.Add(2 * time.Minute).Format(http.TimeFormat)
	if wait, ok := retryAfter(date, receivedAt); !ok || wait != 2*time.Minute {
		t.Fatalf("date retry-after = %v, present=%v", wait, ok)
	}
}

func TestRateLimitDistinguishesZeroHumanAndMissingTiming(t *testing.T) {
	receivedAt := time.Date(2026, 8, 17, 12, 0, 0, 0, time.UTC)
	tests := []struct {
		name           string
		body           string
		timingProvided bool
		serverTimeLeft string
	}{
		{"zero", `{"error_type":"rate_limit","extras":{"wait_seconds":0}}`, true, ""},
		{"human", `{"error_type":"rate_limit","extras":{"time_left":"about 2 minutes"}}`, true, "about 2 minutes"},
		{"missing", `{"error_type":"rate_limit"}`, false, ""},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			limit, ok := (&HTTPError{
				Status: http.StatusTooManyRequests, Body: []byte(test.body), ReceivedAt: receivedAt,
			}).RateLimit()
			if !ok || limit.TimingProvided != test.timingProvided || limit.ServerTimeLeft != test.serverTimeLeft {
				t.Fatalf("rate limit = %#v, present=%v", limit, ok)
			}
		})
	}
}
