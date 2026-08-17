package ui

import (
	"fmt"
	"strings"
	"time"

	"charm.land/bubbles/v2/textarea"
	"charm.land/bubbles/v2/textinput"
	tea "charm.land/bubbletea/v2"
	"charm.land/lipgloss/v2"
	"github.com/merefield/termcourse/internal/discourse"
)

func (u *UI) promptSingleLine(titleKey, promptKey, prefix string, active primaryTabID, context, selected string) string {
	u.navigationLocked = true
	defer func() { u.navigationLocked = false }()
	input := textinput.New()
	input.Prompt = prefix
	u.styleTextInput(&input)
	u.terminal.Run(input.Focus())
	for {
		width, height := u.terminal.Size()
		input.SetWidth(max(width, 1))
		header := u.navigationHeader(u.t(titleKey), active, context, selected, "", []string{u.t(promptKey)}, width, height)
		screen := make([]string, height)
		copy(screen, header)
		row := min(len(header)+1, height-1)
		screen[row] = input.View()
		u.renderer.Render(screen, width, height, "single-line-"+titleKey, -1, -1, false)
		msg, err := u.terminal.ReadMsg(u.tick)
		if err != nil {
			return ""
		}
		if key, ok := msg.(tea.KeyPressMsg); ok {
			switch key.String() {
			case "enter":
				return strings.TrimSpace(input.Value())
			case "ctrl+d", "esc":
				return ""
			}
		}
		var cmd tea.Cmd
		input, cmd = input.Update(msg)
		u.terminal.Run(cmd)
	}
}

func (u *UI) compose(title string, context []string, category, variant string) string {
	u.navigationLocked = true
	defer func() { u.navigationLocked = false }()
	const minLength = 20
	area := textarea.New()
	area.Prompt = " "
	area.ShowLineNumbers = false
	area.EndOfBufferCharacter = ' '
	u.styleTextArea(&area)
	u.terminal.Run(area.Focus())
	for {
		u.renderComposer(title, &area, minLength, context, category, variant, "")
		msg, err := u.terminal.ReadMsg(u.tick)
		if err != nil {
			return ""
		}
		if key, ok := msg.(tea.KeyPressMsg); ok {
			switch key.String() {
			case "ctrl+d":
				value := strings.TrimSpace(area.Value())
				if len([]rune(value)) >= minLength {
					return area.Value()
				}
				u.renderComposer(title, &area, minLength, context, category, variant, u.t("ui.composer.retry"))
				_, _ = u.terminal.ReadMsg(24 * time.Hour)
				area.SetValue("")
				continue
			case "esc":
				return ""
			}
		}
		var cmd tea.Cmd
		area, cmd = area.Update(msg)
		u.terminal.Run(cmd)
	}
}

func (u *UI) renderComposer(title string, area *textarea.Model, minLength int, context []string, category, variant, notice string) {
	width, height := u.terminal.Size()
	inner := max(width-4, 1)
	count := len([]rune(strings.TrimSpace(area.Value())))
	status := u.t("ui.controls.composer", "status", fmt.Sprintf("%d / %d", count, minLength))
	var lines []string
	if len(context) > 0 {
		lines = append(lines, context...)
		lines = append(lines, strings.Repeat("-", inner))
	}
	lines = append(lines, headerLine(status, category, inner))
	header := u.navigationHeader(u.t("ui.composer.compose")+" "+title, primaryCompose, "compose", variant, "", lines, width, height)
	screen := make([]string, height)
	copy(screen, header)
	start := len(header) + 1
	area.SetWidth(max(width-1, 1))
	area.SetHeight(max(height-start-2, 1))
	inputLines := strings.Split(strings.TrimSuffix(area.View(), "\n"), "\n")
	for index, line := range inputLines {
		if start+index < height {
			screen[start+index] = " " + line
		}
	}
	if notice != "" {
		row := min(start+len(inputLines)+1, height-1)
		screen[row] = u.style.Text(notice, roleAccent)
	}
	u.renderer.Render(screen, width, height, "composer-"+title, -1, -1, false)
}

func (u *UI) styleTextInput(input *textinput.Model) {
	styles := input.Styles()
	styles.Focused.Text = u.style.roles[roleListText]
	styles.Focused.Prompt = u.style.roles[roleAccent]
	styles.Focused.Placeholder = u.style.roles[roleListMeta]
	styles.Focused.Suggestion = u.style.roles[roleListMeta]
	styles.Blurred = styles.Focused
	styles.Cursor.Color = u.style.profile.Convert(lipgloss.Color(string(u.style.Theme.Accent)))
	input.SetStyles(styles)
}

func (u *UI) styleTextArea(area *textarea.Model) {
	styles := area.Styles()
	styles.Focused.Base = u.style.roles[roleListText]
	styles.Focused.Text = u.style.roles[roleListText]
	styles.Focused.LineNumber = u.style.roles[roleListMeta]
	styles.Focused.CursorLineNumber = u.style.roles[roleAccent]
	styles.Focused.CursorLine = u.style.roles[roleListText]
	styles.Focused.EndOfBuffer = u.style.roles[rolePrimary]
	styles.Focused.Placeholder = u.style.roles[roleListMeta]
	styles.Focused.Prompt = u.style.roles[roleAccent]
	styles.Blurred = styles.Focused
	styles.Cursor.Color = u.style.profile.Convert(lipgloss.Color(string(u.style.Theme.Accent)))
	area.SetStyles(styles)
}

func (u *UI) newTopicFlow() discourse.JSON {
	title := u.promptSingleLine("ui.composer.new_topic_title", "ui.composer.enter_title", "Title: ", primaryCompose, "compose", "title")
	if title == "" {
		return nil
	}
	category, label := u.pickCategory()
	body := u.compose(u.t("ui.composer.new_topic_body", "title", title), nil, label, "new_topic")
	if body == "" {
		return nil
	}
	result := discourse.JSON{"title": title, "raw": body}
	if category != nil {
		result["category"] = *category
	}
	return result
}

func (u *UI) pickCategory() (*int, string) {
	u.navigationLocked = true
	defer func() { u.navigationLocked = false }()
	info := u.siteInfo()
	type option struct {
		id    *int
		label string
	}
	options := []option{{nil, u.t("ui.composer.no_category_option")}}
	defaultIndex := 0
	defaultID := discourse.Int(info["default_category_id"])
	for _, raw := range discourse.Slice(info["categories"]) {
		category := discourse.Map(raw)
		if discourse.Bool(category["read_restricted"]) {
			continue
		}
		id := discourse.Int(category["id"])
		options = append(options, option{&id, discourse.String(category["name"])})
		if id == defaultID {
			defaultIndex = len(options) - 1
		}
	}
	selected := defaultIndex
	for {
		width, height := u.terminal.Size()
		header := u.navigationHeader(u.t("ui.composer.select_category"), primaryCompose, "compose", "category", "", []string{u.t("ui.controls.category_picker")}, width, height)
		screen := make([]string, height)
		copy(screen, header)
		rows := max(height-len(header)-1, 0)
		start := max(selected-rows/2, 0)
		for index := start; index < min(start+rows, len(options)); index++ {
			line := options[index].label
			if index == selected {
				line = u.style.Selected(line)
			}
			y := len(header) + 1 + index - start
			if y < 0 || y >= height {
				continue
			}
			screen[y] = line
			u.addMouseRegion(0, y, width, 1, rowKey(index))
		}
		u.renderer.Render(screen, width, height, "category-picker", -1, -1, false)
		key, err := u.readKey(u.tick)
		if err != nil {
			return nil, u.t("ui.composer.category_none")
		}
		if row, ok := parseRowKey(key); ok {
			if row >= 0 && row < len(options) {
				if row == selected {
					key = "enter"
				} else {
					selected = row
					continue
				}
			}
		}
		switch key {
		case "up", "wheelup":
			selected = max(selected-1, 0)
		case "down", "wheeldown":
			selected = min(selected+1, len(options)-1)
		case "enter":
			label := u.t("ui.composer.category_named", "name", options[selected].label)
			if options[selected].id == nil {
				label = u.t("ui.composer.category_none")
			}
			return options[selected].id, label
		case "esc":
			return nil, u.t("ui.composer.category_none")
		}
	}
}

func (u *UI) replyContext(post discourse.JSON) []string {
	body := wrapLines(discourse.String(post["raw"]), max(terminalWidth(u.terminal)-3, 1), u.linksEnabled)
	if len(body) > 3 {
		body = body[:3]
	}
	return append([]string{u.t("ui.composer.replying_to", "username", discourse.String(post["username"]))}, body...)
}

func terminalWidth(terminal *Terminal) int {
	width, _ := terminal.Size()
	return width
}
