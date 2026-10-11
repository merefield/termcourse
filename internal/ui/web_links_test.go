package ui

import (
	"bytes"
	"strings"
	"sync"
	"testing"

	uv "github.com/charmbracelet/ultraviolet"
	"github.com/charmbracelet/x/ansi"
)

func TestWebLinksExpandAllDestinations(t *testing.T) {
	for _, target := range []string{"https://github.com/repo/releases", "https://openai.com/docs", "https://unlisted.example/a_(b)?q=1&x=2", "http://localhost:3000/help", "http://localhost:3000/" + strings.Repeat("path", 12)} {
		for _, source := range []string{
			"[release](" + target + ") and plain text",
			"[**bold** label](" + target + " \"title\")",
			"[release][ref]\n\n[ref]: " + target,
			"<" + target + ">",
			target,
			"| Name | Link |\n| --- | --- |\n| Release | [release](" + target + ") |",
		} {
			for _, enabled := range []bool{true, false} {
				lines := wrapLines(source, 100, enabled)
				plain := ansi.Strip(strings.Join(lines, "\n"))
				if !strings.Contains(plain, target) {
					t.Fatalf("address hidden: enabled=%v source=%q output=%q", enabled, source, plain)
				}
				if !enabled && strings.Contains(strings.Join(lines, ""), "\x1b") {
					t.Fatal("disabled links contain escape sequences")
				}
				if enabled {
					found := false
					for _, line := range lines {
						for x := 0; x < visibleWidth(line); x++ {
							if webLinkAt(line, x, 0) == target {
								found = true
							}
						}
					}
					if !found {
						t.Fatalf("no clickable exact address for %q: %q", source, lines)
					}
				}
			}
		}
	}
}

func TestWrappedLinksSurviveScrollingAndDoNotLeakIntoPadding(t *testing.T) {
	t.Setenv("TERMCOURSE_COLOR_MODE", "truecolor")
	s := NewStyle(testTheme(), &bytes.Buffer{})
	target := "https://example.com/" + strings.Repeat("path", 30) + "?a=1&b=2"
	for _, width := range []int{2, 8, 12, 24, 50} {
		lines := wrapLines("[界e\u0301👩‍💻]("+target+") after", width, true)
		plain := strings.Join(strings.Fields(ansi.Strip(strings.Join(lines, "\n"))), "")
		if !strings.Contains(plain, target) {
			t.Fatal("wrapped destination truncated", plain)
		}
		if len(lines) < 2 {
			t.Fatal("long destination did not wrap")
		}
		linkedCells := 0
		for _, line := range lines {
			if visibleWidth(line) > width {
				t.Fatalf("line exceeds width %d: %q", width, line)
			}
			// A row can be drawn in isolation after clipping/scrolling and recolouring.
			frame := (&ScreenRenderer{background: s.body.GetBackground()}).frame([]string{s.Text(line, roleListText)}, width+3, 2)
			for y, row := range backgroundTestLines(frame) {
				for x, cell := range row {
					if cell.Link.URL != "" {
						linkedCells++
						if cell.Link.URL != target || webLinkAt(frame, x, y) != target {
							t.Fatalf("wrong destination in cell: %+v", cell)
						}
						if cell.Style.Underline == uv.UnderlineNone {
							t.Fatal("link not visibly underlined")
						}
					}
					if (y == 1 || x >= width) && cell.Link.URL != "" {
						t.Fatal("hyperlink leaked into padding or next row")
					}
				}
			}
		}
		if linkedCells == 0 {
			t.Fatal("links disappeared after wrapping and styling")
		}
	}
}

func TestWebURLValidationAndLiteralCode(t *testing.T) {
	for _, target := range []string{"javascript:alert(1)", "file:///tmp/file", "https://user:pass@example.com/", "https://example.com/a;b", "https://example.com/\a", "https://example.com/\u200b", "https://example.com/…", "https://example.com/" + strings.Repeat("a", 2048)} {
		if validWebURL(target) {
			t.Fatalf("invalid target accepted %q", target)
		}
		if strings.Contains(target, "\a") {
			continue
		}
		text := webTextLines(ansi.SetHyperlink(target, "arbitrary")+"label"+ansi.ResetHyperlink(), 40, true)
		if strings.Contains(strings.Join(text, ""), "\x1b]8;") {
			t.Fatalf("invalid destination remains clickable: %q", text)
		}
	}
	target := "https://example.com/a_(b)?x=1&y=2"
	value := "See (" + target + ")."
	linked := linkify(value, true)
	if ansi.Strip(linked) != value || webLinkAt(linked, 5, 0) != target {
		t.Fatalf("punctuation changed: %q", linked)
	}
	for _, source := range []string{"`[name](https://example.com)`", "```text\n[name](https://example.com)\n```"} {
		text := strings.Join(wrapLines(source, 80, true), "\n")
		if strings.Contains(text, "\x1b]8;") || !strings.Contains(ansi.Strip(text), "[name](https://example.com)") {
			t.Fatalf("code changed: %q", text)
		}
	}
}

func TestWebLinkHitTestingUsesGraphemesAndGeneratedID(t *testing.T) {
	target := "https://example.com"
	text := "\n  " + webLink(target, "界e\u0301👩‍💻") + " end"
	for x := 2; x < 7; x++ {
		if webLinkAt(text, x, 1) != target {
			t.Fatalf("missed Unicode cell %d", x)
		}
	}
	for _, point := range [][2]int{{-1, 1}, {2, -1}, {2, 0}, {1, 1}, {7, 1}, {99, 1}} {
		if webLinkAt(text, point[0], point[1]) != "" {
			t.Fatalf("non-link cell opened: %v", point)
		}
	}
	if webLinkAt(ansi.SetHyperlink(target, "id=foreign")+"foreign"+ansi.ResetHyperlink(), 0, 0) != "" {
		t.Fatal("foreign OSC actionable")
	}
}

func TestMarkdownRendererCanRenderLinksConcurrently(t *testing.T) {
	var wg sync.WaitGroup
	for i := 0; i < 8; i++ {
		wg.Go(func() {
			for j := 0; j < 5; j++ {
				lines := wrapLines("[docs](https://example.com/docs)", 40, true)
				if !strings.Contains(ansi.Strip(strings.Join(lines, "\n")), "https://example.com/docs") {
					t.Error("cached renderer corrupted output")
				}
			}
		})
	}
	wg.Wait()
}

func TestTinyLinkFrameClosesBeforeNextRow(t *testing.T) {
	target := "https://example.com/long"
	for _, width := range []int{1, 2, 3} {
		for _, line := range wrapLines(target, width, true) {
			frame := normalizeScreen([]string{line}, width, 2)
			if webLinkAt(frame, 0, 1) != "" {
				t.Fatal("link leaked below tiny row")
			}
		}
	}
}
