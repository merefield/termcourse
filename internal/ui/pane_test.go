package ui

import (
	"bytes"
	"errors"
	"fmt"
	"io"
	"strings"
	"testing"
	"time"

	"charm.land/bubbles/v2/textarea"
	"charm.land/bubbles/v2/textinput"
	tea "charm.land/bubbletea/v2"
	"charm.land/lipgloss/v2"
	"github.com/charmbracelet/x/ansi/kitty"
	"github.com/merefield/termcourse/internal/discourse"
	"github.com/merefield/termcourse/internal/theme"
)

// Capture the complete frame sent to Bubble Tea, exercising the real view
// functions without needing an interactive terminal or a network connection.
func capturePaneFrames(t *testing.T, u *UI) func() string {
	t.Helper()
	frames := make(chan string, 8)
	program := tea.NewProgram(terminalModel{state: u.terminal.state},
		tea.WithInput(nil), tea.WithOutput(io.Discard), tea.WithoutRenderer(), tea.WithoutSignalHandler(),
		tea.WithFilter(func(_ tea.Model, msg tea.Msg) tea.Msg {
			// Non-interactive Bubble Tea supplies an 80x24 startup size. The
			// test sets each viewport explicitly, so ignore that startup report.
			if _, ok := msg.(tea.WindowSizeMsg); ok {
				return nil
			}
			if render, ok := msg.(terminalRenderMsg); ok {
				frames <- render.content
			}
			return msg
		}),
	)
	u.terminal.program = program
	done := make(chan error, 1)
	go func() { _, err := program.Run(); done <- err }()
	t.Cleanup(func() {
		program.Quit()
		program.Wait()
		if err := <-done; err != nil {
			t.Error(err)
		}
	})
	return func() string {
		t.Helper()
		select {
		case frame := <-frames:
			return frame
		case <-time.After(5 * time.Second):
			t.Fatal("view did not send a terminal frame")
			return ""
		}
	}
}

func paneBoxRows(frame string) []int {
	var rows []int
	for row, line := range strings.Split(stripANSI(frame), "\n") {
		if strings.HasPrefix(line, "╭") && strings.HasSuffix(line, "╮") {
			rows = append(rows, row)
		}
	}
	return rows
}

func TestContentViewsUseMainPanesAndKeepRowTargetsInsideBorders(t *testing.T) {
	t.Setenv("TERMCOURSE_COLOR_MODE", "truecolor")
	t.Setenv("TERMCOURSE_IMAGES", "0")
	for _, size := range [][2]int{{1, 2}, {2, 3}, {8, 6}, {40, 16}, {88, 24}, {120, 32}} {
		t.Run(fmt.Sprintf("%dx%d", size[0], size[1]), func(t *testing.T) {
			width, height := size[0], size[1]
			value := testTheme()
			value.Background = "#101820"
			u := New(nil, Options{Theme: value, Locale: "en", Output: &bytes.Buffer{}})
			u.terminal.state.width.Store(int32(width))
			u.terminal.state.height.Store(int32(height))
			u.siteCache = discourse.JSON{}
			nextFrame := capturePaneFrames(t, u)
			area := textarea.New()
			area.ShowLineNumbers = false
			area.SetValue("A draft with enough text to wrap inside its pane.")
			input := textinput.New()
			input.Prompt = "Search: "
			input.SetValue("a query")
			cases := []struct {
				name   string
				boxes  int
				render func()
			}{
				{"topics", 1, func() {
					u.renderTopicList([]discourse.JSON{{"id": 1, "title": "Topic", "posts_count": 1}}, 0, "latest", "monthly", false)
				}},
				{"empty-topics", 1, func() { u.renderTopicList(nil, 0, "latest", "monthly", false) }},
				{"search", 2, func() {
					u.renderSearch("query", []discourse.JSON{{"topic_id": 1, "blurb": "query found here"}}, map[int]string{1: "Result"}, 0)
				}},
				{"empty-search", 2, func() { u.renderSearch("query", nil, nil, 0) }},
				{"search-entry", 2, func() {
					u.renderSingleLine("ui.search.title", "ui.search.prompt", &input, primarySearch, "search", "query")
				}},
				{"notifications", 1, func() {
					u.renderNotifications([]discourse.JSON{{"topic_id": 1, "data": discourse.JSON{"display_username": "member"}}}, 0, "all", false)
				}},
				{"empty-notifications", 1, func() { u.renderNotifications(nil, 0, "all", false) }},
				{"topic", 3, func() {
					u.renderTopic(discourse.JSON{"id": 1, "title": "Topic"}, []discourse.JSON{{"id": 1, "username": "member", "raw": "A post"}}, 0, 0, false, primaryTopics)
				}},
				{"compose", 1, func() { u.renderComposer("Draft", &area, 20, nil, "", "new_topic", "") }},
				{"compose-context", 2, func() {
					u.renderComposer("Reply", &area, 20, []string{"Reply context"}, "", "reply_post", "Retry notice")
				}},
				{"title-entry", 2, func() {
					u.renderSingleLine("ui.composer.new_topic_title", "ui.composer.enter_title", &input, primaryCompose, "compose", "title")
				}},
				{"category", 1, func() {
					u.terminal.state.inputs <- terminalInput{msg: tea.KeyPressMsg(tea.Key{Code: tea.KeyEscape})}
					u.pickCategory()
				}},
				{"error", 1, func() {
					u.terminal.state.inputs <- terminalInput{msg: tea.KeyPressMsg(tea.Key{Code: tea.KeyEscape})}
					u.showError(errors.New("An error message"))
				}},
				{"loading", 1, u.renderStartupLoading},
			}
			for _, tc := range cases {
				t.Run(tc.name, func(t *testing.T) {
					tc.render()
					frame := nextFrame()
					if lipgloss.Width(frame) != width || lipgloss.Height(frame) != height {
						t.Fatalf("frame size = %dx%d", lipgloss.Width(frame), lipgloss.Height(frame))
					}
					if height >= 16 {
						if got := len(paneBoxRows(frame)); got != tc.boxes {
							t.Fatalf("content boxes = %d, want %d\n%s", got, tc.boxes, stripANSI(frame))
						}
					}
					for _, region := range u.mouseRegions {
						if _, ok := parseRowKey(region.key); !ok {
							continue
						}
						if region.x0 != 2 || region.x1 != width-2 || region.y1 >= height {
							t.Fatalf("row target overlaps pane border or footer: %+v", region)
						}
						plain := strings.Split(stripANSI(frame), "\n")[region.y0]
						if !strings.HasPrefix(plain, "│") || !strings.HasSuffix(plain, "│") {
							t.Fatalf("row target is outside its box: %+v, %q", region, plain)
						}
					}
					if tc.name == "search-entry" && height >= 16 {
						boxes := paneBoxRows(frame)
						queryRow := -1
						for row, line := range strings.Split(stripANSI(frame), "\n") {
							if strings.Contains(line, "Search: a query") {
								queryRow = row
							}
						}
						if queryRow <= boxes[0] || queryRow >= boxes[1] {
							t.Fatalf("editable query is outside the header: row=%d boxes=%v", queryRow, boxes)
						}
					}
				})
			}
		})
	}
}

func TestEmptyHeadersMoveTheirTitleToTheMainPane(t *testing.T) {
	t.Setenv("TERMCOURSE_COLOR_MODE", "truecolor")
	for _, lines := range [][]string{nil, {}, {"", "  "}, {"\x1b[31m \x1b[m"}} {
		u := &UI{style: NewStyle(testTheme(), io.Discard), locale: "en"}
		header := u.navigationHeader("Section", primaryTopics, "", "", "", lines, 40, 12)
		screen, pane := u.mainPane(header, 40, 12, 1)
		u.drawMainPane(screen, pane, []string{"Body"})
		if len(header) != 3 || len(paneBoxRows(strings.Join(screen, "\n"))) != 1 || !strings.Contains(stripANSI(screen[3]), "SECTION") {
			t.Fatalf("empty header retained a separate box: %#v", screen)
		}
	}
}

func TestMainPanePreservesKittyPlaceholderSelection(t *testing.T) {
	t.Setenv("TERMCOURSE_COLOR_MODE", "truecolor")
	value := testTheme()
	value.Background = "#101820"
	u := New(nil, Options{Theme: value, Output: io.Discard})
	header := u.imageScreenHeader(40, 16)
	screen, pane := u.mainPane(header, 40, 16, 1)
	u.drawMainPane(screen, pane, kittyPlaceholderLines(0x123456, 4, 3))
	cells := backgroundTestLines(u.renderer.frame(screen, 40, 16))
	for y := pane.y; y < pane.y+3; y++ {
		for x := pane.x; x < pane.x+4; x++ {
			cell := cells[y][x]
			if !strings.HasPrefix(cell.Content, string(kitty.Placeholder)) || kittyTestColorID(cell.Style.Fg) != 0x123456 || kittyTestColorID(cell.Style.UnderlineColor) != kittyPlacementID {
				t.Fatalf("pane changed Kitty placeholder selection: %+v", cell)
			}
		}
		if cells[y][pane.x+4].Style.UnderlineColor != nil {
			t.Fatal("placement ID leaked into pane padding")
		}
	}
}

func TestDataEntryFieldsTintEveryEditableCell(t *testing.T) {
	for _, mode := range []string{"truecolor", "256", "16"} {
		t.Run(mode, func(t *testing.T) {
			t.Setenv("TERMCOURSE_COLOR_MODE", mode)
			for _, background := range []string{"", "#101820", "#eeeeee"} {
				value := testTheme()
				value.Background = theme.Color(background)
				if background == "#eeeeee" {
					value.Primary, value.ListText = "#101820", "#101820"
				}
				u := New(nil, Options{Theme: value, Output: io.Discard})
				u.terminal.state.width.Store(88)
				u.terminal.state.height.Store(24)
				nextFrame := capturePaneFrames(t, u)
				input := textinput.New()
				input.Focus()
				area := textarea.New()
				area.Focus()
				area.Prompt = " "
				area.ShowLineNumbers = false
				area.EndOfBufferCharacter = ' '
				for _, text := range []string{"", "Typed | text · test"} {
					input.SetValue(text)
					area.SetValue(text)
					for _, field := range []string{"search", "title", "composer"} {
						var frame string
						if field == "composer" {
							u.renderComposer("Draft", &area, 20, nil, "", "new_topic", "")
							frame = nextFrame()
						} else if field == "search" {
							input.Prompt = "Search: "
							u.renderSingleLine("ui.search.title", "ui.search.prompt", &input, primarySearch, "search", "query")
							frame = nextFrame()
						} else {
							input.Prompt = "Title: "
							u.renderSingleLine("ui.composer.new_topic_title", "ui.composer.enter_title", &input, primaryCompose, "compose", "title")
							frame = nextFrame()
						}
						if !strings.Contains(stripANSI(frame), text) {
							t.Fatalf("%s field changed typed punctuation: %q", field, stripANSI(frame))
						}
						rows := strings.Split(stripANSI(frame), "\n")
						start, end := -1, -1
						if field == "composer" {
							boxes := paneBoxRows(frame)
							start, end = boxes[len(boxes)-1]+1, len(rows)-2
						} else {
							for row, line := range rows {
								if strings.Contains(line, input.Prompt) {
									start, end = row, row+1
								}
							}
						}
						if start < 0 || end <= start {
							t.Fatalf("missing %s field", field)
						}
						cells := backgroundTestLines(frame)
						for y := start; y < end; y++ {
							for x := 2; x < 86; x++ {
								assertBackground(t, cells[y][x].Style.Bg, u.style.input.GetBackground())
							}
							for _, x := range []int{0, 1, 86, 87} {
								assertBackground(t, cells[y][x].Style.Bg, u.style.body.GetBackground())
							}
						}
					}
				}
			}
		})
	}
}
