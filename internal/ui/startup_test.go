package ui

import (
	"bytes"
	"errors"
	"net/url"
	"reflect"
	"strings"
	"testing"
	"time"

	tea "charm.land/bubbletea/v2"
	"charm.land/lipgloss/v2"
	"github.com/charmbracelet/x/ansi"
	"github.com/merefield/termcourse/internal/discourse"
)

func startupPendingModel(t *testing.T) terminalModel {
	t.Helper()
	t.Setenv("TERMCOURSE_COLOR_MODE", "truecolor")
	return terminalModel{
		state: &terminalState{inputs: make(chan terminalInput, 16), ready: make(chan struct{}), done: make(chan error, 1)},
		style: NewStyle(testTheme(), &bytes.Buffer{}), startup: startupState{pending: true}, content: "ready",
	}
}

func startupTestModel(t *testing.T, width, height int) terminalModel {
	t.Helper()
	m := startupPendingModel(t)
	next, cmd := m.Update(tea.WindowSizeMsg{Width: width, Height: height})
	m = next.(terminalModel)
	if !m.startup.active() || cmd == nil {
		t.Fatal("startup did not start after terminal size arrived")
	}
	m.startup = newStartupState(m.startup.started, startupSlide, 42)
	return m
}

func startupKey(code rune) tea.KeyPressMsg {
	return tea.KeyPressMsg(tea.Key{Code: code, Text: string(code)})
}
func startupSpecialKey(code rune) tea.KeyPressMsg { return tea.KeyPressMsg(tea.Key{Code: code}) }
func startupModifiedKey(code rune, mod tea.KeyMod) tea.KeyPressMsg {
	return tea.KeyPressMsg(tea.Key{Code: code, Mod: mod})
}

func TestStartupLogoReturnsToOriginalHeader(t *testing.T) {
	if got := scaledStartupLogo(len([]rune(headerLogo[0])), len(headerLogo)*2); !reflect.DeepEqual(got, headerLogo) {
		t.Fatalf("scaled logo changed header lettering: %#v", got)
	}
	for _, size := range [][2]int{{68, 8}, {80, 24}, {120, 40}, {240, 60}, {320, 8}} {
		m := startupTestModel(t, size[0], size[1])
		previousX := m.width
		for elapsed := time.Duration(0); elapsed <= startupEntrance; elapsed += 30 * time.Millisecond {
			m.startup.elapsed = elapsed
			g := m.startupGeometry()
			if g.x > previousX || g.width != m.width {
				t.Fatal("entrance did not move left at full width", g)
			}
			previousX = g.x
		}
		m.startup.elapsed = startupEntrance + startupHold/2
		g := m.startupGeometry()
		if g.x != 0 || g.width != m.width || g.y != (m.height-(g.pixelHeight+1)/2)/2 {
			t.Fatal("logo did not settle across the full centred screen", g)
		}
		previousWidth, previousY := g.width, g.y
		for elapsed := startupEntrance + startupHold; elapsed <= startupDuration; elapsed += 20 * time.Millisecond {
			m.startup.elapsed = elapsed
			g := m.startupGeometry()
			if g.width > previousWidth || g.y > previousY {
				t.Fatal("docking did not shrink toward the header", g)
			}
			previousWidth, previousY = g.width, g.y
			lines := strings.Split(ansi.Strip(m.View().Content), "\n")
			if len(lines) != m.height {
				t.Fatalf("frame height=%d want %d", len(lines), m.height)
			}
			for _, line := range lines {
				if lipgloss.Width(line) != m.width {
					t.Fatal("animation frame wrapped or left stale cells")
				}
			}
		}
		m.startup.elapsed = startupDuration
		lines := strings.Split(ansi.Strip(m.renderStartup()), "\n")
		for row, logo := range headerLogo {
			if got := lines[row]; got != logo+strings.Repeat(" ", m.width-lipgloss.Width(logo)) {
				t.Fatal("logo did not dock at normal header coordinates", got)
			}
		}
	}
}

func TestStartupSkipDoesNotActivateHiddenControls(t *testing.T) {
	for _, message := range []tea.Msg{startupKey('t'), tea.PasteMsg{Content: "do not send"}, startupSpecialKey(tea.KeyEnter), startupSpecialKey(tea.KeyEscape), tea.MouseClickMsg{X: 3, Y: 1, Button: tea.MouseLeft}} {
		m := startupTestModel(t, 120, 40)
		started := m.startup.started
		next, cmd := m.Update(message)
		m = next.(terminalModel)
		if m.startup.active() || cmd != nil || len(m.state.inputs) != 0 {
			t.Fatal("skip activated an unseen control")
		}
		next, cmd = m.Update(startupFrameMsg{started: started, at: started.Add(time.Second)})
		if next.(terminalModel).startup.active() || cmd != nil {
			t.Fatal("queued frame resurrected skipped animation")
		}
	}
	for _, message := range []tea.Msg{startupKey('q'), startupModifiedKey('c', tea.ModCtrl)} {
		m := startupTestModel(t, 120, 40)
		next, cmd := m.Update(message)
		if next.(terminalModel).startup.active() || cmd == nil {
			t.Fatal("quit blocked by startup")
		}
		if _, ok := cmd().(tea.QuitMsg); !ok {
			t.Fatal("startup quit did not stop program")
		}
	}
	m := startupTestModel(t, 120, 40)
	next, cmd := m.Update(tea.MouseMotionMsg{X: 3, Y: 1})
	if !next.(terminalModel).startup.active() || cmd != nil {
		t.Fatal("hover skipped startup or reached a hidden control")
	}
}

func TestStartupResizeAndDelayedFrames(t *testing.T) {
	m := startupTestModel(t, 80, 24)
	started := m.startup.started
	next, cmd := m.Update(startupFrameMsg{started: started, at: started.Add(500 * time.Millisecond)})
	m = next.(terminalModel)
	if cmd == nil || m.startup.elapsed != 500*time.Millisecond {
		t.Fatal("delayed frame did not use elapsed time")
	}
	next, cmd = m.Update(tea.WindowSizeMsg{Width: 160, Height: 40})
	m = next.(terminalModel)
	if cmd != nil || m.startup.started != started || m.startup.elapsed != 500*time.Millisecond || m.startupGeometry().width != 160 {
		t.Fatal("resize restarted timing or failed to rescale")
	}
	next, cmd = m.Update(startupFrameMsg{started: started.Add(time.Second), at: started.Add(time.Second)})
	if cmd != nil || next.(terminalModel).startup.elapsed != m.startup.elapsed {
		t.Fatal("stale animation changed progress")
	}
	next, _ = m.Update(startupFrameMsg{started: started, at: started.Add(200 * time.Millisecond)})
	m = next.(terminalModel)
	if m.startup.elapsed != 500*time.Millisecond {
		t.Fatal("out-of-order frame moved animation backwards")
	}
	next, cmd = m.Update(tea.WindowSizeMsg{Width: 50, Height: 10})
	if next.(terminalModel).startup.active() || cmd != nil {
		t.Fatal("resize to a compact terminal did not finish animation")
	}
}

func TestStartupCompactFallback(t *testing.T) {
	for _, size := range [][2]int{{40, 24}, {80, 7}, {1, 1}} {
		m := startupPendingModel(t)
		next, cmd := m.Update(tea.WindowSizeMsg{Width: size[0], Height: size[1]})
		m = next.(terminalModel)
		if m.startup.active() || m.startup.pending || cmd != nil || m.View().Content == "" {
			t.Fatal("compact startup did not show the normal UI")
		}
	}
	m := startupPendingModel(t)
	next, cmd := m.Update(tea.WindowSizeMsg{})
	if !next.(terminalModel).startup.pending || cmd != nil {
		t.Fatal("animation started without terminal dimensions")
	}
}

func TestStartupTypingThreeBlinksAndMovingDot(t *testing.T) {
	s := newStartupState(time.Now(), startupTyping, 42)
	for blink := 0; blink < 3; blink++ {
		for _, on := range []bool{true, false} {
			s.elapsed = time.Duration(blink*2) * startupBlinkHalf
			if !on {
				s.elapsed += startupBlinkHalf
			}
			text, cursor, dot := s.lettering()
			if text != strings.Repeat(" ", len(startupTitle)) || cursor != 0 || dot != on {
				t.Fatal("expected three blank-word cursor blinks before typing", blink, on, text, cursor, dot)
			}
			count := 0
			for _, row := range s.bitmap() {
				for _, pixel := range row {
					if pixel {
						count++
					}
				}
			}
			if on && count != 1 || !on && count != 0 {
				t.Fatal("blink drew something other than the dot")
			}
		}
	}
	for typed := 1; typed <= len(startupTitle); typed++ {
		s.elapsed = 6*startupBlinkHalf + time.Duration(typed-1)*startupTypingStep
		text, cursor, dot := s.lettering()
		if text != startupTitle[:typed]+strings.Repeat(" ", len(startupTitle)-typed) || cursor != typed || dot != (typed < len(startupTitle)) {
			t.Fatal("typing did not reveal the next character and move its cursor", typed, text, cursor, dot)
		}
		pixels := s.bitmap()
		for position := 0; position < typed; position++ {
			slot := startupLetterSlots[position]
			for row := range pixels {
				for col := range slot.width {
					if pixels[row][slot.start+col] != startupLogoBitmap[row][slot.start+col] {
						t.Fatal("typing changed the original letter shape")
					}
				}
			}
		}
		if dot {
			slot := startupLetterSlots[cursor]
			if !pixels[len(pixels)-1][slot.start+slot.width/2] {
				t.Fatal("dot was not in the next letter's position")
			}
		}
	}
	if !reflect.DeepEqual(s.bitmap(), startupLogoBitmap) {
		t.Fatal("typing did not finish with the exact original logo")
	}
}

func TestStartupShuffleLocksCharactersInRandomOrder(t *testing.T) {
	s := newStartupState(time.Now(), startupShuffle, 42)
	initial, _, _ := s.lettering()
	if len(initial) != len(startupTitle) {
		t.Fatal("shuffle changed the character count")
	}
	for index, character := range initial {
		if !strings.ContainsRune(startupAlphabet, character) || byte(character) == startupTitle[index] {
			t.Fatal("initial shuffle did not begin with random unresolved characters")
		}
	}
	s.elapsed = startupShuffleFrame
	changed, _, _ := s.lettering()
	if changed == initial {
		t.Fatal("unresolved characters did not shuffle")
	}
	s.elapsed = 0
	same, _, _ := s.lettering()
	if same != initial {
		t.Fatal("a redraw changed the random frame")
	}
	previous := make(map[int]bool)
	var sequence []int
	for count := 1; count <= len(startupTitle); count++ {
		s.elapsed = time.Duration(count) * startupShuffleStep
		text, cursor, dot := s.lettering()
		if len(text) != len(startupTitle) || cursor != -1 || dot {
			t.Fatal("shuffle added a typing cursor or changed character count")
		}
		correct := 0
		for index := range text {
			if previous[index] && text[index] != startupTitle[index] {
				t.Fatal("resolved character became random again")
			}
			if text[index] == startupTitle[index] {
				correct++
				if !previous[index] {
					sequence = append(sequence, index)
				}
				previous[index] = true
			}
		}
		if correct != count {
			t.Fatalf("resolved %d characters, want %d", correct, count)
		}
	}
	if reflect.DeepEqual(sequence, []int{0, 1, 2, 3, 4, 5, 6, 7, 8, 9}) {
		t.Fatal("shuffle resolved from left to right instead of the seeded random order")
	}
	if !reflect.DeepEqual(s.bitmap(), startupLogoBitmap) {
		t.Fatal("shuffle did not end with the original logo")
	}
	other := newStartupState(s.started, startupShuffle, 24)
	if reflect.DeepEqual(s.order, other.order) {
		t.Fatal("shuffle order did not vary with launch seed")
	}
	for _, character := range startupAlphabet {
		glyph, exists := startupRandomGlyphs[byte(character)]
		if !exists {
			t.Fatalf("missing random glyph %c", character)
		}
		for _, row := range glyph {
			if len(row) != 3 {
				t.Fatal("random glyph did not fit the shared font")
			}
		}
	}
}

func TestStartupVariantsShareFullSizeAndDocking(t *testing.T) {
	for _, size := range [][2]int{{68, 8}, {80, 24}, {120, 40}, {240, 60}, {320, 8}} {
		reference := startupTestModel(t, size[0], size[1])
		for _, animation := range []startupAnimation{startupSlide, startupTyping, startupShuffle} {
			m := reference
			m.startup = newStartupState(m.startup.started, animation, 42)
			for _, dockProgress := range []time.Duration{0, startupDock / 4, startupDock / 2, startupDock} {
				m.startup.elapsed = m.startup.entranceDuration() + startupHold + dockProgress
				reference.startup.elapsed = reference.startup.entranceDuration() + startupHold + dockProgress
				if m.startupGeometry() != reference.startupGeometry() || m.renderStartup() != reference.renderStartup() {
					t.Fatalf("variant %d changed full-size lettering or shared docking", animation)
				}
			}
			for elapsed := time.Duration(0); elapsed < m.startup.duration(); elapsed += 70 * time.Millisecond {
				m.startup.elapsed = elapsed
				frame := strings.Split(ansi.Strip(m.View().Content), "\n")
				if len(frame) != m.height {
					t.Fatal("variant overflowed terminal height")
				}
				for _, line := range frame {
					if lipgloss.Width(line) != m.width {
						t.Fatal("variant overflowed terminal width")
					}
				}
			}
			next, cmd := m.Update(startupFrameMsg{started: m.startup.started, at: m.startup.started.Add(m.startup.duration())})
			if next.(terminalModel).startup.active() || cmd != nil {
				t.Fatal("variant did not finish at its own duration")
			}
			m.startup = newStartupState(reference.startup.started, animation, 42)
			next, cmd = m.Update(startupKey('t'))
			if next.(terminalModel).startup.active() || cmd != nil || len(next.(terminalModel).state.inputs) != 0 {
				t.Fatal("variant skip triggered a hidden control")
			}
		}
	}
}

func TestStartupRandomChoiceSurvivesRedrawAndResize(t *testing.T) {
	m := startupPendingModel(t)
	next, cmd := m.Update(tea.WindowSizeMsg{Width: 120, Height: 40})
	m = next.(terminalModel)
	if cmd == nil || m.startup.animation < startupSlide || m.startup.animation >= startupAnimationCount {
		t.Fatal("startup did not choose one of the three animations")
	}
	selected := m.startup
	before := m.renderStartup()
	if m.renderStartup() != before {
		t.Fatal("redraw changed the chosen animation or its random characters")
	}
	next, cmd = m.Update(tea.WindowSizeMsg{Width: 160, Height: 50})
	m = next.(terminalModel)
	if cmd != nil || m.startup != selected {
		t.Fatal("resize reselected the animation or random seed")
	}
	next, _ = m.Update(tea.WindowSizeMsg{Width: 120, Height: 40})
	if next.(terminalModel).renderStartup() != before {
		t.Fatal("resize changed the chosen animation's current frame")
	}
}

func TestStartupAcceptsLoadedFramesAndQueriesWithoutShowingControls(t *testing.T) {
	m := startupTestModel(t, 120, 40)
	progress := tea.NewProgressBar(tea.ProgressBarDefault, 50)
	next, _ := m.Update(terminalRenderMsg{content: "loaded VERSION", cursorX: 2, cursorY: 3, progress: progress})
	m = next.(terminalModel)
	if m.content != "loaded VERSION" || !m.startup.active() || strings.Contains(ansi.Strip(m.View().Content), "VERSION") || m.View().Cursor != nil || m.View().ProgressBar != nil {
		t.Fatal("loaded screen appeared before docking or stopped animation")
	}
	response := make(chan any, 1)
	next, cmd := m.Update(terminalQueryMsg{sequence: "query", accept: func(msg any) bool { return msg == "answer" }, response: response})
	m = next.(terminalModel)
	if cmd == nil {
		t.Fatal("terminal query blocked by startup")
	}
	next, _ = m.Update("answer")
	m = next.(terminalModel)
	if <-response != "answer" || !m.startup.active() {
		t.Fatal("terminal query response blocked by startup")
	}
	next, cmd = m.Update(startupFrameMsg{started: m.startup.started, at: m.startup.started.Add(m.startup.duration())})
	m = next.(terminalModel)
	if cmd != nil || m.startup.active() || m.startup.pending || m.View().Content != "loaded VERSION" || m.View().Cursor == nil || m.View().ProgressBar != progress {
		t.Fatal("completion did not reveal the latest screen and restore cursor/progress")
	}
	next, cmd = m.Update(tea.WindowSizeMsg{Width: 160, Height: 50})
	if cmd != nil || next.(terminalModel).startup.active() {
		t.Fatal("resize replayed startup")
	}
}

type startupClient struct {
	Client
	topics, totals, private, site discourse.JSON
	topicsErr, totalsErr          error
	loading, release, finished    chan struct{}
}

func (c *startupClient) NotificationTotals() (discourse.JSON, error) { return c.totals, c.totalsErr }
func (c *startupClient) ListTopics(_, _, _ string, _ url.Values) (discourse.JSON, error) {
	if c.loading != nil {
		close(c.loading)
		<-c.release
		defer close(c.finished)
	}
	return c.topics, c.topicsErr
}
func (c *startupClient) SiteInfo() (discourse.JSON, error)     { return c.site, nil }
func (c *startupClient) GetURL(string) (discourse.JSON, error) { return c.private, nil }

func TestInitialForumDataAppliesOnUILoop(t *testing.T) {
	topics := discourse.JSON{"topic_list": discourse.JSON{"topics": []any{discourse.JSON{"id": 42}}}, "users": []any{discourse.JSON{"id": 7, "username": "member"}}}
	for _, fallback := range []bool{false, true} {
		client := &startupClient{topics: topics, totals: discourse.JSON{"unread_notifications": 3, "unread_personal_messages": 2}, site: discourse.JSON{"categories": []any{}}}
		if fallback {
			client.totalsErr = errors.New("not supported")
			client.private = discourse.JSON{"topic_list": discourse.JSON{"topics": []any{discourse.JSON{"id": 3, "unread": true}}}}
		}
		u := New(client, Options{Theme: testTheme(), Output: &bytes.Buffer{}})
		initial, err := u.loadInitialData()
		if err != nil || initial.topicsErr != nil || u.listCache[cacheKey("latest", "monthly")] == nil || u.users[7] == nil || u.siteCache == nil || u.lastStatusRefresh.IsZero() {
			t.Fatal("initial forum data was not applied", err)
		}
		if !fallback && (u.notificationUnread != 3 || u.pmUnread != 2) {
			t.Fatal("status counts changed")
		}
		if fallback && (u.notificationUnread != 0 || u.pmUnread != 1) {
			t.Fatal("private-topic fallback changed")
		}
	}
	failure := errors.New("offline")
	u := New(&startupClient{topicsErr: failure}, Options{Theme: testTheme(), Output: &bytes.Buffer{}})
	initial, err := u.loadInitialData()
	if err != nil || !errors.Is(initial.topicsErr, failure) {
		t.Fatal("topic failure was lost")
	}
}

func TestInitialForumLoadingCanQuitBeforeRequestReturns(t *testing.T) {
	for _, terminalQuit := range []bool{false, true} {
		client := &startupClient{loading: make(chan struct{}), release: make(chan struct{}), finished: make(chan struct{})}
		u := New(client, Options{Theme: testTheme(), Output: &bytes.Buffer{}})
		result := make(chan error, 1)
		go func() { _, err := u.loadInitialData(); result <- err }()
		<-client.loading
		if terminalQuit {
			u.terminal.state.done <- nil
		} else {
			u.terminal.state.inputs <- terminalInput{msg: startupKey('t')}
			u.terminal.state.inputs <- terminalInput{msg: startupKey('q')}
		}
		select {
		case err := <-result:
			if !isEOF(err) {
				t.Fatal("startup quit did not finish UI", err)
			}
		case <-time.After(time.Second):
			close(client.release)
			t.Fatal("startup quit waited for forum response")
		}
		close(client.release)
		<-client.finished
		if len(u.listCache) != 0 || !u.lastStatusRefresh.IsZero() || u.style.Theme.Name != "test" {
			t.Fatal("cancelled loading changed UI state or activated hidden control")
		}
	}
}
