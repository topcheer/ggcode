package markdownx

import (
	"net/url"
	"strings"

	"fyne.io/fyne/v2"
	"image/color"

	"fyne.io/fyne/v2/canvas"
	"fyne.io/fyne/v2/container"
	"fyne.io/fyne/v2/layout"
	"fyne.io/fyne/v2/theme"
	"fyne.io/fyne/v2/widget"

	"github.com/yuin/goldmark"
	"github.com/yuin/goldmark/ast"
	east "github.com/yuin/goldmark/extension/ast"
	"github.com/yuin/goldmark/extension"
	"github.com/yuin/goldmark/text"
)

var mdEngine = goldmark.New(goldmark.WithExtensions(extension.GFM))

// renderMarkdown parses markdown and returns a list of CanvasObjects, one per block.
func renderMarkdown(src string, r *nodeRenderers) []fyne.CanvasObject {
	reader := text.NewReader([]byte(src))
	doc := mdEngine.Parser().Parse(reader)

	var objects []fyne.CanvasObject
	for child := doc.FirstChild(); child != nil; child = child.NextSibling() {
		obj := r.renderNode(child, src)
		if obj != nil {
			objects = append(objects, obj)
		}
	}
	return objects
}

// ── nodeRenderers ──────────────────────────────────

type nodeRenderers struct{}

func newNodeRenderers() *nodeRenderers { return &nodeRenderers{} }

func (r *nodeRenderers) renderNode(node ast.Node, src string) fyne.CanvasObject {
	switch n := node.(type) {
	case *ast.Heading:
		return r.renderHeading(n, src)
	case *ast.Paragraph:
		return r.renderParagraph(n, src)
	case *ast.FencedCodeBlock:
		return r.renderCodeBlock(n, src)
	case *ast.CodeBlock:
		return r.renderCodeBlockIndented(n, src)
	case *ast.List:
		return r.renderList(n, src)
	case *ast.Blockquote:
		return r.renderBlockquote(n, src)
	case *ast.ThematicBreak:
		return r.renderThematicBreak()
	case *east.Table:
		return r.renderTable(n, src)
	default:
		text := extractText(node, src)
		if text != "" {
			return newTextLabel(text, 0, false, false)
		}
		return nil
	}
}

// ── Heading ────────────────────────────────────────

func (r *nodeRenderers) renderHeading(n *ast.Heading, src string) fyne.CanvasObject {
	inline := r.renderInlineChildren(n, src)
	if len(inline) == 0 {
		return nil
	}
	rt := widget.NewRichText(inline...)
	rt.Wrapping = fyne.TextWrapWord
	return rt
}

// ── Paragraph ──────────────────────────────────────

func (r *nodeRenderers) renderParagraph(n *ast.Paragraph, src string) fyne.CanvasObject {
	inline := r.renderInlineChildren(n, src)
	if len(inline) == 0 {
		return nil
	}
	rt := widget.NewRichText(inline...)
	rt.Wrapping = fyne.TextWrapWord
	return rt
}

// ── Code Block ─────────────────────────────────────

func (r *nodeRenderers) renderCodeBlock(n *ast.FencedCodeBlock, src string) fyne.CanvasObject {
	lang := ""
	if n.Info != nil {
		lang = string(n.Info.Text([]byte(src)))
	}
	var lines []string
	for i := 0; i < n.Lines().Len(); i++ {
		line := func() string { s := n.Lines().At(i); return string(s.Value([]byte(src))) }()
		lines = append(lines, strings.TrimRight(line, "\n"))
	}
	return newCodeBlock(lang, lines)
}

func (r *nodeRenderers) renderCodeBlockIndented(n *ast.CodeBlock, src string) fyne.CanvasObject {
	var lines []string
	for i := 0; i < n.Lines().Len(); i++ {
		line := func() string { s := n.Lines().At(i); return string(s.Value([]byte(src))) }()
		lines = append(lines, strings.TrimRight(line, "\n"))
	}
	return newCodeBlock("", lines)
}

// ── List ───────────────────────────────────────────

func (r *nodeRenderers) renderList(n *ast.List, src string) fyne.CanvasObject {
	ordered := n.IsOrdered()
	var items []fyne.CanvasObject
	idx := 0
	for child := n.FirstChild(); child != nil; child = child.NextSibling() {
		li, ok := child.(*ast.ListItem)
		if !ok {
			continue
		}
		var contentObjs []fyne.CanvasObject
		for lc := li.FirstChild(); lc != nil; lc = lc.NextSibling() {
			obj := r.renderNode(lc, src)
			if obj != nil {
				contentObjs = append(contentObjs, obj)
			}
		}
		prefix := "• "
		if ordered {
			idx++
			prefix = numPrefix(idx) + ". "
		}
		bullet := canvas.NewText(prefix, color.RGBA{R: 200, G: 200, B: 200, A: 255})
		bullet.TextStyle = fyne.TextStyle{Bold: true}
		content := container.NewVBox(contentObjs...)
		row := container.NewHBox(bullet, content)
		items = append(items, row)
	}
	return container.NewVBox(items...)
}

func numPrefix(n int) string { return strings.TrimSpace(strings.Repeat(" ", 0) + itoa(n)) }
func itoa(n int) string {
	if n < 10 {
		return string(rune('0' + n))
	}
	return itoa(n/10) + string(rune('0'+n%10))
}

// ── Blockquote ─────────────────────────────────────

func (r *nodeRenderers) renderBlockquote(n *ast.Blockquote, src string) fyne.CanvasObject {
	var content []fyne.CanvasObject
	for child := n.FirstChild(); child != nil; child = child.NextSibling() {
		obj := r.renderNode(child, src)
		if obj != nil {
			content = append(content, obj)
		}
	}
	return newBlockquote(content)
}

// ── Thematic Break ─────────────────────────────────

func (r *nodeRenderers) renderThematicBreak() fyne.CanvasObject {
	line := canvas.NewLine(color.RGBA{R: 80, G: 80, B: 80, A: 255})
	line.StrokeWidth = 1
	return container.NewPadded(line)
}

// ── Table ──────────────────────────────────────────

func (r *nodeRenderers) renderTable(n *east.Table, src string) fyne.CanvasObject {
	var headers []string
	var rows [][]string
	for child := n.FirstChild(); child != nil; child = child.NextSibling() {
		switch row := child.(type) {
		case *east.TableHeader:
			headers = extractCells(row, src)
		case *east.TableRow:
			rows = append(rows, extractCells(row, src))
		}
	}
	return newTable(headers, rows)
}

func extractCells(node ast.Node, src string) []string {
	var cells []string
	for child := node.FirstChild(); child != nil; child = child.NextSibling() {
		if cell, ok := child.(*east.TableCell); ok {
			cells = append(cells, strings.TrimSpace(extractText(cell, src)))
		}
	}
	return cells
}

// ── Inline rendering ───────────────────────────────

// inlineStyle holds style context for inline traversal.
type inlineStyle struct {
	bold   bool
	italic bool
	code   bool
	link   string
}

// renderInlineChildren renders all inline children of a block node into RichText segments.
func (r *nodeRenderers) renderInlineChildren(node ast.Node, src string) []widget.RichTextSegment {
	var segs []widget.RichTextSegment
	for child := node.FirstChild(); child != nil; child = child.NextSibling() {
		r.renderInline(child, src, &segs, inlineStyle{})
	}
	return segs
}

func (r *nodeRenderers) renderInline(node ast.Node, src string, out *[]widget.RichTextSegment, s inlineStyle) {
	switch n := node.(type) {
	case *ast.Text:
		text := string(n.Segment.Value([]byte(src)))
		if text == "" {
			return
		}
		*out = append(*out, makeTextSeg(text, s))
		if n.SoftLineBreak() {
			*out = append(*out, makeTextSeg("\n", s))
		}
		if n.HardLineBreak() {
			*out = append(*out, makeTextSeg("\n", s))
		}
	case *ast.String:
		*out = append(*out, makeTextSeg(string(n.Value), s))
	case *ast.CodeSpan:
		for child := n.FirstChild(); child != nil; child = child.NextSibling() {
			r.renderInline(child, src, out, inlineStyle{code: true})
		}
	case *ast.Emphasis:
		em := inlineStyle{bold: s.bold, italic: s.italic, code: s.code, link: s.link}
		if n.Level == 2 {
			em.bold = true
		} else {
			em.italic = true
		}
		for child := n.FirstChild(); child != nil; child = child.NextSibling() {
			r.renderInline(child, src, out, em)
		}
	case *ast.Link:
		linkText := extractText(n, src)
		if linkText != "" {
			u, _ := url.Parse(string(n.Destination))
			if u != nil {
				*out = append(*out, &widget.HyperlinkSegment{Text: linkText, URL: u})
			} else {
				*out = append(*out, makeTextSeg(linkText, s))
			}
		}
	case *ast.AutoLink:
		linkURL := string(n.URL([]byte(src)))
		u, _ := url.Parse(linkURL)
		if u != nil {
			*out = append(*out, &widget.HyperlinkSegment{Text: linkURL, URL: u})
		}
	case *ast.Image:
		alt := extractText(n, src)
		if alt == "" {
			alt = string(n.Destination)
		}
		*out = append(*out, makeTextSeg("[🖼 "+alt+"]", s))
	default:
		for child := node.FirstChild(); child != nil; child = child.NextSibling() {
			r.renderInline(child, src, out, s)
		}
	}
}

// makeTextSeg creates a properly styled TextSegment.
func makeTextSeg(text string, s inlineStyle) *widget.TextSegment {
	ts := &widget.TextSegment{Text: text}
	sty := &ts.Style

	sty.Inline = true

	if s.code {
		sty.TextStyle = fyne.TextStyle{Monospace: true}
		sty.ColorName = theme.ColorNamePrimary
		return ts
	}

	sty.TextStyle = fyne.TextStyle{
		Bold:   s.bold,
		Italic: s.italic,
	}
	return ts
}

// ── Helper functions ───────────────────────────────

// extractText recursively extracts plain text from a node.
func extractText(node ast.Node, src string) string {
	var sb strings.Builder
	extractTextInto(node, src, &sb)
	return sb.String()
}

func extractTextInto(node ast.Node, src string, sb *strings.Builder) {
	switch n := node.(type) {
	case *ast.Text:
		sb.WriteString(string(n.Segment.Value([]byte(src))))
	case *ast.String:
		sb.WriteString(string(n.Value))
	default:
		for child := node.FirstChild(); child != nil; child = child.NextSibling() {
			extractTextInto(child, src, sb)
		}
	}
}

// newTextLabel creates a simple text label.
func newTextLabel(text string, size float32, bold, monospace bool) *widget.Label {
	l := widget.NewLabel(text)
	l.Wrapping = fyne.TextWrapWord
	if bold {
		l.TextStyle = fyne.TextStyle{Bold: true}
	}
	if monospace {
		l.TextStyle = fyne.TextStyle{Monospace: true}
	}
	return l
}

// closeOpenCodeBlocks ensures all ``` have matching closing ```.
func closeOpenCodeBlocks(text string) string {
	count := strings.Count(text, "```")
	if count%2 == 0 {
		return text
	}
	return text + "\n```"
}

// runeWidth returns display width of string (CJK chars = 2).
func runeWidth(s string) int {
	w := 0
	for _, r := range s {
		if isCJK(r) {
			w += 2
		} else {
			w++
		}
	}
	return w
}

func isCJK(r rune) bool {
	return (r >= 0x4E00 && r <= 0x9FFF) ||
		(r >= 0x3400 && r <= 0x4DBF) ||
		(r >= 0x2E80 && r <= 0x2FDF) ||
		(r >= 0xF900 && r <= 0xFAFF) ||
		(r >= 0x3000 && r <= 0x303F) ||
		(r >= 0x3040 && r <= 0x309F) ||
		(r >= 0x30A0 && r <= 0x30FF) ||
		(r >= 0xAC00 && r <= 0xD7AF) ||
		(r >= 0x1100 && r <= 0x11FF) ||
		(r >= 0xFF01 && r <= 0xFF60)
}

// Ensure layout import is used.
var _ = layout.NewSpacer
