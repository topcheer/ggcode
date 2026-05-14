package markdownx

import (
	"net/url"
	"strings"

	"fyne.io/fyne/v2"
	"fyne.io/fyne/v2/theme"
	"fyne.io/fyne/v2/widget"
	"github.com/yuin/goldmark"
	"github.com/yuin/goldmark/ast"
	"github.com/yuin/goldmark/extension"
	east "github.com/yuin/goldmark/extension/ast"
	"github.com/yuin/goldmark/text"
)

// mdInstance is a reusable goldmark instance with GFM extensions.
var mdInstance = goldmark.New(
	goldmark.WithExtensions(extension.GFM),
)

// parseMarkdown converts markdown text into Fyne RichText segments.
func parseMarkdown(src string, r *nodeRenderers) []widget.RichTextSegment {
	reader := text.NewReader([]byte(src))
	doc := mdInstance.Parser().Parse(reader)

	var segments []widget.RichTextSegment
	walkAST(doc, src, r, &segments, defaultStyle())
	return segments
}

// defaultStyle returns the default text style context.
func defaultStyle() styleCtx {
	return styleCtx{}
}

type styleCtx struct {
	bold   bool
	italic bool
	code   bool
	link   string
}

// walkAST traverses goldmark AST nodes and builds Fyne segments.
func walkAST(node ast.Node, src string, r *nodeRenderers, out *[]widget.RichTextSegment, style styleCtx) {
	for child := node.FirstChild(); child != nil; child = child.NextSibling() {
		switch n := child.(type) {
		case *ast.Heading:
			r.renderHeading(n, src, out, n.Level)
		case *ast.Paragraph:
			r.renderParagraph(n, src, out, style)
		case *ast.FencedCodeBlock:
			r.renderFencedCodeBlock(n, src, out)
		case *ast.CodeBlock:
			r.renderCodeBlock(n, src, out)
		case *ast.List:
			r.renderList(n, src, out)
		case *ast.Blockquote:
			r.renderBlockquote(n, src, out)
		case *ast.ThematicBreak:
			*out = append(*out, &widget.SeparatorSegment{})
		case *east.Table:
			r.renderTable(n, src, out)
		default:
			// Fallback: render inline text.
			text := extractText(child, src)
			if text != "" {
				*out = append(*out, textSegment(text, style))
			}
		}
	}
}

// extractText recursively extracts plain text from a node.
func extractText(node ast.Node, src string) string {
	var sb strings.Builder
	extractTextInto(node, src, &sb)
	return sb.String()
}

func extractTextInto(node ast.Node, src string, sb *strings.Builder) {
	if node.Type() == ast.TypeBlock {
		for child := node.FirstChild(); child != nil; child = child.NextSibling() {
			extractTextInto(child, src, sb)
		}
		return
	}

	switch n := node.(type) {
	case *ast.Text:
		sb.WriteString(string(n.Segment.Value([]byte(src))))
		if n.SoftLineBreak() {
			sb.WriteString("\n")
		}
		if n.HardLineBreak() {
			sb.WriteString("\n")
		}
	case *ast.String:
		sb.WriteString(string(n.Value))
	case *ast.CodeSpan:
		for child := n.FirstChild(); child != nil; child = child.NextSibling() {
			extractTextInto(child, src, sb)
		}
	default:
		for child := node.FirstChild(); child != nil; child = child.NextSibling() {
			extractTextInto(child, src, sb)
		}
	}
}

// renderInline renders inline elements.
func renderInline(node ast.Node, src string, out *[]widget.RichTextSegment, style styleCtx) {
	switch n := node.(type) {
	case *ast.Text:
		text := string(n.Segment.Value([]byte(src)))
		if text == "" {
			return
		}
		*out = append(*out, textSegment(text, style))
		if n.SoftLineBreak() {
			*out = append(*out, textSegment("\n", style))
		}
		if n.HardLineBreak() {
			*out = append(*out, textSegment("\n", style))
		}
	case *ast.String:
		*out = append(*out, textSegment(string(n.Value), style))
	case *ast.CodeSpan:
		codeStyle := style
		codeStyle.code = true
		for child := n.FirstChild(); child != nil; child = child.NextSibling() {
			renderInline(child, src, out, codeStyle)
		}
	case *ast.Emphasis:
		emStyle := style
		if n.Level == 2 {
			emStyle.bold = true
		} else {
			emStyle.italic = true
		}
		for child := n.FirstChild(); child != nil; child = child.NextSibling() {
			renderInline(child, src, out, emStyle)
		}
	case *ast.Link:
		linkText := extractText(n, src)
		if linkText != "" {
			*out = append(*out, &widget.HyperlinkSegment{
				Text: linkText,
				URL:  parseURL(string(n.Destination)),
			})
		}
	case *ast.AutoLink:
		url := string(n.URL([]byte(src)))
		*out = append(*out, &widget.HyperlinkSegment{
			Text: url,
			URL:  parseURL(url),
		})
	case *ast.Image:
		alt := extractText(n, src)
		if alt == "" {
			alt = string(n.Destination)
		}
		*out = append(*out, textSegment("[Image: "+alt+"]", style))
	default:
		for child := node.FirstChild(); child != nil; child = child.NextSibling() {
			renderInline(child, src, out, style)
		}
	}
}

func parseURL(s string) *url.URL {
	u, err := url.Parse(s)
	if err != nil {
		return nil
	}
	return u
}

// textSegment creates a TextSegment with the given style.
func textSegment(text string, s styleCtx) *widget.TextSegment {
	ts := &widget.TextSegment{
		Text: text,
		Style: widget.RichTextStyle{
			TextStyle: fyne.TextStyle{
				Bold:      s.bold,
				Italic:    s.italic,
				Monospace: s.code,
			},
		},
	}
	if s.code {
		ts.Style.Inline = true
	}
	return ts
}

// headingSize returns text size for heading level.
func headingSize(level int) float32 {
	sizes := map[int]float32{
		1: 24, 2: 20, 3: 18, 4: 16, 5: 14, 6: 13,
	}
	if s, ok := sizes[level]; ok {
		return s
	}
	return theme.TextSize()
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
