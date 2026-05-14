package markdownx

import (
	"strings"

	"fyne.io/fyne/v2"
	"fyne.io/fyne/v2/canvas"
)

// textRun is a segment of text with a uniform style.
type textRun struct {
	text  string
	style textStyle
}

// positionedText is a canvas.Text with a fixed position.
type positionedText struct {
	obj *canvas.Text
	x   float32
	y   float32
}

// inlineLayout measures and wraps text runs into lines of positioned canvas.Text objects.
type inlineLayout struct {
	runs  []textRun
	width float32
	lines [][]positionedText
	total float32 // total height
}

// newInlineLayout creates a layout engine for the given runs and available width.
func newInlineLayout(runs []textRun, width float32) *inlineLayout {
	il := &inlineLayout{runs: runs, width: width}
	il.layout()
	return il
}

// layout performs word wrapping and positions all text objects.
func (il *inlineLayout) layout() {
	il.lines = nil
	il.total = 0

	if il.width <= 0 {
		il.width = 400
	}

	// Flatten runs into words, each word knows its source style.
	type word struct {
		text  string
		style textStyle
		width float32
	}

	var words []word
	for _, r := range il.runs {
		if r.text == "" {
			continue
		}
		// Split by whitespace but preserve spaces as part of words for proper display.
		// We split into tokens: words and spaces.
		for _, field := range splitWithSpaces(r.text) {
			if field == "" {
				continue
			}
			w := measureText(field, r.style)
			words = append(words, word{text: field, style: r.style, width: w})
		}
	}

	if len(words) == 0 {
		return
	}

	// Word wrap: accumulate words into lines.
	var line []positionedText
	lineWidth := float32(0)

	for _, w := range words {
		// If adding this word exceeds width and line is not empty, start new line.
		if lineWidth+w.width > il.width && len(line) > 0 {
			il.lines = append(il.lines, line)
			line = nil
			lineWidth = 0
		}
		t := canvas.NewText(w.text, w.style.color)
		t.TextStyle = w.style.fyneStyle()
		t.TextSize = w.style.size
		line = append(line, positionedText{obj: t, x: lineWidth})
		lineWidth += w.width
	}
	if len(line) > 0 {
		il.lines = append(il.lines, line)
	}

	// Calculate total height.
	for _, line := range il.lines {
		h := il.lineHeight(line)
		il.total += h + lineSpacing
	}
	if il.total > 0 {
		il.total -= lineSpacing // remove trailing spacing
	}
}

// lineHeight returns the max height of texts in a line.
func (il *inlineLayout) lineHeight(line []positionedText) float32 {
	maxH := float32(0)
	for _, pt := range line {
		s := fyne.MeasureText(pt.obj.Text, pt.obj.TextSize, pt.obj.TextStyle)
		if s.Height > maxH {
			maxH = s.Height
		}
	}
	return maxH
}

// render creates all canvas.Text objects positioned correctly.
// Returns a flat list of objects with absolute Y positions starting at startY.
func (il *inlineLayout) render(startY float32) []fyne.CanvasObject {
	var objects []fyne.CanvasObject
	y := startY

	for _, line := range il.lines {
		h := il.lineHeight(line)
		for _, pt := range line {
			pt.obj.Move(fyne.NewPos(pt.x, y))
			objects = append(objects, pt.obj)
		}
		y += h + lineSpacing
	}
	return objects
}

// height returns the total height of the laid out text.
func (il *inlineLayout) height() float32 {
	return il.total
}

// measureText returns the width of text with the given style.
func measureText(text string, s textStyle) float32 {
	size := fyne.MeasureText(text, s.size, s.fyneStyle())
	return size.Width
}

// splitWithSpaces splits text into tokens, keeping leading/trailing spaces
// attached to words so that word spacing is preserved in the layout.
func splitWithSpaces(text string) []string {
	if text == "" {
		return nil
	}
	// Simple approach: split by spaces but keep the space with the preceding word.
	// Exception: leading space stays with the first word.
	var tokens []string
	current := ""

	for i, r := range text {
		if r == ' ' || r == '\t' {
			current += string(r)
		} else if r == '\n' {
			if current != "" {
				tokens = append(tokens, current)
			}
			tokens = append(tokens, "\n")
			current = ""
		} else {
			if current != "" && len(current) > 0 && (current[len(current)-1] == ' ' || current[len(current)-1] == '\t') && i > 0 && text[i-1] != ' ' && text[i-1] != '\t' {
				// Previous chars were spaces, this is a new word after space.
				tokens = append(tokens, current)
				current = ""
			}
			current += string(r)
		}
	}
	if current != "" {
		tokens = append(tokens, current)
	}
	return tokens
}

// Ensure strings is imported.
var _ = strings.TrimSpace
