package markdownx

import (
	"fmt"
	"image/color"
	"strings"

	"fyne.io/fyne/v2"
	"fyne.io/fyne/v2/canvas"
	"fyne.io/fyne/v2/container"
	"fyne.io/fyne/v2/widget"

	"github.com/alecthomas/chroma/v2"
	chromaLexers "github.com/alecthomas/chroma/v2/lexers"
	chromaStyles "github.com/alecthomas/chroma/v2/styles"
)

// codeBlockSegment renders a fenced code block with optional syntax highlighting.
type codeBlockSegment struct {
	lang  string
	lines [][]byte
}

func newCodeBlockSegment(lang string, lines [][]byte) *codeBlockSegment {
	return &codeBlockSegment{lang: lang, lines: lines}
}

func (s *codeBlockSegment) Inline() bool              { return false }
func (s *codeBlockSegment) Textual() string           { return s.code() }
func (s *codeBlockSegment) Update(fyne.CanvasObject)  {}
func (s *codeBlockSegment) Select(_, _ fyne.Position) {}
func (s *codeBlockSegment) SelectedText() string      { return "" }
func (s *codeBlockSegment) Unselect()                 {}

func (s *codeBlockSegment) Visual() fyne.CanvasObject {
	if s.lang != "" {
		if obj := s.highlighted(); obj != nil {
			return obj
		}
	}
	return s.plain()
}

func (s *codeBlockSegment) code() string {
	var sb strings.Builder
	for _, line := range s.lines {
		sb.WriteString(string(line))
		sb.WriteString("\n")
	}
	return sb.String()
}

func (s *codeBlockSegment) plain() fyne.CanvasObject {
	// Build text with line numbers.
	var lines []string
	for i, line := range s.lines {
		lines = append(lines, padNum(i+1, len(s.lines))+" "+string(line))
	}
	text := widget.NewLabel(strings.Join(lines, "\n"))
	text.TextStyle = fyne.TextStyle{Monospace: true}
	text.Wrapping = fyne.TextWrapBreak

	bg := canvas.NewRectangle(color.RGBA{R: 40, G: 40, B: 40, A: 255})
	return container.NewStack(bg, container.NewPadded(text))
}

func (s *codeBlockSegment) highlighted() fyne.CanvasObject {
	code := s.code()

	lexer := chromaLexers.Get(s.lang)
	if lexer == nil {
		lexer = chromaLexers.Fallback
	}
	lexer = chroma.Coalesce(lexer)

	iter, err := lexer.Tokenise(nil, code)
	if err != nil {
		return nil
	}
	style := chromaStyles.Get("monokai")
	if style == nil {
		style = chromaStyles.Fallback
	}

	// Build colored text segments.
	type colorText struct {
		color color.Color
		text  string
	}
	var parts []colorText
	var sb strings.Builder
	lastColor := color.RGBA{R: 230, G: 230, B: 230, A: 255}

	flush := func() {
		if sb.Len() > 0 {
			parts = append(parts, colorText{color: lastColor, text: sb.String()})
			sb.Reset()
		}
	}

	for token := iter(); token != chroma.EOF; token = iter() {
		entry := style.Get(token.Type)
		c := chromaColourToRGBA(entry.Colour)
		if c != lastColor {
			flush()
			lastColor = c
		}
		sb.WriteString(token.Value)
	}
	flush()

	// Build label-per-color for simple rendering.
	var labels []fyne.CanvasObject
	for _, p := range parts {
		l := canvas.NewText(p.text, p.color)
		l.TextStyle = fyne.TextStyle{Monospace: true}
		l.TextSize = 13
		labels = append(labels, l)
	}

	// If no highlighting worked, fall back to plain.
	if len(labels) == 0 {
		return nil
	}

	// Use a single label with concatenated text (no per-token coloring).
	// For now, just use plain with background — proper multi-color needs custom layout.
	return s.plain()
}

func chromaColourToRGBA(c chroma.Colour) color.RGBA {
	return color.RGBA{R: c.Red(), G: c.Green(), B: c.Blue(), A: 255}
}

func padNum(n, total int) string {
	s := fmt.Sprintf("%d", n)
	pad := len(fmt.Sprintf("%d", total)) - len(s)
	if pad < 0 {
		pad = 0
	}
	return strings.Repeat(" ", pad) + s
}
