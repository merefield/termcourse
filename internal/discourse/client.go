package discourse

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/http/cookiejar"
	"net/url"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"time"
)

const userAgent = "Mozilla/5.0 (Macintosh; Intel Mac OS X 10_15_7) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/121.0.0.0 Safari/537.36"

type JSON = map[string]any

type HTTPError struct {
	Status     int
	Body       []byte
	Header     http.Header
	ReceivedAt time.Time
}

type RateLimitInfo struct {
	Wait           time.Duration
	RetryAt        time.Time
	ServerTimeLeft string
	TimingProvided bool
	Code           string
}

func (e *HTTPError) Error() string {
	if msg := ErrorMessage(e.Body); msg != "" {
		return fmt.Sprintf("HTTP %d: %s", e.Status, msg)
	}
	return fmt.Sprintf("HTTP %d", e.Status)
}

func (e *HTTPError) RateLimit() (RateLimitInfo, bool) {
	var payload struct {
		ErrorType string `json:"error_type"`
		Extras    struct {
			WaitSeconds any `json:"wait_seconds"`
			TimeLeft    any `json:"time_left"`
		} `json:"extras"`
	}
	_ = json.Unmarshal(e.Body, &payload)
	if e.Status != http.StatusTooManyRequests && payload.ErrorType != "rate_limit" {
		return RateLimitInfo{}, false
	}

	receivedAt := e.ReceivedAt
	if receivedAt.IsZero() {
		receivedAt = time.Now()
	}
	wait, timingProvided := retryAfter(e.Header.Get("Retry-After"), receivedAt)
	serverTimeLeft := ""
	if !timingProvided {
		if seconds, ok := numericSeconds(payload.Extras.WaitSeconds); ok {
			wait, timingProvided = time.Duration(seconds*float64(time.Second)), true
		} else if seconds, ok := numericSeconds(payload.Extras.TimeLeft); ok {
			wait, timingProvided = time.Duration(seconds*float64(time.Second)), true
		} else if label, ok := payload.Extras.TimeLeft.(string); ok && strings.TrimSpace(label) != "" {
			serverTimeLeft, timingProvided = strings.TrimSpace(label), true
		}
	}
	if wait < 0 {
		wait = 0
	}
	return RateLimitInfo{
		Wait:           wait,
		RetryAt:        receivedAt.Add(wait),
		ServerTimeLeft: serverTimeLeft,
		TimingProvided: timingProvided,
		Code:           e.Header.Get("Discourse-Rate-Limit-Error-Code"),
	}, true
}

func numericSeconds(value any) (float64, bool) {
	switch number := value.(type) {
	case float64:
		return number, true
	case json.Number:
		seconds, err := number.Float64()
		return seconds, err == nil
	case string:
		seconds, err := strconv.ParseFloat(strings.TrimSpace(number), 64)
		return seconds, err == nil
	default:
		return 0, false
	}
}

func retryAfter(value string, receivedAt time.Time) (time.Duration, bool) {
	value = strings.TrimSpace(value)
	if value == "" {
		return 0, false
	}
	if seconds, err := strconv.ParseFloat(value, 64); err == nil {
		return time.Duration(seconds * float64(time.Second)), true
	}
	when, err := http.ParseTime(value)
	if err != nil {
		return 0, false
	}
	return when.Sub(receivedAt), true
}

func newHTTPError(response *http.Response, body []byte) *HTTPError {
	return &HTTPError{
		Status:     response.StatusCode,
		Body:       body,
		Header:     response.Header.Clone(),
		ReceivedAt: time.Now(),
	}
}

type Client struct {
	BaseURL     string
	APIKey      string
	APIUsername string

	client     *http.Client
	ipv4Client *http.Client
	jar        http.CookieJar
	csrf       string
	preferIPv4 bool
	debug      bool
	mu         sync.Mutex
}

func NewClient(baseURL, apiKey, apiUsername string) (*Client, error) {
	baseURL = strings.TrimRight(baseURL, "/")
	if _, err := url.ParseRequestURI(baseURL); err != nil {
		return nil, err
	}
	jar, _ := cookiejar.New(nil)
	makeClient := func(network string) *http.Client {
		dialer := &net.Dialer{Timeout: 3 * time.Second, KeepAlive: 30 * time.Second}
		transport := &http.Transport{
			Proxy: http.ProxyFromEnvironment,
			DialContext: func(ctx context.Context, _, address string) (net.Conn, error) {
				return dialer.DialContext(ctx, network, address)
			},
			ForceAttemptHTTP2:     true,
			MaxIdleConns:          20,
			IdleConnTimeout:       90 * time.Second,
			TLSHandshakeTimeout:   5 * time.Second,
			ResponseHeaderTimeout: 15 * time.Second,
		}
		return &http.Client{
			Transport: transport,
			Jar:       jar,
			Timeout:   20 * time.Second,
			CheckRedirect: func(_ *http.Request, _ []*http.Request) error {
				return http.ErrUseLastResponse
			},
		}
	}
	return &Client{
		BaseURL: baseURL, APIKey: apiKey, APIUsername: apiUsername,
		client: makeClient("tcp"), ipv4Client: makeClient("tcp4"), jar: jar,
	}, nil
}

func (c *Client) SetDebug(enabled bool) { c.debug = enabled }

func (c *Client) LatestTopics() (JSON, error) { return c.getJSON("/latest.json", nil) }

func (c *Client) ListTopics(filter, period, username string, params url.Values) (JSON, error) {
	path := "/latest.json"
	switch filter {
	case "hot":
		path = "/hot.json"
	case "private":
		if strings.TrimSpace(username) != "" {
			path = "/topics/private-messages/" + url.PathEscape(username) + ".json"
		} else {
			path = "/topics/private-messages.json"
		}
	case "new":
		path = "/new.json"
	case "unread":
		path = "/unread.json"
	case "top":
		path = "/top.json"
	}
	params = cloneValues(params)
	if filter == "top" {
		params.Set("period", period)
	}
	result, err := c.getJSON(path, params)
	var httpErr *HTTPError
	if filter == "private" && strings.Contains(path, "/private-messages/") && errors.As(err, &httpErr) && httpErr.Status == http.StatusNotFound {
		return c.getJSON("/topics/private-messages.json", params)
	}
	return result, err
}

func (c *Client) Search(query string) (JSON, error) {
	return c.getJSON("/search.json", url.Values{"q": {query}})
}

func (c *Client) Notifications(offset, limit int, filter string) (JSON, error) {
	values := url.Values{"offset": {strconv.Itoa(offset)}, "limit": {strconv.Itoa(limit)}}
	if filter != "" {
		values.Set("filter", filter)
	}
	return c.getJSON("/notifications.json", values)
}

func (c *Client) MarkNotificationRead(id int) (JSON, error) {
	return c.requestJSON(http.MethodPut, "/notifications/mark-read", JSON{"id": id})
}

func (c *Client) NotificationTotals() (JSON, error) {
	return c.getJSON("/notifications/totals.json", nil)
}

func (c *Client) Topic(id int, nearPost int) (JSON, error) {
	path := fmt.Sprintf("/t/%d.json", id)
	if nearPost > 0 {
		path = fmt.Sprintf("/t/%d/%d.json", id, nearPost)
	}
	return c.getJSON(path, url.Values{"print": {"true"}, "include_raw": {"true"}})
}

func (c *Client) TopicPosts(topicID int, postIDs []int, includeRaw bool) (JSON, error) {
	values := url.Values{"include_suggested": {"false"}}
	for _, id := range postIDs {
		values.Add("post_ids[]", strconv.Itoa(id))
	}
	if includeRaw {
		values.Set("include_raw", "true")
	}
	return c.getJSON(fmt.Sprintf("/t/%d/posts.json", topicID), values)
}

func (c *Client) Post(id int) (JSON, error) {
	return c.getJSON(fmt.Sprintf("/posts/%d.json", id), nil)
}

func (c *Client) LikePost(id int) (JSON, error) {
	return c.requestJSON(http.MethodPost, "/post_actions.json", JSON{"id": id, "post_action_type_id": 2})
}

func (c *Client) UnlikePost(id int) (JSON, error) {
	return c.requestJSON(http.MethodDelete, fmt.Sprintf("/post_actions/%d.json", id), JSON{"post_action_type_id": 2})
}

func (c *Client) CreatePost(topicID int, raw string, replyToPostNumber int) (JSON, error) {
	payload := JSON{"topic_id": topicID, "raw": raw}
	if replyToPostNumber > 0 {
		payload["reply_to_post_number"] = replyToPostNumber
	}
	return c.requestJSON(http.MethodPost, "/posts.json", payload)
}

func (c *Client) CreateTopic(title, raw string, category *int) (JSON, error) {
	payload := JSON{"title": title, "raw": raw}
	if category != nil {
		payload["category"] = *category
	}
	return c.requestJSON(http.MethodPost, "/posts.json", payload)
}

func (c *Client) SiteInfo() (JSON, error) { return c.getJSON("/site.json", nil) }

func (c *Client) CurrentUser() (JSON, error) {
	data, err := c.getJSON("/session/current.json", nil)
	var httpErr *HTTPError
	if errors.As(err, &httpErr) && httpErr.Status == http.StatusNotFound {
		return nil, nil
	}
	return data, err
}

func (c *Client) Login(username, password, otp string, otpMethod int) (JSON, error) {
	c.log("login_start")
	_, _ = c.EnsureCSRF()
	payload := JSON{"login": username, "password": password}
	if otp != "" {
		payload["second_factor_token"] = otp
		payload["second_factor_method"] = otpMethod
	}
	data, err := c.requestJSON(http.MethodPost, "/session.json", payload)
	var httpErr *HTTPError
	if errors.As(err, &httpErr) {
		var parsed JSON
		if json.Unmarshal(httpErr.Body, &parsed) != nil {
			parsed = JSON{"error": string(httpErr.Body)}
		}
		parsed["__http_status"] = httpErr.Status
		return parsed, nil
	}
	return data, err
}

func (c *Client) EnsureCSRF() (string, error) {
	c.mu.Lock()
	if c.csrf != "" {
		token := c.csrf
		c.mu.Unlock()
		return token, nil
	}
	c.mu.Unlock()
	data, err := c.getJSON("/session/csrf.json", nil)
	if err == nil {
		if token := String(data["csrf"]); token != "" {
			c.mu.Lock()
			c.csrf = token
			c.mu.Unlock()
			return token, nil
		}
	}
	body, getErr := c.getBytes("/", 2<<20, 0)
	if getErr != nil {
		return "", err
	}
	marker := `name="csrf-token" content="`
	if start := bytes.Index(body, []byte(marker)); start >= 0 {
		start += len(marker)
		if end := bytes.IndexByte(body[start:], '"'); end >= 0 {
			token := string(body[start : start+end])
			c.mu.Lock()
			c.csrf = token
			c.mu.Unlock()
			return token, nil
		}
	}
	return "", errors.New("CSRF token missing")
}

func (c *Client) MessageBusHeaders() http.Header {
	headers := http.Header{"User-Agent": {userAgent}}
	root, _ := url.Parse(c.BaseURL + "/")
	var cookies []string
	for _, cookie := range c.jar.Cookies(root) {
		cookies = append(cookies, cookie.Name+"="+cookie.Value)
	}
	if len(cookies) > 0 {
		headers.Set("Cookie", strings.Join(cookies, "; "))
	}
	return headers
}

func (c *Client) UpdateTopicReadState(topicID, postNumber, topicTimeMS int) bool {
	if postNumber <= 0 {
		return false
	}
	key := strconv.Itoa(postNumber)
	payloads := []struct {
		path string
		data JSON
	}{
		{"/topics/timings", JSON{"topic_id": topicID, "topic_time": topicTimeMS, "timings": JSON{key: topicTimeMS}}},
		{"/topics/timings", JSON{"topic_id": topicID, "topic_time": topicTimeMS, "timings": JSON{key: strconv.Itoa(topicTimeMS)}}},
		{fmt.Sprintf("/t/%d/timings", topicID), JSON{"topic_time": topicTimeMS, "timings": JSON{key: topicTimeMS}}},
		{fmt.Sprintf("/t/%d/timings", topicID), JSON{"topic_time": topicTimeMS, "timings": JSON{key: strconv.Itoa(topicTimeMS)}}},
	}
	for _, item := range payloads {
		if _, err := c.requestJSON(http.MethodPost, item.path, item.data); err == nil {
			return true
		}
	}
	return false
}

func (c *Client) GetURL(pathOrURL string) (JSON, error) {
	return c.getJSON(pathOrURL, nil)
}

func (c *Client) GetBytes(pathOrURL string, maxBytes int) ([]byte, error) {
	return c.getBytes(pathOrURL, maxBytes, 4)
}

func (c *Client) getBytes(pathOrURL string, maxBytes, redirects int) ([]byte, error) {
	response, err := c.perform(http.MethodGet, pathOrURL, nil)
	if err != nil {
		return nil, err
	}
	defer response.Body.Close()
	if response.StatusCode >= 300 && response.StatusCode < 400 {
		if redirects <= 0 {
			return nil, errors.New("too many redirects")
		}
		location := response.Header.Get("Location")
		if location == "" {
			return nil, errors.New("redirect without location")
		}
		base, _ := url.Parse(c.BaseURL + "/")
		next, err := base.Parse(location)
		if err != nil {
			return nil, err
		}
		return c.getBytes(next.String(), maxBytes, redirects-1)
	}
	if response.StatusCode >= 400 {
		body, _ := io.ReadAll(io.LimitReader(response.Body, 1<<20))
		return nil, newHTTPError(response, body)
	}
	limit := int64(maxBytes)
	if limit <= 0 {
		limit = 20 << 20
	}
	body, err := io.ReadAll(io.LimitReader(response.Body, limit+1))
	if err != nil {
		return nil, err
	}
	if len(body) > int(limit) {
		return nil, errors.New("image too large")
	}
	if len(body) == 0 {
		return nil, errors.New("empty image body")
	}
	return body, nil
}

func (c *Client) getJSON(path string, values url.Values) (JSON, error) {
	if len(values) > 0 {
		separator := "?"
		if strings.Contains(path, "?") {
			separator = "&"
		}
		path += separator + values.Encode()
	}
	return c.requestJSON(http.MethodGet, path, nil)
}

func (c *Client) requestJSON(method, path string, payload JSON) (JSON, error) {
	response, err := c.perform(method, path, payload)
	if err != nil {
		return nil, err
	}
	defer response.Body.Close()
	body, err := io.ReadAll(response.Body)
	if err != nil {
		return nil, err
	}
	if response.StatusCode < 200 || response.StatusCode >= 300 {
		return nil, newHTTPError(response, body)
	}
	if len(bytes.TrimSpace(body)) == 0 {
		return JSON{}, nil
	}
	var result JSON
	if err := json.Unmarshal(body, &result); err != nil {
		return nil, err
	}
	return result, nil
}

func (c *Client) perform(method, path string, payload JSON) (*http.Response, error) {
	target, err := c.resolve(path)
	if err != nil {
		return nil, err
	}
	var body []byte
	if payload != nil {
		body, err = json.Marshal(payload)
		if err != nil {
			return nil, err
		}
	}
	var lastErr error
	for attempt := 0; attempt < 3; attempt++ {
		var reader io.Reader
		if body != nil {
			reader = bytes.NewReader(body)
		}
		request, reqErr := http.NewRequest(method, target, reader)
		if reqErr != nil {
			return nil, reqErr
		}
		request.Header.Set("Content-Type", "application/json")
		request.Header.Set("Accept", "application/json")
		request.Header.Set("User-Agent", userAgent)
		if strings.TrimSpace(c.APIKey) != "" {
			request.Header.Set("Api-Key", c.APIKey)
		}
		if strings.TrimSpace(c.APIUsername) != "" {
			request.Header.Set("Api-Username", c.APIUsername)
		}
		c.mu.Lock()
		csrf, ipv4 := c.csrf, c.preferIPv4
		c.mu.Unlock()
		if csrf != "" {
			request.Header.Set("X-CSRF-Token", csrf)
		}
		started := time.Now()
		httpClient := c.client
		if ipv4 {
			httpClient = c.ipv4Client
		}
		c.log("http_request method=%s path=%s ipv4=%t", method, path, ipv4)
		response, requestErr := httpClient.Do(request)
		if requestErr == nil {
			c.log(
				"http_response method=%s path=%s status=%d ms=%.1f retry_after=%q rate_limit_code=%q",
				method, path, response.StatusCode, time.Since(started).Seconds()*1000,
				response.Header.Get("Retry-After"), response.Header.Get("Discourse-Rate-Limit-Error-Code"),
			)
			return response, nil
		}
		lastErr = requestErr
		c.log("http_error method=%s path=%s error=%T ms=%.1f", method, path, requestErr, time.Since(started).Seconds()*1000)
		if attempt == 0 && !ipv4 && retryableNetworkError(requestErr) {
			c.mu.Lock()
			c.preferIPv4 = true
			c.mu.Unlock()
			continue
		}
		if attempt < 2 {
			time.Sleep(time.Duration(100*(1<<attempt)) * time.Millisecond)
		}
	}
	return nil, lastErr
}

func (c *Client) resolve(path string) (string, error) {
	if strings.HasPrefix(path, "http://") || strings.HasPrefix(path, "https://") {
		return path, nil
	}
	base, err := url.Parse(c.BaseURL + "/")
	if err != nil {
		return "", err
	}
	relative, err := url.Parse(path)
	if err != nil {
		return "", err
	}
	return base.ResolveReference(relative).String(), nil
}

func (c *Client) log(format string, args ...any) {
	if !c.debug {
		return
	}
	line := fmt.Sprintf("[%s] %s\n", time.Now().UTC().Format(time.RFC3339), fmt.Sprintf(format, args...))
	path := filepath.Join(os.TempDir(), "termcourse_http_debug.txt")
	file, err := os.OpenFile(path, os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0o600)
	if err == nil {
		_, _ = file.WriteString(line)
		_ = file.Close()
	}
}

func cloneValues(values url.Values) url.Values {
	out := url.Values{}
	for key, items := range values {
		out[key] = append([]string{}, items...)
	}
	return out
}

func retryableNetworkError(err error) bool {
	var netErr net.Error
	return errors.As(err, &netErr) && (netErr.Timeout() || netErr.Temporary())
}

func ErrorMessage(body []byte) string {
	var data JSON
	if json.Unmarshal(body, &data) != nil {
		return strings.TrimSpace(string(body))
	}
	if errorsList, ok := data["errors"].([]any); ok {
		var out []string
		for _, item := range errorsList {
			out = append(out, String(item))
		}
		return strings.Join(out, "\n")
	}
	for _, key := range []string{"error", "message"} {
		if value := String(data[key]); value != "" {
			return value
		}
	}
	return strings.TrimSpace(string(body))
}

func String(value any) string {
	switch v := value.(type) {
	case string:
		return v
	case json.Number:
		return v.String()
	case float64:
		return strconv.FormatFloat(v, 'f', -1, 64)
	case int:
		return strconv.Itoa(v)
	case nil:
		return ""
	default:
		return fmt.Sprint(v)
	}
}

func Int(value any) int {
	switch v := value.(type) {
	case int:
		return v
	case float64:
		return int(v)
	case json.Number:
		n, _ := v.Int64()
		return int(n)
	case string:
		n, _ := strconv.Atoi(v)
		return n
	default:
		return 0
	}
}

func Bool(value any) bool {
	switch v := value.(type) {
	case bool:
		return v
	case string:
		b, _ := strconv.ParseBool(v)
		return b
	default:
		return false
	}
}

func Map(value any) JSON {
	if value == nil {
		return nil
	}
	if result, ok := value.(map[string]any); ok {
		return result
	}
	return nil
}

func Slice(value any) []any {
	if result, ok := value.([]any); ok {
		return result
	}
	return nil
}
