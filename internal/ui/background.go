package ui

import (
	"image/color"
	"strings"

	"charm.land/lipgloss/v2"
	uv "github.com/charmbracelet/ultraviolet"
	"github.com/charmbracelet/x/ansi"
)

// withDefaultBackground fills cells that would otherwise use the terminal's
// profile background. Parse SGR rather than replacing reset strings: explicit
// editor tints, button highlights, text attributes and hyperlinks must survive.
func withDefaultBackground(text string, background color.Color) string {
	if text == "" || background == nil {
		return text
	}
	if _, ok := background.(lipgloss.NoColor); ok {
		return text
	}
	p := ansi.GetParser()
	defer ansi.PutParser(p)
	var pen uv.Style
	var state byte
	var out strings.Builder
	out.Grow(len(text))
	fill := ansi.Style{}.BackgroundColor(background).String()
	for len(text) > 0 {
		seq, width, n, next := ansi.DecodeSequence(text, state, p)
		if width > 0 && pen.Bg == nil {
			out.WriteString(fill)
			pen.Bg = background
		} else if ansi.HasCsiPrefix(seq) && p.Command() == 'm' {
			uv.ReadStyle(p.Params(), &pen)
		}
		out.WriteString(seq)
		text, state = text[n:], next
	}
	out.WriteString(ansi.ResetStyle)
	return out.String()
}
