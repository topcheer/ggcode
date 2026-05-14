package markdownx

import (
	"fmt"
	"image/color"
	"strings"

	"fyne.io/fyne/v2"
	"fyne.io/fyne/v2/canvas"
	"fyne.io/fyne/v2/container"
	"fyne.io/fyne/v2/layout"

	"github.com/alecthomas/chroma/v2"
	"github.com/alecthomas/chroma/v2/lexers"
	"github.com/alecthomas/chroma/v2/styles"
)

// newCodeBlock creates a styled code block with dark background and line numbers.
func newCodeBlock(lang string, lines []string) fyne.CanvasObject {
	if len(lines) == 0 {
		return nil
	}

	numWidth := len(itoa(len(lines)))

	// Try to get highlighted colors for the whole block.
	lineColors := chromaLineColors(lang, lines)

	var lineObjs []fyne.CanvasObject
	for i, line := range lines {
		// Line number.
		num := canvas.NewText(fmt.Sprintf("%*d ", numWidth, i+1), color.RGBA{R: 100, G: 100, B: 100, A: 255})
		num.TextStyle = fyne.TextStyle{Monospace: true}
		num.TextSize = 13

		// Code text.
		codeText := canvas.NewText(line, color.RGBA{R: 220, G: 220, B: 220, A: 255})
		codeText.TextStyle = fyne.TextStyle{Monospace: true}
		codeText.TextSize = 13
		if i < len(lineColors) && lineColors[i] != nil {
			codeText.Color = lineColors[i]
		}

		row := container.NewHBox(num, codeText)
		lineObjs = append(lineObjs, row)
	}

	codeVBox := container.NewVBox(lineObjs...)
	bg := canvas.NewRectangle(color.RGBA{R: 30, G: 30, B: 30, A: 255})
	padded := container.NewPadded(codeVBox)

	content := container.NewStack(bg, padded)
	return container.New(layout.NewCustomPaddedLayout(4, 4, 8, 8), content)
}

// chromaLineColors returns per-line dominant colors using chroma highlighting.
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
	lastColor := color.Color(color.RGBA{R: 230, G: 230, B: 230, A: 255}) // default
	for tok := iter(); tok != chroma.EOF; tok = iter() {
		entry := style.Get(tok.Type)
		if entry.Colour.Red() > 0 || entry.Colour.Green() > 0 || entry.Colour.Blue() > 0 {
			lastColor = color.RGBA{R: entry.Colour.Red(), G: entry.Colour.Green(), B: entry.Colour.Blue(), A: 255}
		}
		newlines := strings.Count(tok.Value, "\n")
		for i := currentLine; i <= currentLine+newlines && i < len(result); i++ {
			if result[i] == nil {
				result[i] = lastColor
			}
		}
		currentLine += newlines
	}
	return result
}

// Ensure styles import is used.
var _ = styles.Fallback
