package markdownx

import (
	"image/color"
	"strings"

	"fyne.io/fyne/v2"
	"fyne.io/fyne/v2/canvas"

	"github.com/alecthomas/chroma/v2"
	"github.com/alecthomas/chroma/v2/lexers"
	"github.com/alecthomas/chroma/v2/styles"
	"github.com/yuin/goldmark"
	"github.com/yuin/goldmark/ast"
	east "github.com/yuin/goldmark/extension/ast"
	"github.com/yuin/goldmark/extension"
	"github.com/yuin/goldmark/text"
)

var mdEngine = goldmark.New(goldmark.WithExtensions(extension.GFM))

// parseBlocks parses markdown source into a list of blocks.
func parseBlocks(src string) []block {
	reader := text.NewReader([]byte(src))
	doc := mdEngine.Parser().Parse(reader)

	var blocks []block
	for child := doc.FirstChild(); child != nil; child = child.NextSibling() {
		b := astToBlock(child, src)
		if b != nil {
			blocks = append(blocks, b)
		}
	}
	return blocks
}

func astToBlock(node ast.Node, src string) block {
	switch n := node.(type) {
	case *ast.Heading:
		return &headingBlock{level: n.Level, runs: collectInlineRuns(n, src)}
	case *ast.Paragraph:
		return &paragraphBlock{runs: collectInlineRuns(n, src)}
	case *ast.FencedCodeBlock:
		return parseCodeBlock(n, src)
	case *ast.CodeBlock:
		return parsePlainCodeBlock(n, src)
	case *ast.List:
		return parseList(n, src)
	case *ast.Blockquote:
		return parseBlockquote(n, src)
	case *ast.ThematicBreak:
		return &hrBlock{}
	case *east.Table:
		return parseTable(n, src)
	}
	return nil
}

// ── Code block parsing ─────────────────────────────

func parseCodeBlock(n *ast.FencedCodeBlock, src string) *codeBlock {
	lang := ""
	if n.Info != nil {
		lang = string(n.Info.Text([]byte(src)))
	}
	var lines []string
	for i := 0; i < n.Lines().Len(); i++ {
		seg := n.Lines().At(i)
		line := string(seg.Value([]byte(src)))
		lines = append(lines, strings.TrimRight(line, "\n"))
	}
	colors := chromaLineColors(lang, lines)
	return &codeBlock{lang: lang, lines: lines, color: colors}
}

func parsePlainCodeBlock(n *ast.CodeBlock, src string) *codeBlock {
	var lines []string
	for i := 0; i < n.Lines().Len(); i++ {
		seg := n.Lines().At(i)
		line := string(seg.Value([]byte(src)))
		lines = append(lines, strings.TrimRight(line, "\n"))
	}
	return &codeBlock{lines: lines}
}

// chromaLineColors returns per-line dominant syntax colors.
func chromaLineColors(lang string, lines []string) []color.Color {
	result := make([]color.Color, len(lines))
	if lang == "" {
		return result
	}

	lexer := lexers.Get(lang)
	if lexer == nil {
		return result
	}
	lexer = chroma.Coalesce(lexer)

	code := strings.Join(lines, "\n")
	iter, err := lexer.Tokenise(nil, code)
	if err != nil {
		return result
	}

	style := styles.Get("monokai")
	if style == nil {
		style = styles.Fallback
	}

	currentLine := 0
	for tok := iter(); tok != chroma.EOF; tok = iter() {
		entry := style.Get(tok.Type)
		c := entry.Colour
		if c.Red() > 0 || c.Green() > 0 || c.Blue() > 0 {
			lastColor := color.RGBA{R: c.Red(), G: c.Green(), B: c.Blue(), A: 255}
			newlines := strings.Count(tok.Value, "\n")
			for i := currentLine; i <= currentLine+newlines && i < len(result); i++ {
				if result[i] == nil {
					result[i] = lastColor
				}
			}
		}
		currentLine += strings.Count(tok.Value, "\n")
	}
	return result
}

// ── List parsing ───────────────────────────────────

func parseList(n *ast.List, src string) *listBlock {
	lb := &listBlock{ordered: n.IsOrdered()}
	for child := n.FirstChild(); child != nil; child = child.NextSibling() {
		li, ok := child.(*ast.ListItem)
		if !ok {
			continue
		}
		item := listItem{}
		for lc := li.FirstChild(); lc != nil; lc = lc.NextSibling() {
			if para, ok := lc.(*ast.Paragraph); ok {
				item.runs = collectInlineRuns(para, src)
			} else {
				b := astToBlock(lc, src)
				if b != nil {
					item.children = append(item.children, b)
				}
			}
		}
		lb.items = append(lb.items, item)
	}
	return lb
}

// ── Blockquote parsing ─────────────────────────────

func parseBlockquote(n *ast.Blockquote, src string) *blockquoteBlock {
	var children []block
	for child := n.FirstChild(); child != nil; child = child.NextSibling() {
		b := astToBlock(child, src)
		if b != nil {
			children = append(children, b)
		}
	}
	return &blockquoteBlock{children: children}
}

// ── Table parsing ──────────────────────────────────

func parseTable(n *east.Table, src string) *tableBlock {
	tb := &tableBlock{}
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

// ── Inline run collection ──────────────────────────

// collectInlineRuns extracts styled text runs from an inline container.
func collectInlineRuns(node ast.Node, src string) []textRun {
	var runs []textRun
	for child := node.FirstChild(); child != nil; child = child.NextSibling() {
		collectInline(child, src, &runs, normalStyle())
	}
	return runs
}

func collectInline(node ast.Node, src string, runs *[]textRun, s textStyle) {
	switch n := node.(type) {
	case *ast.Text:
		text := string(n.Segment.Value([]byte(src)))
		if text != "" {
			*runs = append(*runs, textRun{text: text, style: s})
		}
	case *ast.String:
		*runs = append(*runs, textRun{text: string(n.Value), style: s})
	case *ast.CodeSpan:
		for child := n.FirstChild(); child != nil; child = child.NextSibling() {
			collectInline(child, src, runs, codeSpanStyle())
		}
	case *ast.Emphasis:
		em := s
		if n.Level == 2 {
			em = boldStyle()
			if s.italic {
				em = textStyle{bold: true, italic: true, size: textSize, color: colFgBold}
			}
		} else {
			em = italicStyle()
			if s.bold {
				em = textStyle{bold: true, italic: true, size: textSize, color: colFgBold}
			}
		}
		for child := n.FirstChild(); child != nil; child = child.NextSibling() {
			collectInline(child, src, runs, em)
		}
	case *ast.Link:
		linkText := extractText(n, src)
		if linkText != "" {
			*runs = append(*runs, textRun{text: linkText, style: textStyle{size: textSize, color: color.RGBA{R: 100, G: 180, B: 255, A: 255}}})
		}
	case *ast.Image:
		alt := extractText(n, src)
		if alt == "" {
			alt = string(n.Destination)
		}
		*runs = append(*runs, textRun{text: "[🖼 " + alt + "]", style: s})
	default:
		for child := node.FirstChild(); child != nil; child = child.NextSibling() {
			collectInline(child, src, runs, s)
		}
	}
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
	count := strings.Count(text, "```")
	if count%2 == 0 {
		return text
	}
	return text + "\n```"
}

// Ensure imports used.
var _ = canvas.NewLine
var _ = fyne.MeasureText
