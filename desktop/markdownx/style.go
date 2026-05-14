package markdownx

import (
	"image/color"

	"fyne.io/fyne/v2"
	"fyne.io/fyne/v2/theme"
	"fyne.io/fyne/v2/widget"
)

// ── Block element styles ───────────────────────────

// headingSegments returns RichText segments for a heading.
func headingSegments(text string, level int) []widget.RichTextSegment {
	sty := widget.RichTextStyle{
		Inline:    false,
		TextStyle: fyne.TextStyle{Bold: true},
	}
	switch level {
	case 1:
		sty.SizeName = theme.SizeNameHeadingText
	case 2:
		sty.SizeName = theme.SizeNameSubHeadingText
	default:
		sty.SizeName = theme.SizeNameText
	}
	return []widget.RichTextSegment{&widget.TextSegment{Text: text, Style: sty}}
}

// ── Inline element styles ──────────────────────────

func normalSeg(text string) *widget.TextSegment {
	return &widget.TextSegment{Text: text, Style: widget.RichTextStyle{
		Inline: true,
	}}
}

func boldSeg(text string) *widget.TextSegment {
	return &widget.TextSegment{Text: text, Style: widget.RichTextStyle{
		Inline:    true,
		TextStyle: fyne.TextStyle{Bold: true},
	}}
}

func italicSeg(text string) *widget.TextSegment {
	return &widget.TextSegment{Text: text, Style: widget.RichTextStyle{
		Inline:    true,
		TextStyle: fyne.TextStyle{Italic: true},
	}}
}

func codeSpanSeg(text string) *widget.TextSegment {
	return &widget.TextSegment{Text: text, Style: widget.RichTextStyleCodeInline}
}

func linkSeg(text, url string) *widget.HyperlinkSegment {
	return &widget.HyperlinkSegment{Text: text} // URL set by caller
}

// ── Colors for code block ──────────────────────────

var (
	colCodeBg   = color.RGBA{R: 40, G: 40, B: 40, A: 255}
	colLineNum  = color.RGBA{R: 100, G: 100, B: 100, A: 255}
	colQuoteBar = color.RGBA{R: 60, G: 120, B: 216, A: 255}
	colQuoteBg  = color.RGBA{R: 40, G: 40, B: 40, A: 40}
	colTblHead  = color.RGBA{R: 55, G: 85, B: 135, A: 100}
	colTblAlt   = color.RGBA{R: 50, G: 50, B: 50, A: 50}
)

// ── Helpers ────────────────────────────────────────

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
	return (r >= 0x4E00 && r <= 0x9FFF) || (r >= 0x3400 && r <= 0x4DBF) ||
		(r >= 0xF900 && r <= 0xFAFF) || (r >= 0x3000 && r <= 0x303F) ||
		(r >= 0x3040 && r <= 0x309F) || (r >= 0x30A0 && r <= 0x30FF) ||
		(r >= 0xAC00 && r <= 0xD7AF) || (r >= 0x1100 && r <= 0x11FF)
}
