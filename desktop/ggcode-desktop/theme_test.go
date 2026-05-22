package main

import (
	"image/color"
	"math"
	"testing"
)

// relativeLuminance computes the WCAG 2.0 relative luminance of a color.
func relativeLuminance(c color.Color) float64 {
	r, g, b, _ := c.RGBA()
	rl := srgbToLinear(float64(r) / 65535.0)
	gl := srgbToLinear(float64(g) / 65535.0)
	bl := srgbToLinear(float64(b) / 65535.0)
	return 0.2126*rl + 0.7152*gl + 0.0722*bl
}

func srgbToLinear(v float64) float64 {
	if v <= 0.04045 {
		return v / 12.92
	}
	return math.Pow((v+0.055)/1.055, 2.4)
}

// contrastRatio computes WCAG 2.0 contrast ratio between two colors.
func contrastRatio(c1, c2 color.Color) float64 {
	l1 := relativeLuminance(c1)
	l2 := relativeLuminance(c2)
	if l1 < l2 {
		l1, l2 = l2, l1
	}
	return (l1 + 0.05) / (l2 + 0.05)
}

func TestAllThemesContrastRatios(t *testing.T) {
	minNormalText := 4.5 // WCAG AA normal text
	minLargeText := 3.0  // WCAG AA large text

	for _, themeName := range availableThemes {
		pal, ok := themePalettes[themeName]
		if !ok {
			t.Fatalf("theme %q not found in themePalettes", themeName)
		}

		t.Run(themeName, func(t *testing.T) {
			// Core text readability
			checkPair(t, "Foreground/Background", pal.Foreground, pal.Background, minNormalText, themeName)
			checkPair(t, "Foreground/Button", pal.Foreground, pal.Button, minNormalText, themeName)
			checkPair(t, "Foreground/InputBackground", pal.Foreground, pal.InputBackground, minNormalText, themeName)
			checkPair(t, "Foreground/HeaderBackground", pal.Foreground, pal.HeaderBackground, minNormalText, themeName)
			checkPair(t, "Foreground/MenuBackground", pal.Foreground, pal.MenuBackground, minNormalText, themeName)
			checkPair(t, "Foreground/OverlayBackground", pal.Foreground, pal.OverlayBackground, minNormalText, themeName)

			// Semantic colors on background
			checkPair(t, "Primary/Background", pal.Primary, pal.Background, minNormalText, themeName)
			checkPair(t, "Error/Background", pal.Error, pal.Background, minNormalText, themeName)
			checkPair(t, "Warning/Background", pal.Warning, pal.Background, minLargeText, themeName)
			checkPair(t, "Success/Background", pal.Success, pal.Background, minNormalText, themeName)

			// Button text
			checkPair(t, "FgOnPrimary/Primary", pal.FgOnPrimary, pal.Primary, minNormalText, themeName)

			// Secondary text
			checkPair(t, "Placeholder/Background", pal.Placeholder, pal.Background, minLargeText, themeName)
			checkPair(t, "Disabled/Background", pal.Disabled, pal.Background, minLargeText, themeName)
		})
	}
}

func TestNoTwoThemesProduceIdenticalBackground(t *testing.T) {
	bgs := map[string]color.Color{}
	for _, name := range availableThemes {
		bgs[name] = themePalettes[name].Background
	}
	for i, n1 := range availableThemes {
		for _, n2 := range availableThemes[i+1:] {
			r1, g1, b1, _ := bgs[n1].RGBA()
			r2, g2, b2, _ := bgs[n2].RGBA()
			if r1 == r2 && g1 == g2 && b1 == b2 {
				t.Errorf("themes %q and %q have identical background colors", n1, n2)
			}
		}
	}
}

func TestAllPalettesHaveAllFields(t *testing.T) {
	for _, name := range availableThemes {
		pal := themePalettes[name]
		t.Run(name, func(t *testing.T) {
			assertNonZero(t, "Background", pal.Background)
			assertNonZero(t, "Foreground", pal.Foreground)
			assertNonZero(t, "Primary", pal.Primary)
			assertNonZero(t, "Error", pal.Error)
			assertNonZero(t, "Success", pal.Success)
			assertNonZero(t, "InputBackground", pal.InputBackground)
			assertNonZero(t, "Button", pal.Button)
			assertNonZero(t, "FgOnPrimary", pal.FgOnPrimary)
		})
	}
}

func assertNonZero(t *testing.T, label string, c color.Color) {
	t.Helper()
	r, g, b, a := c.RGBA()
	if r == 0 && g == 0 && b == 0 && a == 0 {
		t.Errorf("%s is fully transparent (zero value)", label)
	}
}

func checkPair(t *testing.T, label string, fg, bg color.Color, minRatio float64, theme string) {
	t.Helper()
	ratio := contrastRatio(fg, bg)
	if ratio < minRatio {
		r, g, b, _ := fg.RGBA()
		rb, gb, bb, _ := bg.RGBA()
		t.Errorf("[%s] %s: contrast ratio %.2f < %.2f  (fg=#%02X%02X%02X bg=#%02X%02X%02X)",
			theme, label, ratio, minRatio,
			uint8(r>>8), uint8(g>>8), uint8(b>>8),
			uint8(rb>>8), uint8(gb>>8), uint8(bb>>8))
	}
}
