package ui

import (
	"html"
	"regexp"
	"strconv"
	"strings"

	"github.com/charmbracelet/x/ansi"
)

var htmlPattern = regexp.MustCompile(`<[^>]*>`)

func stripANSI(value string) string {
	return ansi.Strip(value)
}

func displayWidth(value string) int {
	return ansi.StringWidth(value)
}

type labelTier struct {
	labels    []string
	separator string
}

// chooseLabelTier returns the first tier that fits, or the last tier as a fallback.
func chooseLabelTier(tiers []labelTier, width int) labelTier {
	for _, candidate := range tiers {
		total := max(len(candidate.labels)-1, 0) * displayWidth(candidate.separator)
		for _, label := range candidate.labels {
			total += displayWidth(label)
		}
		if total <= width {
			return candidate
		}
	}
	return tiers[len(tiers)-1]
}

func visibleWidth(value string) int { return displayWidth(stripANSI(value)) }

func truncateVisible(value string, width int) string {
	if width <= 0 {
		return ""
	}
	return ansi.Truncate(value, width, "")
}

func truncate(value string, width int) string {
	if displayWidth(value) <= width {
		return value
	}
	if width <= 3 {
		return truncateVisible(value, width)
	}
	return truncateVisible(value, width-3) + "..."
}

func padLine(value string, width int) string {
	value = strings.Map(func(r rune) rune {
		if r == '\t' {
			return ' '
		}
		// Keep ESC for CSI/SGR and BEL for OSC8 hyperlink terminators.
		if (r < 0x20 && r != 0x1b && r != 0x07) || r == 0x7f || (r >= 0x200b && r <= 0x200f) {
			return -1
		}
		return r
	}, value)
	if visibleWidth(value) > width {
		value = truncateVisible(value, width)
	}
	return value + strings.Repeat(" ", max(width-visibleWidth(value), 0))
}

func fitCell(value string, width int, right bool) string {
	value = truncate(value, width)
	padding := strings.Repeat(" ", max(width-displayWidth(value), 0))
	if right {
		return padding + value
	}
	return value + padding
}

func headerLine(left, right string, width int) string {
	if width <= 0 {
		return ""
	}
	if right == "" {
		return padLine(left, width)
	}
	right = truncateVisible(right, max(width-1, 0))
	rightWidth := visibleWidth(right)
	leftWidth := max(width-rightWidth-1, 0)
	left = truncateVisible(left, leftWidth)
	spaces := max(width-visibleWidth(left)-rightWidth, 0)
	return left + strings.Repeat(" ", spaces) + right
}

func stripHTML(value string) string {
	return html.UnescapeString(htmlPattern.ReplaceAllString(value, ""))
}

func emojify(value string, enabled bool) string {
	if !enabled {
		return value
	}
	replacements := map[string]string{
		":heart:": "❤️", ":+1:": "👍", ":-1:": "👎", ":thumbsup:": "👍", ":thumbsdown:": "👎",
		":smile:": "😄", ":laughing:": "😆", ":joy:": "😂", ":wink:": "😉", ":blush:": "😊",
		":thinking:": "🤔", ":tada:": "🎉", ":fire:": "🔥", ":rocket:": "🚀", ":eyes:": "👀",
		":warning:": "⚠️", ":white_check_mark:": "✅", ":x:": "❌", ":-)": "🙂", ":)": "🙂",
		";-)": "😉", ";)": "😉", ":-D": "😄", ":D": "😄", ":-(": "🙁", ":(": "🙁",
	}
	for from, to := range replacements {
		value = strings.ReplaceAll(value, from, to)
	}
	return value
}

func formatCount(value any) string {
	return strconv.Itoa(intNumber(value))
}

func intNumber(value any) int {
	switch v := value.(type) {
	case float64:
		return int(v)
	case int:
		return v
	case string:
		n, _ := strconv.Atoi(v)
		return n
	default:
		return 0
	}
}
