package markdownx

import (
	"strings"

	"fyne.io/fyne/v2/widget"
	"github.com/yuin/goldmark/ast"
	east "github.com/yuin/goldmark/extension/ast"
)

// nodeRenderers renders different AST node types to Fyne segments.
type nodeRenderers struct{}

func newNodeRenderers() *nodeRenderers {
	return &nodeRenderers{}
}

// ── Heading ─────────────────────────────────────────

func (r *nodeRenderers) renderHeading(n *ast.Heading, src string, out *[]widget.RichTextSegment, level int) {
	var segs []widget.RichTextSegment
	for child := n.FirstChild(); child != nil; child = child.NextSibling() {
		renderInline(child, src, &segs, styleCtx{})
	}
	sz := headingSize(level)
	for i := range segs {
		if ts, ok := segs[i].(*widget.TextSegment); ok {
			ts.Style.TextStyle.Bold = true
			// TextSegment doesn't support per-segment size directly.
			// We store it via the parent paragraph concept.
			_ = sz
		}
	}
	*out = append(*out, &widget.ParagraphSegment{Texts: segs})
}

// ── Paragraph ───────────────────────────────────────

func (r *nodeRenderers) renderParagraph(n *ast.Paragraph, src string, out *[]widget.RichTextSegment, style styleCtx) {
	var segs []widget.RichTextSegment
	for child := n.FirstChild(); child != nil; child = child.NextSibling() {
		renderInline(child, src, &segs, style)
	}
	if len(segs) > 0 {
		*out = append(*out, &widget.ParagraphSegment{Texts: segs})
	}
}

// ── List ────────────────────────────────────────────

func (r *nodeRenderers) renderList(n *ast.List, src string, out *[]widget.RichTextSegment) {
	items := make([][]widget.RichTextSegment, 0)

	for child := n.FirstChild(); child != nil; child = child.NextSibling() {
		if listItem, ok := child.(*ast.ListItem); ok {
			var itemSegs []widget.RichTextSegment
			for lc := listItem.FirstChild(); lc != nil; lc = lc.NextSibling() {
				if para, ok := lc.(*ast.Paragraph); ok {
					var paraSegs []widget.RichTextSegment
					for inline := para.FirstChild(); inline != nil; inline = inline.NextSibling() {
						renderInline(inline, src, &paraSegs, styleCtx{})
					}
					itemSegs = append(itemSegs, paraSegs...)
				} else {
					walkAST(lc, src, r, &itemSegs, defaultStyle())
				}
			}
			items = append(items, itemSegs)
		}
	}

	ordered := n.IsOrdered()
	// ListSegment.Items is flat []RichTextSegment, each item is a ParagraphSegment.
	var flatItems []widget.RichTextSegment
	for _, itemSegs := range items {
		flatItems = append(flatItems, &widget.ParagraphSegment{Texts: itemSegs})
	}
	ls := &widget.ListSegment{Ordered: ordered, Items: flatItems}
	*out = append(*out, ls)
}

// ── CodeBlock ───────────────────────────────────────

func (r *nodeRenderers) renderFencedCodeBlock(n *ast.FencedCodeBlock, src string, out *[]widget.RichTextSegment) {
	lang := ""
	if n.Info != nil {
		lang = string(n.Info.Text([]byte(src)))
	}
	lines := extractCodeLines(n, src)
	*out = append(*out, newCodeBlockSegment(lang, lines))
}

func (r *nodeRenderers) renderCodeBlock(n *ast.CodeBlock, src string, out *[]widget.RichTextSegment) {
	text := extractText(n, src)
	lines := splitLines(text)
	*out = append(*out, newCodeBlockSegment("", lines))
}

// ── Blockquote ──────────────────────────────────────

func (r *nodeRenderers) renderBlockquote(n *ast.Blockquote, src string, out *[]widget.RichTextSegment) {
	*out = append(*out, newBlockquoteSegment(n, src, r))
}

// ── Table ───────────────────────────────────────────

func (r *nodeRenderers) renderTable(n *east.Table, src string, out *[]widget.RichTextSegment) {
	*out = append(*out, newTableSegment(n, src, r))
}

// ── Helpers ─────────────────────────────────────────

func extractCodeLines(fcb *ast.FencedCodeBlock, src string) [][]byte {
	source := []byte(src)
	var lines [][]byte
	for i := 0; i < fcb.Lines().Len(); i++ {
		seg := fcb.Lines().At(i)
		line := seg.Value(source)
		if len(line) > 0 && line[len(line)-1] == '\n' {
			line = line[:len(line)-1]
		}
		lines = append(lines, line)
	}
	return lines
}

func splitLines(text string) [][]byte {
	var lines [][]byte
	for _, line := range strings.Split(text, "\n") {
		lines = append(lines, []byte(line))
	}
	return lines
}
