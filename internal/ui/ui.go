package ui

import (
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"strconv"
	"strings"
	"time"

	"github.com/merefield/termcourse/internal/discourse"
	"github.com/merefield/termcourse/internal/i18n"
	"github.com/merefield/termcourse/internal/liveupdates"
	"github.com/merefield/termcourse/internal/theme"
)

type Client interface {
	LatestTopics() (discourse.JSON, error)
	ListTopics(filter, period, username string, params url.Values) (discourse.JSON, error)
	Search(query string) (discourse.JSON, error)
	Notifications(offset, limit int, filter string) (discourse.JSON, error)
	MarkNotificationRead(id int) (discourse.JSON, error)
	NotificationTotals() (discourse.JSON, error)
	Topic(id int, nearPost int) (discourse.JSON, error)
	TopicPosts(topicID int, postIDs []int, includeRaw bool) (discourse.JSON, error)
	Post(id int) (discourse.JSON, error)
	LikePost(id int) (discourse.JSON, error)
	UnlikePost(id int) (discourse.JSON, error)
	CreatePost(topicID int, raw string, replyToPostNumber int) (discourse.JSON, error)
	CreateTopic(title, raw string, category *int) (discourse.JSON, error)
	SiteInfo() (discourse.JSON, error)
	UpdateTopicReadState(topicID, postNumber, topicTimeMS int) bool
	CurrentUser() (discourse.JSON, error)
	MessageBusHeaders() http.Header
	GetURL(pathOrURL string) (discourse.JSON, error)
	GetBytes(pathOrURL string, maxBytes int) ([]byte, error)
}

type Options struct {
	BaseURL                     string
	Username                    string
	CurrentUserID               int
	NotificationChannelPosition *int
	Theme                       theme.Theme
	Themes                      []theme.Theme
	Locale                      string
	EnableLiveUpdates           bool
	Input                       *os.File
	Output                      io.Writer
}

type UI struct {
	client     Client
	options    Options
	terminal   *Terminal
	renderer   *ScreenRenderer
	style      *Style
	locale     string
	displayURL string

	linksEnabled   bool
	emojiEnabled   bool
	tick           time.Duration
	live           *liveupdates.LiveUpdates
	lastLiveFilter string

	listCache   map[string]discourse.JSON
	topicCache  map[int]discourse.JSON
	siteCache   discourse.JSON
	users       map[int]discourse.JSON
	lastRead    map[int]int
	imageCache  map[string][]string
	kittyImages map[string]*kittyInlineImage
	kittyIDs    map[int]string
	kittyOrder  []string

	kittyChecked     bool
	kittySupported   bool
	cellSizeChecked  bool
	cellWidthPixels  int
	cellHeightPixels int

	pmUnread           int
	notificationUnread int
	lastStatusRefresh  time.Time
	resized            bool
	debug              bool
	mouseEnabled       bool
	mouseRegions       []mouseRegion
	hoveredControl     string
	activePrimary      primaryTabID
	requestedPrimary   *primaryTabID
	quitRequested      bool
	activeContext      string
	activeContextValue string
	activePeriod       string
	navigationLocked   bool
	primaryNavAllowed  bool
}

func New(client Client, options Options) *UI {
	if options.Input == nil {
		options.Input = os.Stdin
	}
	if options.Output == nil {
		options.Output = os.Stdout
	}
	if len(options.Themes) == 0 {
		options.Themes = []theme.Theme{options.Theme}
	}
	tickMS, _ := strconv.Atoi(os.Getenv("TERMCOURSE_TICK_MS"))
	if tickMS <= 0 {
		tickMS = 100
	}
	locale := i18n.ResolveLocale(options.Locale)
	terminal := NewTerminal(options.Input, options.Output)
	result := &UI{
		client: client, options: options, terminal: terminal,
		renderer: NewScreenRenderer(terminal), style: NewStyle(options.Theme, options.Output), locale: locale,
		displayURL:   strings.TrimPrefix(strings.TrimPrefix(options.BaseURL, "https://"), "http://"),
		linksEnabled: os.Getenv("TERMCOURSE_LINKS") != "0", emojiEnabled: os.Getenv("TERMCOURSE_EMOJI") != "0",
		tick: time.Duration(tickMS) * time.Millisecond, listCache: map[string]discourse.JSON{},
		topicCache: map[int]discourse.JSON{}, users: map[int]discourse.JSON{}, lastRead: map[int]int{},
		imageCache:  map[string][]string{},
		kittyImages: map[string]*kittyInlineImage{}, kittyIDs: map[int]string{},
		debug:        os.Getenv("TERMCOURSE_DEBUG") == "1",
		mouseEnabled: os.Getenv("TERMCOURSE_MOUSE") != "0",
	}
	terminal.SetMouseEnabled(result.mouseEnabled)
	if options.EnableLiveUpdates {
		result.live = liveupdates.New(options.BaseURL, client.MessageBusHeaders(), liveupdates.Options{
			CurrentUserID: options.CurrentUserID, NotificationChannelPosition: options.NotificationChannelPosition,
			Debug: result.debugLog,
		})
	}
	return result
}

func (u *UI) cycleTheme() {
	if len(u.options.Themes) < 2 {
		return
	}
	current := u.style.Theme.Name
	index := 0
	for candidate, value := range u.options.Themes {
		if value.Name == current {
			index = candidate
			break
		}
	}
	next := u.options.Themes[(index+1)%len(u.options.Themes)]
	u.options.Theme = next
	u.style = NewStyle(next, u.options.Output)
	if u.renderer != nil {
		u.renderer.Reset()
	}
}

func (u *UI) t(key string, pairs ...any) string { return i18n.Tr(u.locale, key, pairs...) }

func (u *UI) Run() error {
	if err := u.terminal.EnterRaw(); err != nil {
		return fmt.Errorf("raw terminal mode: %w", err)
	}
	defer func() {
		if u.live != nil {
			u.live.Stop()
		}
		u.clearKittyImages()
		u.terminal.Restore()
		u.renderer.Clear()
	}()
	if u.live != nil {
		_ = u.live.Start()
	}
	u.refreshStatusCounts(true)

	filter, period := "latest", "monthly"
	active := primaryTopics
	for {
		active = u.takeRequestedPrimary(active)
		if u.quitRequested {
			return nil
		}
		switch active {
		case primarySearch:
			if query := u.promptSingleLine("ui.search.title", "ui.search.prompt", "Search: ", primarySearch, "search", "query"); query != "" {
				u.searchFlow(query)
			}
			if u.quitRequested {
				return nil
			}
			active = u.takeRequestedPrimary(primaryTopics)
			continue
		case primaryNotifications:
			if u.notificationsLoop() {
				return nil
			}
			active = u.takeRequestedPrimary(primaryTopics)
			continue
		case primaryCompose:
			u.createNewTopic(u.newTopicFlow())
			active = u.takeRequestedPrimary(primaryTopics)
			continue
		}
		u.trackLive(filter)
		data, err := u.loadList(filter, period, false)
		if err != nil {
			if !u.showError(err) {
				return nil
			}
			continue
		}
		result := u.topicListLoop(data, filter, period)
		switch result.kind {
		case "quit":
			return nil
		case "filter":
			filter = result.text
		case "period":
			period = result.text
		case "reload":
			delete(u.listCache, cacheKey(filter, period))
		case "topic":
			if quit, _ := u.topicLoop(result.id, result.post, ""); quit {
				return nil
			}
		case "navigate":
			active = u.takeRequestedPrimary(primaryTopics)
		}
	}
}

func (u *UI) createNewTopic(data discourse.JSON) {
	if data == nil {
		return
	}
	_, err := u.client.CreateTopic(discourse.String(data["title"]), discourse.String(data["raw"]), optionalInt(data["category"]))
	if err != nil {
		u.showError(err)
		return
	}
	u.listCache = map[string]discourse.JSON{}
}

type loopResult struct {
	kind string
	id   int
	post int
	text string
}

func topicListResult(topic discourse.JSON) loopResult {
	lastRead := discourse.Int(topic["last_read_post_number"])
	target := lastRead
	if lastRead > 0 && discourse.Int(topic["highest_post_number"]) > lastRead {
		target++
	}
	return loopResult{kind: "topic", id: discourse.Int(topic["id"]), post: target}
}

func (u *UI) topicListLoop(data discourse.JSON, filter, period string) loopResult {
	selected, loading := 0, false
	filters := []string{"latest", "unread", "private", "hot", "new", "top"}
	periods := []string{"daily", "weekly", "monthly", "quarterly", "yearly"}
	for {
		if u.requestedPrimary != nil {
			return loopResult{kind: "navigate"}
		}
		u.refreshStatusCounts(false)
		if u.live != nil && u.live.ConsumeTopicListRefreshRequest() {
			return loopResult{kind: "reload"}
		}
		topics := topicList(data)
		nextURL := discourse.String(discourse.Map(data["topic_list"])["more_topics_url"])
		u.renderTopicList(topics, selected, filter, period, loading)
		key, err := u.readKey(u.tick)
		if err != nil {
			return loopResult{kind: "quit"}
		}
		if tab, ok := parsePrimaryTabKey(key); ok {
			if tab != primaryTopics {
				u.requestPrimary(tab)
				return loopResult{kind: "navigate"}
			}
			continue
		}
		if context, ok := parseContextTabKey(key); ok {
			if context == "period" && filter == "top" {
				return loopResult{kind: "period", text: periods[(indexOf(periods, period)+1)%len(periods)]}
			}
			if indexOf(filters, context) >= 0 && context != filter {
				return loopResult{kind: "filter", text: context}
			}
			continue
		}
		if row, ok := parseRowKey(key); ok {
			if row >= 0 && row < len(topics) {
				if row == selected {
					return topicListResult(topics[row])
				}
				selected = row
			}
			continue
		}
		switch key {
		case keyTick:
			continue
		case "up", "wheelup":
			step := 1
			if key == "wheelup" {
				step = 3
			}
			selected = max(selected-step, 0)
		case "down", "wheeldown":
			step := 1
			if key == "wheeldown" {
				step = 3
			}
			selected = min(selected+step, max(len(topics)-1, 0))
			if nextURL != "" && selected >= len(topics)-3 && !loading {
				loading = true
				u.renderTopicList(topics, selected, filter, period, loading)
				if more, fetchErr := u.client.GetURL(nextURL); fetchErr == nil {
					mergeTopicLists(data, more, false)
					u.mergeUsers(more)
				}
				loading = false
			}
		case "enter":
			if selected < len(topics) {
				return topicListResult(topics[selected])
			}
		case "f":
			return loopResult{kind: "filter", text: filters[(indexOf(filters, filter)+1)%len(filters)]}
		case "p":
			if filter == "top" {
				return loopResult{kind: "period", text: periods[(indexOf(periods, period)+1)%len(periods)]}
			}
		case "s":
			u.requestPrimary(primarySearch)
			return loopResult{kind: "navigate"}
		case "c":
			u.requestPrimary(primaryCompose)
			return loopResult{kind: "navigate"}
		case "n":
			u.requestPrimary(primaryNotifications)
			return loopResult{kind: "navigate"}
		case "g":
			return loopResult{kind: "reload"}
		case "q", "esc":
			return loopResult{kind: "quit"}
		default:
			if len(key) == 1 && key[0] >= '0' && key[0] <= '9' {
				index := int(key[0] - '1')
				if key == "0" {
					index = 9
				}
				if index >= 0 && index < len(topics) {
					return topicListResult(topics[index])
				}
			}
		}
	}
}

func (u *UI) renderTopicList(topics []discourse.JSON, selected int, filter, period string, loading bool) {
	width, height := u.terminal.Size()
	controls := u.t("ui.controls.topic_list")
	header := u.navigationHeader("", primaryTopics, "topics", filter, period, nil, width, height)
	screen := make([]string, height)
	copy(screen, header)
	boxStart := min(len(header)+1, height)
	boxHeight := max(height-boxStart-1, 0)
	innerRows := max(boxHeight-2, 0)
	innerWidth, _ := frameInnerWidth(width)
	boxLines := make([]string, innerRows)
	if len(topics) == 0 {
		if len(boxLines) > 0 {
			boxLines[0] = u.t("ui.empty.topics")
		}
		copy(screen[boxStart:], u.style.Box(boxLines, width))
		u.placeControlsFooter(screen, controls, width)
		u.renderer.Render(screen, width, height, "topic-list-"+filter+"-"+period, -1, -1, false)
		return
	}
	mode := "compact"
	if width >= 149 {
		mode = "stats"
	} else if width >= 125 {
		mode = "category"
	}
	contentRow := 0
	if loading && contentRow < len(boxLines) {
		boxLines[contentRow] = u.style.Text(u.t("ui.status.loading_more"), roleListMeta)
		contentRow++
	}
	if mode != "compact" && contentRow < len(boxLines) {
		boxLines[contentRow] = u.topicTableHeader(innerWidth, filter, mode)
		contentRow++
	}
	rows := max(len(boxLines)-contentRow, 0)
	if rows == 0 {
		copy(screen[boxStart:], u.style.Box(boxLines, width))
		u.placeControlsFooter(screen, controls, width)
		u.renderer.Render(screen, width, height, "topic-list-"+filter+"-"+period+"-"+mode, -1, -1, false)
		return
	}
	start := max(selected-rows/2, 0)
	end := min(start+rows, len(topics))
	for index := start; index < end; index++ {
		line := u.topicRow(topics[index], index+1, innerWidth, filter, mode)
		if index == selected {
			line = u.style.Selected(line)
		}
		boxLines[contentRow+index-start] = line
		y := boxStart + 1 + contentRow + index - start
		u.addMouseRegion(0, y, width, 1, rowKey(index))
	}
	copy(screen[boxStart:], u.style.Box(boxLines, width))
	u.placeControlsFooter(screen, controls, width)
	u.renderer.Render(screen, width, height, "topic-list-"+filter+"-"+period+"-"+mode, -1, -1, false)
}

func (u *UI) topicTableHeader(width int, filter, mode string) string {
	if filter == "private" {
		return u.style.Text(tableRow([]cell{{"#", 4, true}, {u.t("ui.topic_list.columns.title"), max(width-40, 20), false}, {u.t("ui.topic_list.columns.users"), 22, false}, {u.t("ui.topic_list.columns.replies"), 8, true}}), roleListMeta)
	}
	categoryWidth := 0
	if mode != "compact" {
		categoryWidth = 22
	}
	viewsWidth := 0
	separatorWidth := 6
	if mode == "stats" {
		viewsWidth = 8
		separatorWidth = 8
	}
	titleWidth := max(width-4-categoryWidth-viewsWidth-8-separatorWidth, 20)
	return u.style.Text(tableRow([]cell{{"#", 4, true}, {u.t("ui.topic_list.columns.title"), titleWidth, false}, {u.t("ui.topic_list.columns.category"), categoryWidth, false}, {u.t("ui.topic_list.columns.replies"), 8, true}, {u.t("ui.topic_list.columns.views"), viewsWidth, true}}), roleListMeta)
}

func (u *UI) topicRow(topic discourse.JSON, number, width int, filter, mode string) string {
	title := emojify(discourse.String(topic["title"]), u.emojiEnabled)
	replies := max(discourse.Int(topic["posts_count"])-1, 0)
	badge := ""
	if discourse.Bool(topic["unseen"]) || discourse.Int(topic["unread_posts"]) > 0 {
		badge = " *"
	}
	if mode == "compact" {
		meta := strconv.Itoa(replies)
		if filter == "private" {
			meta = u.pmUsers(topic)
		}
		prefix := fmt.Sprintf("%3d ", number)
		suffix := " [" + meta + "]" + badge
		title = truncate(title, max(width-displayWidth(prefix)-displayWidth(suffix), 1))
		return u.style.Text(prefix, roleListNumber) + u.style.Text(title, roleListText) + u.style.Text(suffix, roleListMeta)
	}
	if filter == "private" {
		return u.topicTableRow([]cell{{strconv.Itoa(number), 4, true}, {title + badge, max(width-40, 20), false}, {u.pmUsers(topic), 22, false}, {strconv.Itoa(replies), 8, true}})
	}
	category := discourse.String(topic["category_name"])
	if category == "" {
		category = discourse.String(topic["category_slug"])
	}
	categoryWidth, viewsWidth := 22, 0
	separatorWidth := 6
	if mode == "stats" {
		viewsWidth = 8
		separatorWidth = 8
	}
	titleWidth := max(width-4-categoryWidth-viewsWidth-8-separatorWidth, 20)
	return u.topicTableRow([]cell{{strconv.Itoa(number), 4, true}, {title + badge, titleWidth, false}, {category, categoryWidth, false}, {strconv.Itoa(replies), 8, true}, {formatCount(topic["views"]), viewsWidth, true}})
}

func (u *UI) topicTableRow(cells []cell) string {
	values := make([]string, 0, len(cells))
	visibleIndex := 0
	for _, item := range cells {
		if item.width <= 0 {
			continue
		}
		role := roleListMeta
		switch visibleIndex {
		case 0:
			role = roleListNumber
		case 1:
			role = roleListText
		}
		values = append(values, u.style.Text(fitCell(item.text, item.width, item.right), role))
		visibleIndex++
	}
	return strings.Join(values, u.style.Text("  ", roleSeparator))
}

type cell struct {
	text  string
	width int
	right bool
}

func tableRow(cells []cell) string {
	var values []string
	for _, item := range cells {
		if item.width > 0 {
			values = append(values, fitCell(item.text, item.width, item.right))
		}
	}
	return strings.Join(values, "  ")
}

func (u *UI) pmUsers(topic discourse.JSON) string {
	var names []string
	login := strings.ToLower(strings.TrimSpace(u.options.Username))
	for _, key := range []string{"participants", "allowed_users"} {
		for _, raw := range discourse.Slice(topic[key]) {
			name := strings.ToLower(strings.TrimSpace(discourse.String(discourse.Map(raw)["username"])))
			if name != "" && name != login {
				names = appendUnique(names, name)
			}
		}
		if len(names) > 0 {
			break
		}
	}
	ids := discourse.Slice(topic["participant_ids"])
	if len(names) == 0 {
		for _, raw := range ids {
			id := discourse.Int(raw)
			if user := u.users[id]; user != nil {
				name := strings.ToLower(strings.TrimSpace(discourse.String(user["username"])))
				if name != "" && name != login {
					names = appendUnique(names, name)
				}
			}
		}
	}
	if len(names) == 0 {
		for _, raw := range discourse.Slice(topic["posters"]) {
			id := discourse.Int(discourse.Map(raw)["user_id"])
			if user := u.users[id]; user != nil {
				name := strings.ToLower(strings.TrimSpace(discourse.String(user["username"])))
				if name != "" && name != login {
					names = appendUnique(names, name)
				}
			}
		}
	}
	if len(names) == 0 {
		return u.t("ui.topic_list.pm_users.none")
	}
	if len(names) > 3 {
		return u.t("ui.topic_list.pm_users.count", "count", len(names))
	}
	return strings.Join(names, ", ")
}

func appendUnique(values []string, value string) []string {
	for _, existing := range values {
		if existing == value {
			return values
		}
	}
	return append(values, value)
}

func optionalInt(value any) *int {
	if value == nil {
		return nil
	}
	number := discourse.Int(value)
	return &number
}

func indexOf(values []string, target string) int {
	for index, value := range values {
		if value == target {
			return index
		}
	}
	return 0
}
