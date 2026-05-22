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

// ── Theme-aware block colors ───────────────────────

func currentThemeColor(name fyne.ThemeColorName) color.Color {
	app := fyne.CurrentApp()
	if app == nil {
		return themeColor(theme.DefaultTheme(), theme.VariantDark, name)
	}
	return themeColor(app.Settings().Theme(), app.Settings().ThemeVariant(), name)
}

func themeColor(th fyne.Theme, variant fyne.ThemeVariant, name fyne.ThemeColorName) color.Color {
	if th == nil {
		th = theme.DefaultTheme()
	}
	return th.Color(name, variant)
}

func toNRGBA(c color.Color) color.NRGBA {
	r, g, b, a := c.RGBA()
	return color.NRGBA{R: uint8(r >> 8), G: uint8(g >> 8), B: uint8(b >> 8), A: uint8(a >> 8)}
}

func blendColor(base, overlay color.Color, alpha float64) color.Color {
	if alpha < 0 {
		alpha = 0
	}
	if alpha > 1 {
		alpha = 1
	}
	b := toNRGBA(base)
	o := toNRGBA(overlay)
	mix := func(x, y uint8) uint8 {
		return uint8(float64(x)*(1-alpha) + float64(y)*alpha + 0.5)
	}
	return color.NRGBA{
		R: mix(b.R, o.R),
		G: mix(b.G, o.G),
		B: mix(b.B, o.B),
		A: 255,
	}
}

func codeBlockBackgroundColor() color.Color {
	return codeBlockBackgroundColorForTheme(nil, theme.VariantDark)
}

func codeBlockBackgroundColorForTheme(th fyne.Theme, variant fyne.ThemeVariant) color.Color {
	if th == nil {
		return blendColor(currentThemeColor(theme.ColorNameInputBackground), currentThemeColor(theme.ColorNameForeground), 0.08)
	}
	return blendColor(themeColor(th, variant, theme.ColorNameInputBackground), themeColor(th, variant, theme.ColorNameForeground), 0.08)
}

func quoteBarColor() color.Color {
	return quoteBarColorForTheme(nil, theme.VariantDark)
}

func quoteBarColorForTheme(th fyne.Theme, variant fyne.ThemeVariant) color.Color {
	if th == nil {
		return currentThemeColor(theme.ColorNamePrimary)
	}
	return themeColor(th, variant, theme.ColorNamePrimary)
}

func quoteBackgroundColor() color.Color {
	return quoteBackgroundColorForTheme(nil, theme.VariantDark)
}

func quoteBackgroundColorForTheme(th fyne.Theme, variant fyne.ThemeVariant) color.Color {
	if th == nil {
		return blendColor(currentThemeColor(theme.ColorNameBackground), currentThemeColor(theme.ColorNamePrimary), 0.08)
	}
	return blendColor(themeColor(th, variant, theme.ColorNameBackground), themeColor(th, variant, theme.ColorNamePrimary), 0.08)
}

func tableHeaderBackgroundColor() color.Color {
	return tableHeaderBackgroundColorForTheme(nil, theme.VariantDark)
}

func tableHeaderBackgroundColorForTheme(th fyne.Theme, variant fyne.ThemeVariant) color.Color {
	if th == nil {
		return blendColor(currentThemeColor(theme.ColorNameInputBackground), currentThemeColor(theme.ColorNamePrimary), 0.22)
	}
	return blendColor(themeColor(th, variant, theme.ColorNameInputBackground), themeColor(th, variant, theme.ColorNamePrimary), 0.22)
}

func tableAlternateBackgroundColor() color.Color {
	return tableAlternateBackgroundColorForTheme(nil, theme.VariantDark)
}

func tableAlternateBackgroundColorForTheme(th fyne.Theme, variant fyne.ThemeVariant) color.Color {
	if th == nil {
		return blendColor(currentThemeColor(theme.ColorNameBackground), currentThemeColor(theme.ColorNameForeground), 0.04)
	}
	return blendColor(themeColor(th, variant, theme.ColorNameBackground), themeColor(th, variant, theme.ColorNameForeground), 0.04)
}

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
