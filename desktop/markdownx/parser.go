package markdownx

import (
	"image/color"
	"strings"

	"fyne.io/fyne/v2"
	"fyne.io/fyne/v2/widget"

	"github.com/alecthomas/chroma/v2"
	"github.com/alecthomas/chroma/v2/lexers"
	"github.com/alecthomas/chroma/v2/styles"
	"github.com/yuin/goldmark"
	"github.com/yuin/goldmark/ast"
	"github.com/yuin/goldmark/extension"
	east "github.com/yuin/goldmark/extension/ast"
	"github.com/yuin/goldmark/text"
)

var mdEngine = goldmark.New(goldmark.WithExtensions(extension.GFM))

// ── Block data types ───────────────────────────────

type mdBlock struct {
	kind     blockKind
	content  string        // raw text (heading, paragraph)
	level    int           // heading level
	lang     string        // code block language
	lines    []string      // code block lines
	colors   []color.Color // per-line chroma colors
	ordered  bool          // list
	items    []listItem    // list items (with nesting support)
	headers  []string      // table headers
	rows     [][]string    // table rows
	children []*mdBlock    // blockquote children
	runs     []inlineRun   // inline runs (paragraph)
}

type listItem struct {
	text     string   // item text content
	children *mdBlock // nested sub-list (nil if none)
}

type blockKind int

const (
	blockHeading blockKind = iota
	blockParagraph
	blockCode
	blockList
	blockBlockquote
	blockTable
	blockHR
)

// parseBlocks converts markdown source into a list of block descriptors.
func parseBlocks(src string) []*mdBlock {
	reader := text.NewReader([]byte(src))
	doc := mdEngine.Parser().Parse(reader)
	var blocks []*mdBlock
	for child := doc.FirstChild(); child != nil; child = child.NextSibling() {
		b := nodeToBlock(child, src)
		if b != nil {
			blocks = append(blocks, b)
		}
	}
	return blocks
}

func nodeToBlock(node ast.Node, src string) *mdBlock {
	switch n := node.(type) {
	case *ast.Heading:
		return &mdBlock{kind: blockHeading, level: n.Level, content: extractText(n, src)}
	case *ast.Paragraph:
		// Collect inline segments as text runs.
		runs := collectInlineRuns(n, src)
		return &mdBlock{kind: blockParagraph, content: runsToText(runs), runs: runs}
	case *ast.FencedCodeBlock:
		return parseCodeBlock(n, src)
	case *ast.CodeBlock:
		return parsePlainCodeBlock(n, src)
	case *ast.List:
		return parseList(n, src)
	case *ast.Blockquote:
		return parseBlockquote(n, src)
	case *ast.ThematicBreak:
		return &mdBlock{kind: blockHR}
	case *east.Table:
		return parseTable(n, src)
	}
	return nil
}

// ── Inline run ─────────────────────────────────────

type inlineRun struct {
	text   string
	bold   bool
	italic bool
	code   bool
	link   string
}

func collectInlineRuns(node ast.Node, src string) []inlineRun {
	var runs []inlineRun
	for child := node.FirstChild(); child != nil; child = child.NextSibling() {
		collectInline(child, src, &runs, inlineRun{})
	}
	return runs
}

func collectInline(node ast.Node, src string, runs *[]inlineRun, style inlineRun) {
	switch n := node.(type) {
	case *ast.Text:
		text := string(n.Segment.Value([]byte(src)))
		if text != "" {
			*runs = append(*runs, inlineRun{text: text, bold: style.bold, italic: style.italic, code: style.code, link: style.link})
		}
	case *ast.String:
		*runs = append(*runs, inlineRun{text: string(n.Value), bold: style.bold, italic: style.italic, code: style.code})
	case *ast.CodeSpan:
		for child := n.FirstChild(); child != nil; child = child.NextSibling() {
			collectInline(child, src, runs, inlineRun{code: true})
		}
	case *ast.Emphasis:
		em := style
		if n.Level == 2 {
			em.bold = true
		} else {
			em.italic = true
		}
		for child := n.FirstChild(); child != nil; child = child.NextSibling() {
			collectInline(child, src, runs, em)
		}
	case *ast.Link:
		for child := n.FirstChild(); child != nil; child = child.NextSibling() {
			collectInline(child, src, runs, inlineRun{link: string(n.Destination)})
		}
	default:
		for child := node.FirstChild(); child != nil; child = child.NextSibling() {
			collectInline(child, src, runs, style)
		}
	}
}

// runsToSegments converts inline runs to Fyne RichText segments.
func runsToSegments(runs []inlineRun) []interface{} {
	var segs []interface{}
	for _, r := range runs {
		switch {
		case r.code:
			segs = append(segs, codeSpanSeg(r.text))
		case r.bold && r.italic:
			segs = append(segs, &widget.TextSegment{Text: r.text, Style: widget.RichTextStyle{
				Inline: true, TextStyle: fyne.TextStyle{Bold: true, Italic: true}}})
		case r.bold:
			segs = append(segs, boldSeg(r.text))
		case r.italic:
			segs = append(segs, italicSeg(r.text))
		case r.link != "":
			segs = append(segs, linkSeg(r.text, r.link))
		default:
			segs = append(segs, normalSeg(r.text))
		}
	}
	return segs
}

func runsToText(runs []inlineRun) string {
	var sb strings.Builder
	for _, r := range runs {
		sb.WriteString(r.text)
	}
	return sb.String()
}

// ── Code block parsing ─────────────────────────────

func parseCodeBlock(n *ast.FencedCodeBlock, src string) *mdBlock {
	lang := ""
	if n.Info != nil {
		lang = string(n.Info.Text([]byte(src)))
	}
	var lines []string
	for i := 0; i < n.Lines().Len(); i++ {
		seg := n.Lines().At(i)
		lines = append(lines, strings.TrimRight(string(seg.Value([]byte(src))), "\n"))
	}
	colors := chromaColors(lang, lines)
	return &mdBlock{kind: blockCode, lang: lang, lines: lines, colors: colors}
}

func parsePlainCodeBlock(n *ast.CodeBlock, src string) *mdBlock {
	var lines []string
	for i := 0; i < n.Lines().Len(); i++ {
		seg := n.Lines().At(i)
		lines = append(lines, strings.TrimRight(string(seg.Value([]byte(src))), "\n"))
	}
	return &mdBlock{kind: blockCode, lines: lines}
}

func chromaColors(lang string, lines []string) []color.Color {
	result := make([]color.Color, len(lines))
	if lang == "" {
		return result
	}
	lexer := lexers.Get(lang)
	if lexer == nil {
		return result
	}
	lexer = chroma.Coalesce(lexer)
	iter, err := lexer.Tokenise(nil, strings.Join(lines, "\n"))
	if err != nil {
		return result
	}
	style := styles.Get("monokai")
	if style == nil {
		style = styles.Fallback
	}
	line := 0
	for tok := iter(); tok != chroma.EOF; tok = iter() {
		entry := style.Get(tok.Type)
		c := entry.Colour
		if c.Red() > 0 || c.Green() > 0 || c.Blue() > 0 {
			rgb := color.RGBA{R: c.Red(), G: c.Green(), B: c.Blue(), A: 255}
			for i := line; i < line+strings.Count(tok.Value, "\n")+1 && i < len(result); i++ {
				if result[i] == nil {
					result[i] = rgb
				}
			}
		}
		line += strings.Count(tok.Value, "\n")
	}
	return result
}

// ── List parsing ───────────────────────────────────

func parseList(n *ast.List, src string) *mdBlock {
	lb := &mdBlock{kind: blockList, ordered: n.IsOrdered()}
	for child := n.FirstChild(); child != nil; child = child.NextSibling() {
		li, ok := child.(*ast.ListItem)
		if !ok {
			continue
		}
		item := listItem{}
		for lc := li.FirstChild(); lc != nil; lc = lc.NextSibling() {
			switch v := lc.(type) {
			case *ast.Paragraph:
				// Direct text content of this list item.
				item.text = extractText(v, src)
			case *ast.List:
				// Nested sub-list.
				sub := parseList(v, src)
				item.children = sub
			case *ast.TextBlock:
				// Some markdown parsers use TextBlock instead of Paragraph.
				item.text = extractText(v, src)
			default:
				// Fallback: try to extract text.
				if item.text == "" {
					item.text = strings.TrimSpace(extractText(lc, src))
				}
			}
		}
		lb.items = append(lb.items, item)
	}
	return lb
}

// ── Blockquote parsing ─────────────────────────────

func parseBlockquote(n *ast.Blockquote, src string) *mdBlock {
	bq := &mdBlock{kind: blockBlockquote}
	for child := n.FirstChild(); child != nil; child = child.NextSibling() {
		b := nodeToBlock(child, src)
		if b != nil {
			bq.children = append(bq.children, b)
		}
	}
	return bq
}

// ── Table parsing ──────────────────────────────────

func parseTable(n *east.Table, src string) *mdBlock {
	tb := &mdBlock{kind: blockTable}
	for child := n.FirstChild(); child != nil; child = child.NextSibling() {
		switch row := child.(type) {
		case *east.TableHeader:
			tb.headers = extractCells(row, src)
		case *east.TableRow:
			tb.rows = append(tb.rows, extractCells(row, src))
		}
	}
	return tb
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

// ── Text extraction ────────────────────────────────

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

// closeOpenCodeBlocks ensures all ``` have matching closing ```.
func closeOpenCodeBlocks(text string) string {
	if strings.Count(text, "```")%2 != 0 {
		return text + "\n```"
	}
	return text
}
