package markdownx

import (
	"image/color"

	"fyne.io/fyne/v2"
)

// ── Colors ─────────────────────────────────────────

var (
	colFg       = color.RGBA{R: 220, G: 220, B: 220, A: 255}
	colFgBold   = color.RGBA{R: 240, G: 240, B: 240, A: 255}
	colFgDim    = color.RGBA{R: 140, G: 140, B: 140, A: 255}
	colFgCode   = color.RGBA{R: 255, G: 200, B: 120, A: 255}
	colCodeBg   = color.RGBA{R: 45, G: 45, B: 45, A: 255}
	colLineNum  = color.RGBA{R: 100, G: 100, B: 100, A: 255}
	colHR       = color.RGBA{R: 80, G: 80, B: 80, A: 255}
	colQuoteBar = color.RGBA{R: 60, G: 120, B: 216, A: 255}
	colQuoteBg  = color.RGBA{R: 40, G: 40, B: 40, A: 30}
	colTblHead  = color.RGBA{R: 60, G: 90, B: 140, A: 80}
	colTblAlt   = color.RGBA{R: 50, G: 50, B: 50, A: 40}
)

// ── Font sizes ─────────────────────────────────────

const (
	textSize     float32 = 14
	heading1Size float32 = 28
	heading2Size float32 = 22
	heading3Size float32 = 18
	heading4Size float32 = 16
	codeSize     float32 = 13
	lineNumSize  float32 = 12
)

// ── Spacing ────────────────────────────────────────

const (
	lineSpacing   float32 = 4 // between lines within a block
	blockSpacing  float32 = 8 // between blocks
	paraSpacing   float32 = 6 // paragraph top/bottom padding
	indentSize    float32 = 20 // list indent per level
	quoteIndent   float32 = 12 // blockquote left indent
	quoteBarWidth float32 = 3  // blockquote left bar width
)

// ── TextStyle helpers ──────────────────────────────

type textStyle struct {
	bold      bool
	italic    bool
	monospace bool
	size      float32
	color     color.Color
}

func (s textStyle) fyneStyle() fyne.TextStyle {
	return fyne.TextStyle{
		Bold:      s.bold,
		Italic:    s.italic,
		Monospace: s.monospace,
	}
}

func normalStyle() textStyle {
	return textStyle{size: textSize, color: colFg}
}

func boldStyle() textStyle {
	return textStyle{bold: true, size: textSize, color: colFgBold}
}

func italicStyle() textStyle {
	return textStyle{italic: true, size: textSize, color: colFg}
}

func codeSpanStyle() textStyle {
	return textStyle{monospace: true, size: textSize, color: colFgCode}
}

func headingStyle(level int) textStyle {
	sz := textSize
	switch level {
	case 1:
		sz = heading1Size
	case 2:
		sz = heading2Size
	case 3:
		sz = heading3Size
	default:
		sz = heading4Size
	}
	return textStyle{bold: true, size: sz, color: colFgBold}
}

// runeWidth returns display width (CJK = 2).
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
