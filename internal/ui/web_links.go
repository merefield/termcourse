package ui

import (
	"net/url"
	"regexp"
	"strings"
	"unicode"

	tea "charm.land/bubbletea/v2"
	uv "github.com/charmbracelet/ultraviolet"
	"github.com/charmbracelet/x/ansi"
	"github.com/yuin/goldmark"
	"github.com/yuin/goldmark/ast"
	"github.com/yuin/goldmark/extension"
	"github.com/yuin/goldmark/text"
)

const webLinkID = "id=termcourse-web"

var webURLPattern = regexp.MustCompile("(?i)https?://[^\\s<>\"'\x60\\x00-\\x20]+")

// Accept the same destinations for native terminal links and local opening.
// Ultraviolet cannot retain raw semicolons in OSC 8 URIs; keep those plain.
func validWebURL(target string) bool {
	if len(target) > 2048 || strings.HasSuffix(target, "…") || strings.Contains(target, ";") {
		return false
	}
	for _, r := range target {
		if unicode.IsControl(r) || unicode.Is(unicode.Cf, r) || unicode.IsSpace(r) {
			return false
		}
	}
	u, err := url.Parse(target)
	return err == nil && (strings.EqualFold(u.Scheme, "http") || strings.EqualFold(u.Scheme, "https")) && u.Hostname() != "" && u.User == nil
}

func webLink(target, label string) string {
	return ansi.SetHyperlink(target, webLinkID) + "\x1b[4m" + label + "\x1b[24m" + ansi.ResetHyperlink()
}

// Plain-text fallback. Balanced punctuation stays in the destination.
func linkify(value string, enabled bool) string {
	if !enabled {
		return value
	}
	return webURLPattern.ReplaceAllStringFunc(value, func(found string) string {
		end := plainWebURLEnd(found)
		if !validWebURL(found[:end]) {
			return found
		}
		return webLink(found[:end], found[:end]) + found[end:]
	})
}

func walkWebText(text string, visit func(string, int, *ansi.Parser)) {
	p := ansi.GetParser()
	defer ansi.PutParser(p)
	var state byte
	for len(text) > 0 {
		seq, width, n, next := ansi.DecodeSequence(text, state, p)
		visit(seq, width, p)
		text, state = text[n:], next
	}
}

// Give each wrapped row its own styles and validated links. Rows can be
// scrolled into view independently, and their styling must stop before padding.
func webTextLines(text string, width int, enabled bool) []string {
	lines := strings.Split(ansi.Hardwrap(text, width, true), "\n")
	var style uv.Style
	var activeLink string
	for i, line := range lines {
		var out strings.Builder
		var drawnStyle uv.Style
		var drawnLink string
		walkWebText(strings.TrimRight(line, " \t\r"), func(seq string, cells int, p *ansi.Parser) {
			switch {
			case ansi.HasCsiPrefix(seq) && p.Command() == 'm':
				uv.ReadStyle(p.Params(), &style)
			case ansi.HasOscPrefix(seq) && p.Command() == 8:
				activeLink = ""
				if enabled {
					var link uv.Link
					uv.ReadLink(p.Data(), &link)
					if validWebURL(link.URL) {
						activeLink = link.URL
					}
				}
			case cells > 0 || seq == "\t":
				if drawnLink != activeLink {
					out.WriteString(ansi.SetHyperlink(activeLink, webLinkID))
					drawnLink = activeLink
				}
				wanted := uv.Style{Attrs: style.Attrs & (uv.AttrBold | uv.AttrItalic | uv.AttrReverse)}
				if drawnLink != "" {
					wanted.Underline = uv.UnderlineSingle
				}
				out.WriteString(markdownStyleDiff(&drawnStyle, &wanted))
				drawnStyle = wanted
				out.WriteString(seq)
			}
		})
		if drawnLink != "" {
			out.WriteString(ansi.ResetHyperlink())
		}
		out.WriteString(markdownStyleDiff(&drawnStyle, &uv.Style{}))
		lines[i] = out.String()
	}
	return lines
}

func markdownStyleDiff(from, to *uv.Style) string {
	// The pane supplies foreground and background colours. Clear the inline
	// attributes individually so an SGR reset doesn't erase those colours.
	if to.IsZero() && !from.IsZero() {
		return ansi.Style{}.Normal().Italic(false).Reverse(false).Underline(false).String()
	}
	return to.Diff(from)
}

// Use the renderer's grapheme widths, including both cells of wide characters.
func webLinkAt(content string, x, y int) string {
	if x < 0 || y < 0 {
		return ""
	}
	var row, col int
	var target, params, found string
	walkWebText(content, func(seq string, width int, p *ansi.Parser) {
		if found != "" {
			return
		}
		if ansi.HasOscPrefix(seq) && p.Command() == 8 {
			fields := strings.SplitN(string(p.Data()), ";", 3)
			if len(fields) == 3 {
				params, target = fields[1], fields[2]
			}
		} else if seq == "\n" {
			row++
			col = 0
		} else if width > 0 {
			if row == y && x >= col && x < col+width && params == webLinkID && validWebURL(target) {
				found = target
			}
			col += width
		}
	})
	return found
}

type webLinkOpenedMsg struct{ err error }

func (m terminalModel) webLinkClick(msg tea.MouseMsg) (tea.Cmd, bool) {
	click, ok := msg.(tea.MouseClickMsg)
	if !ok || click.Button != tea.MouseLeft || !m.state.mouse.Load() || !localWebLinkOpening() {
		return nil, false
	}
	target := webLinkAt(m.View().Content, click.X, click.Y)
	if target == "" {
		return nil, false
	}
	return openWebLink(target), true
}

func plainWebURLEnd(found string) int {
	end := len(found)
	for end > 0 {
		candidate := found[:end]
		last := candidate[len(candidate)-1]
		if strings.ContainsRune(".,!?;:", rune(last)) ||
			last == ')' && strings.Count(candidate, ")") > strings.Count(candidate, "(") ||
			last == ']' && strings.Count(candidate, "]") > strings.Count(candidate, "[") ||
			last == '}' && strings.Count(candidate, "}") > strings.Count(candidate, "{") {
			end--
			continue
		}
		break
	}
	return end
}

// GFM's automatic URL matcher requires a dotted hostname. Turn other bare web
// URLs into explicit autolinks before rendering so even a long localhost URL
// retains its full destination when wrapped. Markdown's AST keeps links,
// reference definitions, HTML and code outside these source replacements.
func expandBareWebURLs(source string) string {
	if !strings.Contains(source, "://") {
		return source
	}
	data := []byte(source)
	root := goldmark.New(goldmark.WithExtensions(extension.GFM)).Parser().Parse(text.NewReader(data))
	type replacement struct {
		start, end int
		target     string
	}
	var replacements []replacement
	_ = ast.Walk(root, func(node ast.Node, entering bool) (ast.WalkStatus, error) {
		if !entering {
			return ast.WalkContinue, nil
		}
		switch node.Kind() {
		case ast.KindLink, ast.KindAutoLink, ast.KindImage, ast.KindCodeSpan, ast.KindCodeBlock, ast.KindFencedCodeBlock:
			return ast.WalkSkipChildren, nil
		}
		if node, ok := node.(*ast.Text); ok {
			segment := node.Segment
			value := string(segment.Value(data))
			for _, match := range webURLPattern.FindAllStringIndex(value, -1) {
				start := match[0]
				end := start + plainWebURLEnd(value[start:match[1]])
				target := value[start:end]
				if validWebURL(target) {
					replacements = append(replacements, replacement{segment.Start + start, segment.Start + end, target})
				}
			}
		}
		return ast.WalkContinue, nil
	})
	if len(replacements) == 0 {
		return source
	}
	var out strings.Builder
	previous := 0
	for _, r := range replacements {
		out.WriteString(source[previous:r.start])
		out.WriteByte('<')
		out.WriteString(r.target)
		out.WriteByte('>')
		previous = r.end
	}
	out.WriteString(source[previous:])
	return out.String()
}
