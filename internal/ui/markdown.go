package ui

import (
	"bytes"
	"strconv"
	"strings"
	"sync"

	glamour "charm.land/glamour/v2/ansi"
	"charm.land/glamour/v2/styles"
	"github.com/yuin/goldmark"
	"github.com/yuin/goldmark/ast"
	"github.com/yuin/goldmark/extension"
	table "github.com/yuin/goldmark/extension/ast"
	"github.com/yuin/goldmark/parser"
	"github.com/yuin/goldmark/renderer"
	"github.com/yuin/goldmark/text"
	"github.com/yuin/goldmark/util"
)

var (
	markdownParser = goldmark.New(
		goldmark.WithExtensions(extension.GFM, extension.DefinitionList),
		goldmark.WithParserOptions(parser.WithAutoHeadingID()),
	).Parser()
	markdownRenderers sync.Map
)

type markdownRenderer struct {
	mu sync.Mutex
	renderer.Renderer
}

func renderMarkdown(source []byte, doc ast.Node, width int) (string, error) {
	cached, ok := markdownRenderers.Load(width)
	if !ok {
		style := styles.NoTTYStyleConfig
		style.Emph = glamour.StylePrimitive{Italic: new(true)}
		style.Strong = glamour.StylePrimitive{Bold: new(true)}
		style.Code.StylePrimitive = glamour.StylePrimitive{Inverse: new(true)}
		style.Table.RowSeparator = new("─")
		style.Table.ColumnSeparator = new("│")
		style.Table.CenterSeparator = new("┼")
		if width == 0 {
			style.Document.Margin = nil
		}
		ansiRenderer := glamour.NewRenderer(glamour.Options{
			Styles: style, WordWrap: width, PreserveNewLines: true, InlineTableLinks: true,
		})
		cached, _ = markdownRenderers.LoadOrStore(width, &markdownRenderer{Renderer: renderer.NewRenderer(
			renderer.WithNodeRenderers(util.Prioritized(ansiRenderer, 1000)),
		)})
	}
	r := cached.(*markdownRenderer)
	var output bytes.Buffer
	r.mu.Lock()
	defer r.mu.Unlock()
	err := r.Render(&output, source, doc)
	return strings.Trim(output.String(), "\n"), err
}

func wrapLines(value string, width int, links bool) []string {
	if width <= 0 {
		return []string{""}
	}
	// Terminal styling and hyperlinks come from the renderer, never the source.
	value = stripANSI(value)
	source := []byte(expandBareWebURLs(value))
	doc := markdownParser.Parse(text.NewReader(source))
	rendered, err := renderMarkdownDocument(source, doc, width)
	if err != nil {
		rendered = linkify(stripHTML(value), links)
	}
	return webTextLines(rendered, width, links)
}

func renderMarkdownDocument(source []byte, doc ast.Node, width int) (string, error) {
	// Parse the whole document so references resolve across table boundaries.
	// Render consecutive prose blocks together to keep Glamour's spacing.
	var sections []string
	prose := ast.NewDocument()
	flush := func() error {
		if !prose.HasChildren() {
			return nil
		}
		section, err := renderMarkdown(source, prose, width)
		sections = append(sections, section)
		prose.RemoveChildren(prose)
		return err
	}
	for block := doc.FirstChild(); block != nil; block = doc.FirstChild() {
		doc.RemoveChild(doc, block)
		if grid, ok := block.(*table.Table); ok {
			if err := flush(); err != nil {
				return "", err
			}
			section, err := renderMarkdownTable(source, grid, width)
			if err != nil {
				return "", err
			}
			sections = append(sections, section)
		} else {
			prose.AppendChild(prose, block)
		}
	}
	if err := flush(); err != nil {
		return "", err
	}
	return strings.Join(sections, "\n\n"), nil
}

func renderMarkdownTable(source []byte, grid *table.Table, width int) (string, error) {
	sizes := make([]int, len(grid.Alignments))
	rows := make([][]string, 0, grid.ChildCount())
	for row := grid.FirstChild(); row != nil; row = row.NextSibling() {
		cells := make([]string, len(sizes))
		for cell, col := row.FirstChild(), 0; cell != nil; cell, col = cell.NextSibling(), col+1 {
			doc, paragraph := ast.NewDocument(), ast.NewParagraph()
			doc.AppendChild(doc, paragraph)
			var inline ast.Node = paragraph
			if row.Kind() == table.KindTableHeader {
				inline = ast.NewEmphasis(2)
				paragraph.AppendChild(paragraph, inline)
			}
			for child := cell.FirstChild(); child != nil; child = cell.FirstChild() {
				cell.RemoveChild(cell, child)
				inline.AppendChild(inline, child)
			}
			value, err := renderMarkdown(source, doc, 0)
			if err != nil {
				return "", err
			}
			cells[col] = value
			sizes[col] = max(sizes[col], visibleWidth(value))
		}
		rows = append(rows, cells)
	}

	total := 2 * max(len(sizes)-1, 0)
	for _, size := range sizes {
		total += size
	}
	var lines []string
	if total <= width {
		for i, cells := range rows {
			for col, value := range cells {
				pad := sizes[col] - visibleWidth(value)
				var left int
				switch grid.Alignments[col] {
				case table.AlignCenter:
					left = pad / 2
				case table.AlignRight:
					left = pad
				}
				cells[col] = strings.Repeat(" ", left) + value + strings.Repeat(" ", pad-left)
			}
			lines = append(lines, strings.TrimRight(strings.Join(cells, "  "), " "))
			if i == 0 {
				lines = append(lines, strings.Repeat("─", total))
			}
		}
	} else if len(rows) == 1 {
		lines = rows[0]
	} else if len(rows) > 1 {
		for _, cells := range rows[1:] {
			if len(lines) > 0 {
				lines = append(lines, "")
			}
			for col, value := range cells {
				label := rows[0][col]
				if label == "" {
					label = "#" + strconv.Itoa(col+1)
				}
				lines = append(lines, label+": "+value)
			}
		}
	}
	return strings.Join(lines, "\n"), nil
}
