package main

import (
	"image/color"

	"fyne.io/fyne/v2"
	"fyne.io/fyne/v2/theme"
)

// modernTheme implements a clean, modern dark theme inspired by
// ChatGPT/Cursor-style AI chat applications.
type modernTheme struct {
	fyne.Theme
}

func newModernTheme() fyne.Theme {
	return &modernTheme{Theme: theme.DefaultTheme()}
}

func (m *modernTheme) Color(name fyne.ThemeColorName, variant fyne.ThemeVariant) color.Color {
	switch name {
	// Background
	case theme.ColorNameBackground:
		return color.NRGBA{R: 30, G: 30, B: 30, A: 255} // dark charcoal
	case theme.ColorNameButton:
		return color.NRGBA{R: 50, G: 50, B: 50, A: 255}
	case theme.ColorNameDisabledButton:
		return color.NRGBA{R: 40, G: 40, B: 40, A: 255}

	// Text
	case theme.ColorNameForeground:
		return color.NRGBA{R: 230, G: 230, B: 230, A: 255}
	case theme.ColorNameDisabled:
		return color.NRGBA{R: 120, G: 120, B: 120, A: 255}
	case theme.ColorNamePlaceHolder:
		return color.NRGBA{R: 100, G: 100, B: 100, A: 255}

	// Primary accent — soft blue
	case theme.ColorNamePrimary:
		return color.NRGBA{R: 86, G: 154, B: 214, A: 255}
	case theme.ColorNameForegroundOnPrimary:
		return color.NRGBA{R: 255, G: 255, B: 255, A: 255}

	// Input
	case theme.ColorNameInputBackground:
		return color.NRGBA{R: 42, G: 42, B: 42, A: 255}
	case theme.ColorNameInputBorder:
		return color.NRGBA{R: 65, G: 65, B: 65, A: 255}

	// Semantic colors
	case theme.ColorNameError:
		return color.NRGBA{R: 235, G: 87, B: 87, A: 255}
	case theme.ColorNameWarning:
		return color.NRGBA{R: 230, G: 170, B: 50, A: 255}
	case theme.ColorNameSuccess:
		return color.NRGBA{R: 72, G: 191, B: 132, A: 255}

	// Header / Card
	case theme.ColorNameHeaderBackground:
		return color.NRGBA{R: 38, G: 38, B: 38, A: 255}

	// Focus / Hover
	case theme.ColorNameFocus:
		return color.NRGBA{R: 86, G: 154, B: 214, A: 100}
	case theme.ColorNameHover:
		return color.NRGBA{R: 60, G: 60, B: 60, A: 255}

	// Separator
	case theme.ColorNameSeparator:
		return color.NRGBA{R: 55, G: 55, B: 55, A: 255}

	// Shadow
	case theme.ColorNameShadow:
		return color.NRGBA{R: 0, G: 0, B: 0, A: 40}
	}
	return m.Theme.Color(name, variant)
}

func (m *modernTheme) Size(name fyne.ThemeSizeName) float32 {
	switch name {
	case theme.SizeNamePadding:
		return 8
	case theme.SizeNameInlineIcon:
		return 20
	case theme.SizeNameScrollBar:
		return 8
	case theme.SizeNameScrollBarSmall:
		return 6
	case theme.SizeNameText:
		return 14
	case theme.SizeNameHeadingText:
		return 20
	case theme.SizeNameSubHeadingText:
		return 16
	case theme.SizeNameCaptionText:
		return 11
	case theme.SizeNameInputBorder:
		return 1
	case theme.SizeNameInputRadius:
		return 8
	case theme.SizeNameSelectionRadius:
		return 6
		return 2 // not used much in cards
	}
	return m.Theme.Size(name)
}

func (m *modernTheme) Font(style fyne.TextStyle) fyne.Resource {
	return m.Theme.Font(style)
}

func (m *modernTheme) Icon(name fyne.ThemeIconName) fyne.Resource {
	return m.Theme.Icon(name)
}
