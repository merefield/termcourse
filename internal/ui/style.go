package ui

import (
	"fmt"
	"io"
	"os"
	"strings"

	"charm.land/lipgloss/v2"
	"github.com/charmbracelet/colorprofile"
	"github.com/merefield/termcourse/internal/theme"
)

type textRole uint8

const (
	rolePrimary textRole = iota
	roleSeparator
	roleListNumber
	roleListText
	rolePostUsername
	roleListMeta
	roleAccent
)

type Style struct {
	Theme      theme.Theme
	ColorMode  string
	profile    colorprofile.Profile
	roles      map[textRole]lipgloss.Style
	body       lipgloss.Style
	border     lipgloss.Style
	selected   lipgloss.Style
	header     lipgloss.Style
	headerSep  lipgloss.Style
	bar        lipgloss.Style
	barFill    lipgloss.Style
	barTrack   lipgloss.Style
	brand      lipgloss.Style
	label      lipgloss.Style
	liked      lipgloss.Style
	tab        lipgloss.Style
	tabActive  lipgloss.Style
	tabHover   lipgloss.Style
	tabHot     lipgloss.Style
	control    lipgloss.Style
	controlHot lipgloss.Style
}

func NewStyle(value theme.Theme, output io.Writer) *Style {
	profile, mode := resolveColorProfile(output)
	result := &Style{Theme: value, ColorMode: mode, profile: profile}
	result.roles = map[textRole]lipgloss.Style{
		rolePrimary: result.makeStyle(value.Primary, ""), roleSeparator: result.makeStyle(value.Separator, ""),
		roleListNumber: result.makeStyle(value.ListNumber, ""), roleListText: result.makeStyle(value.ListText, ""),
		rolePostUsername: result.makeStyle(value.PostUsername, ""), roleListMeta: result.makeStyle(value.ListMeta, ""),
		roleAccent: result.makeStyle(value.Accent, ""),
	}
	result.body = result.makeStyle(value.Primary, value.Background)
	result.border = result.makeStyle(value.Border, "")
	result.selected = result.makeStyle(value.SelectedText, value.Selected)
	result.header = result.makeStyle(value.Primary, value.HeaderBackground)
	result.headerSep = result.makeStyle(value.Separator, value.HeaderBackground)
	result.bar = result.makeStyle(value.Primary, value.HeaderBackground)
	result.barFill = result.makeStyle(value.Accent, value.HeaderBackground).Bold(true)
	result.barTrack = result.makeStyle(value.Separator, value.HeaderBackground)
	result.brand = result.makeStyle(value.Primary, "").Bold(true)
	result.label = result.makeStyle(value.Accent, "").Bold(true)
	result.liked = result.makeStyle(theme.Color("red"), "").Bold(true)
	result.tab = result.makeStyle(value.Border, "")
	result.tabActive = result.makeStyle(value.Primary, value.Border).Bold(true)
	result.tabHover = result.makeStyle(value.Accent, "").Bold(true)
	result.tabHot = result.makeStyle(value.Primary, value.Accent).Bold(true)
	result.control = result.makeStyle(value.Border, "").Bold(true)
	result.controlHot = result.makeStyle(value.Accent, "").Bold(true)
	return result
}

func resolveColorProfile(output io.Writer) (colorprofile.Profile, string) {
	mode := strings.ToLower(os.Getenv("TERMCOURSE_COLOR_MODE"))
	profile := colorprofile.Detect(output, os.Environ())
	switch mode {
	case "truecolor":
		profile = colorprofile.TrueColor
	case "16":
		profile = colorprofile.ANSI
	case "256":
		profile = colorprofile.ANSI256
	default:
		switch profile {
		case colorprofile.TrueColor:
			mode = "truecolor"
		case colorprofile.ANSI256:
			mode = "256"
		case colorprofile.ANSI:
			mode = "16"
		default:
			mode = "none"
		}
	}
	return profile, mode
}

func (s *Style) Text(value string, role textRole) string {
	return s.roles[role].Render(value)
}

func (s *Style) Selected(value string) string {
	return s.selected.Render(stripANSI(value))
}

func (s *Style) Liked(value string) string { return s.liked.Render(value) }

func (s *Style) Box(lines []string, width int) []string {
	return s.frame("", lines, width, s.body, func(_ int, line string) string {
		return s.body.Render(line)
	})
}

func (s *Style) AppHeader(section, host string, lines []string, width, height int) []string {
	return append(s.AppTitle(host, width, height), s.HeaderBox(section, lines, width)...)
}

func (s *Style) AppTitle(host string, width, height int) []string {
	var output []string
	if width >= 64 && height >= 30 {
		logo := []string{
			"▀█▀ █▀▀ █▀█ █▀▄▀█ █▀▀ █▀█ █ █ █▀█ █▀▀ █▀▀",
			" █  ██▄ █▀▄ █ ▀ █ █▄▄ █▄█ █▄█ █▀▄ ▄▄█ ██▄",
		}
		output = append(output, headerLine(s.brand.Render(logo[0]), s.label.Render("● ONLINE"), width))
		output = append(output, s.brand.Render(logo[1]))
		subtitle := s.label.Render("◉ DISCOURSE TERMINAL") + " " +
			s.Text("·", roleSeparator) + " " + s.label.Render(strings.ToUpper(s.Theme.Name))
		output = append(output, headerLine(subtitle, s.Text(host, roleListMeta), width))
	} else {
		output = append(output, headerLine(s.brand.Render("▰ TERMCOURSE ▰"), s.Text(host, roleListMeta), width))
	}
	return output
}

func (s *Style) HeaderBox(title string, lines []string, width int) []string {
	prepared := make([]string, len(lines))
	for index, line := range lines {
		line = strings.ReplaceAll(line, " | ", " · ")
		if isHorizontalRule(line) {
			line = strings.Repeat("─", max(width-4, 1))
		}
		prepared[index] = line
	}
	return s.frame(title, prepared, width, s.header, func(_ int, line string) string {
		if isHorizontalRule(line) {
			return s.headerSep.Render(line)
		}
		parts := strings.Split(line, "·")
		for partIndex := range parts {
			parts[partIndex] = s.header.Render(parts[partIndex])
		}
		separator := s.headerSep.Render("·")
		return strings.Join(parts, separator)
	})
}

func (s *Style) BarBox(title string, lines []string, width int) []string {
	return s.frame(title, lines, width, s.bar, func(_ int, line string) string {
		return s.bar.Render(line)
	})
}

type progressBoxLayout struct {
	label          string
	barX, barWidth int
	gap, filled    int
}

func layoutProgressBox(current, total, width int) progressBoxLayout {
	inner, _ := frameInnerWidth(max(width, 1))
	label := fmt.Sprintf("%d/%d", current, total)
	label = truncateVisible(label, inner)
	available := max(inner-displayWidth(label), 0)
	gap := min(2, available)
	barWidth := max(available-gap, 0)
	filled := 0
	if total > 0 {
		filled = int(float64(current)/float64(total)*float64(barWidth) + 0.5)
	}
	filled = min(max(filled, 0), barWidth)
	_, sidePadding := frameInnerWidth(max(width, 1))
	barX := 1
	if sidePadding {
		barX++
	}
	return progressBoxLayout{
		label: label, barX: barX, barWidth: barWidth, gap: gap, filled: filled,
	}
}

func (s *Style) ProgressBox(title string, current, total, width int, hovered ...bool) []string {
	layout := layoutProgressBox(current, total, width)
	hot := len(hovered) > 0 && hovered[0]
	return s.frame(title, []string{""}, width, s.bar, func(_ int, _ string) string {
		fillStyle, trackStyle := s.barFill, s.barTrack
		if hot {
			fillStyle, trackStyle = s.controlHot, s.controlHot
		}
		return fillStyle.Render(strings.Repeat("█", layout.filled)) +
			trackStyle.Render(strings.Repeat("░", layout.barWidth-layout.filled)) +
			s.bar.Render(strings.Repeat(" ", layout.gap)+layout.label)
	})
}

func (s *Style) frame(title string, lines []string, width int, padding lipgloss.Style, decorate func(int, string) string) []string {
	width = max(width, 1)
	inner, sidePadding := frameInnerWidth(width)
	out := []string{s.frameEdge(title, width, true)}
	for index, line := range lines {
		content := decorate(index, padLine(line, inner))
		switch {
		case width == 1:
			out = append(out, s.border.Render("│"))
		case width == 2:
			out = append(out, s.border.Render("││"))
		case sidePadding:
			out = append(out, s.border.Render("│")+padding.Render(" ")+content+padding.Render(" ")+s.border.Render("│"))
		default:
			out = append(out, s.border.Render("│")+content+s.border.Render("│"))
		}
	}
	return append(out, s.frameEdge("", width, false))
}

func (s *Style) frameEdge(title string, width int, top bool) string {
	border := lipgloss.RoundedBorder()
	left, horizontal, right := border.BottomLeft, border.Bottom, border.BottomRight
	if top {
		left, horizontal, right = border.TopLeft, border.Top, border.TopRight
	}
	if width == 1 {
		return s.border.Render(horizontal)
	}
	if width == 2 {
		return s.border.Render(left + right)
	}
	title = strings.ToUpper(strings.TrimSpace(truncateVisible(title, max(width-6, 0))))
	if !top || title == "" {
		return s.border.Render(left + strings.Repeat(horizontal, width-2) + right)
	}
	prefix := left + horizontal + " "
	suffixWidth := max(width-displayWidth(prefix)-displayWidth(title)-2, 1)
	suffix := " " + strings.Repeat(horizontal, suffixWidth) + right
	return s.border.Render(prefix) + s.border.Bold(true).Render(title) + s.border.Render(suffix)
}

func (s *Style) makeStyle(foreground, background theme.Color) lipgloss.Style {
	style := lipgloss.NewStyle()
	if foreground != "" {
		style = style.Foreground(s.profile.Convert(lipgloss.Color(string(foreground))))
	}
	if background != "" {
		style = style.Background(s.profile.Convert(lipgloss.Color(string(background))))
	}
	return style
}

func frameInnerWidth(width int) (int, bool) {
	if width >= 4 {
		return width - 4, true
	}
	return max(width-2, 0), false
}

func isHorizontalRule(value string) bool {
	value = strings.TrimSpace(stripANSI(value))
	if value == "" {
		return false
	}
	return strings.Trim(value, "-─") == ""
}

func WriteThemePreviews(output io.Writer, themes []theme.Theme) {
	for index, value := range themes {
		style := NewStyle(value, output)
		if index > 0 {
			fmt.Fprintln(output)
		}
		fmt.Fprintf(output, "%s (%s)\n", value.Name, style.ColorMode)
		primary := layoutTabRail([]tabSpec{
			{id: "topics", label: "Topics", short: "TPS", micro: "T", selected: true, enabled: true},
			{id: "search", label: "Search", short: "SRC", micro: "S", enabled: true},
			{id: "notifications", label: "Notifications", short: "NOT", micro: "N", badge: 3, enabled: true},
			{id: "compose", label: "Compose", short: "CMP", micro: "C", enabled: true},
		}, 38)
		filters := layoutTabRail([]tabSpec{
			{id: "latest", label: "Latest", short: "LAT", micro: "L", selected: true, enabled: true},
			{id: "unread", label: "Unread", short: "UNR", micro: "U", enabled: true},
			{id: "private", label: "Private", short: "PRI", micro: "P", enabled: true},
			{id: "hot", label: "Hot", short: "HOT", micro: "H", enabled: true},
			{id: "new", label: "New", short: "NEW", micro: "N", enabled: true},
			{id: "top", label: "Top", short: "TOP", micro: "T", enabled: true},
		}, 48)
		header := style.AppTitle("community.example", 52, 24)
		header = append(header, strings.Repeat(" ", 52))
		header = append(header,
			headerLine(style.renderTabRail(primary), "member", 52),
			style.renderTabRail(filters),
		)
		for _, line := range header {
			fmt.Fprintln(output, line)
		}
		fmt.Fprintln(output)
		for _, line := range style.Box([]string{
			style.Text(" 1", roleListNumber) + " " + style.Text("A themed topic", roleListText) + "  " + style.Text("@member", rolePostUsername),
			style.Selected(" 2 Selected topic                              "),
			style.Text(" metadata", roleListMeta) + "  " + style.Text("accent", roleAccent),
		}, 52) {
			fmt.Fprintln(output, line)
		}
		themeStatus := "THEME // " + strings.ToUpper(value.Name)
		controls := layoutControls("t: theme | f: filter | g: refresh | q: quit", max(52-visibleWidth(themeStatus)-1, 0))
		fmt.Fprintln(output, headerLine(style.renderControls(controls, ""), style.Text(themeStatus, roleListMeta), 52))
	}
}
