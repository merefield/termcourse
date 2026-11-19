package ui

import (
	"bytes"
	"strings"
	"testing"

	uv "github.com/charmbracelet/ultraviolet"
	"github.com/merefield/termcourse/internal/discourse"
)

func TestPostMarkdownStylesStayInsideTheirSpans(t *testing.T) {
	t.Setenv("TERMCOURSE_COLOR_MODE", "truecolor")
	t.Setenv("TERMCOURSE_IMAGES", "0")
	u := UI{style: NewStyle(testTheme(), &bytes.Buffer{})}
	post := discourse.JSON{"username": "member", "raw": "**B** *I* `C*` P\n\n***abcdefghij***"}
	for _, width := range []int{10, 40} {
		lines := u.postBlock(post, true, width)[1:]
		var seen strings.Builder
		for _, line := range lines {
			// Each line can be redrawn on its own when the post is scrolled.
			for _, cell := range backgroundTestLines(line + "│")[0] {
				var want uint8
				switch cell.Content {
				case "B":
					want = uv.AttrBold
				case "I":
					want = uv.AttrItalic
				case "C", "*":
					want = uv.AttrReverse
				case " ", "│", "P":
				default:
					want = uv.AttrBold | uv.AttrItalic
				}
				if cell.Style.Attrs != want {
					t.Fatalf("width %d, cell %q has attributes %v, want %v", width, cell.Content, cell.Style.Attrs, want)
				}
				if cell.Content != " " && cell.Content != "│" {
					seen.WriteString(cell.Content)
					assertBackground(t, cell.Style.Fg, u.style.roles[roleListText].GetForeground())
				}
			}
		}
		if got := seen.String(); got != "BIC*Pabcdefghij" {
			t.Fatalf("width %d displayed %q", width, got)
		}
	}
}

func TestMarkdownTablesAlignAndReflow(t *testing.T) {
	source := "| City | Total |\n|:---|---:|\n| **界** | `3` |\n| London | 100 |"
	wide := wrapLines(source, 32, false)
	if got := stripANSI(strings.Join(wide, "\n")); got != "City    Total\n─────────────\n界          3\nLondon    100" {
		t.Fatalf("unaligned table:\n%s", got)
	}
	cells := backgroundTestLines(strings.Join(wide, "\n"))
	if cells[0][0].Style.Attrs != uv.AttrBold || cells[2][0].Style.Attrs != uv.AttrBold || cells[2][len(cells[2])-1].Style.Attrs != uv.AttrReverse {
		t.Fatal("table header, emphasis or inline code lost its style")
	}

	narrow := wrapLines(source, 10, false)
	for _, line := range narrow {
		if visibleWidth(line) > 10 {
			t.Fatalf("table exceeds pane width: %q", line)
		}
	}
	if got := stripANSI(strings.Join(narrow, "\n")); got != "City: 界\nTotal: 3\n\nCity: Lond\non\nTotal: 100" {
		t.Fatalf("table lost a label or value when reflowing:\n%s", got)
	}
}

func TestMarkdownTableKeepsReferenceLinksInNarrowPanes(t *testing.T) {
	const target = "https://example.com/termcourse"
	source := "Before.\n\n| Docs |\n|---|\n| [Guide][docs] |\n\nAfter.\n\n[docs]: " + target
	lines := wrapLines(source, 12, true)
	for _, line := range lines {
		if visibleWidth(line) > 12 {
			t.Fatalf("table exceeds pane width: %q", line)
		}
	}
	plain := strings.Join(strings.Fields(stripANSI(strings.Join(lines, "\n"))), "")
	if plain != "Before.Docs:Guide"+target+"After." {
		t.Fatalf("lost content around wrapped reference link: %q", plain)
	}
	for _, line := range lines {
		for x := 0; x < visibleWidth(line); x++ {
			if webLinkAt(line, x, 0) == target {
				return
			}
		}
	}
	t.Fatal("reference link is no longer clickable")
}
