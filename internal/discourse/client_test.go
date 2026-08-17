package discourse

import (
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
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
