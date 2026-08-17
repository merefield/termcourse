package messagebus

import (
	"bufio"
	"bytes"
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"io"
	"net"
	"net/http"
	"strings"
	"sync"
	"sync/atomic"
	"time"
)

const (
	Stopped = 0
	Started = 1
)

type Callback func(data map[string]any, messageID, globalID int)

type subscription struct {
	last      int
	callbacks []Callback
}

type Client struct {
	baseURL string
	headers http.Header
	id      string

	mu       sync.RWMutex
	channels map[string]*subscription
	status   atomic.Int32
	success  atomic.Int64
	failed   atomic.Int64
	cancel   context.CancelFunc

	OpenTimeout time.Duration
	ReadTimeout time.Duration
	MinInterval time.Duration
	MaxInterval time.Duration
}

func New(baseURL string, headers http.Header) *Client {
	random := make([]byte, 16)
	_, _ = rand.Read(random)
	return &Client{
		baseURL: strings.TrimRight(baseURL, "/"), headers: headers.Clone(), id: hex.EncodeToString(random),
		channels:    make(map[string]*subscription),
		OpenTimeout: 10 * time.Second, ReadTimeout: 120 * time.Second,
		MinInterval: 100 * time.Millisecond, MaxInterval: 180 * time.Second,
	}
}

func (c *Client) Subscribe(channel string, lastMessageID int, callback Callback) error {
	if !strings.HasPrefix(channel, "/") {
		return errors.New("message bus channel must start with /")
	}
	if callback == nil {
		return errors.New("message bus callback is required")
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	item := c.channels[channel]
	if item == nil {
		item = &subscription{last: lastMessageID}
		c.channels[channel] = item
	} else {
		item.last = lastMessageID
	}
	item.callbacks = append(item.callbacks, callback)
	return nil
}

func (c *Client) Unsubscribe(channel string) {
	c.mu.Lock()
	delete(c.channels, channel)
	c.mu.Unlock()
}

func (c *Client) Start() error {
	if !c.status.CompareAndSwap(Stopped, Started) {
		return nil
	}
	ctx, cancel := context.WithCancel(context.Background())
	c.mu.Lock()
	c.cancel = cancel
	c.mu.Unlock()
	go c.loop(ctx)
	return nil
}

func (c *Client) Stop() {
	if c.status.Swap(Stopped) == Stopped {
		return
	}
	c.mu.Lock()
	cancel := c.cancel
	c.cancel = nil
	c.mu.Unlock()
	if cancel != nil {
		cancel()
	}
}

func (c *Client) Status() int    { return int(c.status.Load()) }
func (c *Client) Success() int64 { return c.success.Load() }
func (c *Client) Failed() int64  { return c.failed.Load() }

func (c *Client) Positions() map[string]int {
	c.mu.RLock()
	defer c.mu.RUnlock()
	result := make(map[string]int, len(c.channels))
	for name, item := range c.channels {
		result[name] = item.last
	}
	return result
}

func (c *Client) loop(ctx context.Context) {
	defer c.status.Store(Stopped)
	for c.status.Load() == Started {
		if len(c.Positions()) == 0 {
			if !wait(ctx, c.MinInterval) {
				return
			}
			continue
		}
		if err := c.poll(ctx); err != nil {
			failures := c.failed.Add(1)
			delay := c.MinInterval
			if failures > 2 {
				for i := int64(0); i < failures && delay < c.MaxInterval; i++ {
					delay *= 2
				}
				if delay > c.MaxInterval {
					delay = c.MaxInterval
				}
			}
			if !wait(ctx, delay) {
				return
			}
			continue
		}
		c.success.Add(1)
		c.failed.Store(0)
		if !wait(ctx, c.MinInterval) {
			return
		}
	}
}

func (c *Client) poll(ctx context.Context) error {
	payload, err := json.Marshal(c.Positions())
	if err != nil {
		return err
	}
	target := c.baseURL + "/message-bus/" + c.id + "/poll"
	request, err := http.NewRequestWithContext(ctx, http.MethodPost, target, bytes.NewReader(payload))
	if err != nil {
		return err
	}
	for key, values := range c.headers {
		request.Header[key] = append([]string{}, values...)
	}
	request.Header.Set("Content-Type", "application/json")
	request.Header.Set("X-Silence-logger", "true")

	transport := &http.Transport{
		Proxy:               http.ProxyFromEnvironment,
		DialContext:         (&netDialer{timeout: c.OpenTimeout}).DialContext,
		TLSHandshakeTimeout: c.OpenTimeout,
	}
	client := &http.Client{Transport: transport}
	response, err := client.Do(request)
	if err != nil {
		return err
	}
	defer response.Body.Close()
	if response.StatusCode < 200 || response.StatusCode >= 300 {
		_, _ = io.Copy(io.Discard, io.LimitReader(response.Body, 64<<10))
		return errors.New(response.Status)
	}

	// Discourse's MessageBus long-poll stream separates JSON arrays with
	// CRLF|CRLF. Scanner's split function keeps partial chunks buffered.
	scanner := bufio.NewScanner(response.Body)
	scanner.Buffer(make([]byte, 4096), 8<<20)
	scanner.Split(splitChunks)
	for scanner.Scan() {
		if err := c.notify(scanner.Bytes()); err != nil {
			return err
		}
	}
	return scanner.Err()
}

func splitChunks(data []byte, atEOF bool) (advance int, token []byte, err error) {
	separator := []byte("\r\n|\r\n")
	if index := bytes.Index(data, separator); index >= 0 {
		return index + len(separator), bytes.TrimSpace(data[:index]), nil
	}
	if atEOF && len(bytes.TrimSpace(data)) > 0 {
		return len(data), bytes.TrimSpace(data), nil
	}
	return 0, nil, nil
}

func (c *Client) notify(body []byte) error {
	var messages []map[string]any
	if err := json.Unmarshal(body, &messages); err != nil {
		return err
	}
	for _, message := range messages {
		channel, _ := message["channel"].(string)
		if channel == "/__status" {
			data, _ := message["data"].(map[string]any)
			c.mu.Lock()
			for name, value := range data {
				if item := c.channels[name]; item != nil {
					item.last = number(value)
				}
			}
			c.mu.Unlock()
			continue
		}
		messageID, globalID := number(message["message_id"]), number(message["global_id"])
		data, _ := message["data"].(map[string]any)
		c.mu.Lock()
		item := c.channels[channel]
		var callbacks []Callback
		if item != nil {
			item.last = messageID
			callbacks = append(callbacks, item.callbacks...)
		}
		c.mu.Unlock()
		for _, callback := range callbacks {
			callback(data, messageID, globalID)
		}
	}
	return nil
}

func wait(ctx context.Context, duration time.Duration) bool {
	timer := time.NewTimer(duration)
	defer timer.Stop()
	select {
	case <-ctx.Done():
		return false
	case <-timer.C:
		return true
	}
}

func number(value any) int {
	switch v := value.(type) {
	case float64:
		return int(v)
	case int:
		return v
	case json.Number:
		number, _ := v.Int64()
		return int(number)
	default:
		return 0
	}
}

// netDialer is kept local so the message bus client does not share the normal
// API client's shorter response timeout.
type netDialer struct{ timeout time.Duration }

func (d *netDialer) DialContext(ctx context.Context, network, address string) (net.Conn, error) {
	return (&net.Dialer{Timeout: d.timeout, KeepAlive: 30 * time.Second}).DialContext(ctx, network, address)
}
