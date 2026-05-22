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
		return color.NRGBA{R: 11, G: 16, B: 31, A: 255}
	case theme.ColorNameButton:
		return color.NRGBA{R: 26, G: 37, B: 64, A: 255}
	case theme.ColorNameDisabledButton:
		return color.NRGBA{R: 23, G: 31, B: 52, A: 255}

	// Text
	case theme.ColorNameForeground:
		return color.NRGBA{R: 245, G: 247, B: 251, A: 255}
	case theme.ColorNameDisabled:
		return color.NRGBA{R: 122, G: 136, B: 170, A: 255}
	case theme.ColorNamePlaceHolder:
		return color.NRGBA{R: 113, G: 128, B: 163, A: 255}

	// Primary accent
	case theme.ColorNamePrimary:
		return color.NRGBA{R: 110, G: 168, B: 255, A: 255}
	case theme.ColorNameForegroundOnPrimary:
		return color.NRGBA{R: 255, G: 255, B: 255, A: 255}

	// Input
	case theme.ColorNameInputBackground:
		return color.NRGBA{R: 19, G: 28, B: 48, A: 255}
	case theme.ColorNameInputBorder:
		return color.NRGBA{R: 42, G: 54, B: 85, A: 255}

	// Semantic colors
	case theme.ColorNameError:
		return color.NRGBA{R: 255, G: 108, B: 122, A: 255}
	case theme.ColorNameWarning:
		return color.NRGBA{R: 243, G: 179, B: 92, A: 255}
	case theme.ColorNameSuccess:
		return color.NRGBA{R: 86, G: 211, B: 155, A: 255}

	// Header / Card
	case theme.ColorNameHeaderBackground:
		return color.NRGBA{R: 19, G: 26, B: 43, A: 255}

	// Menu / Popup / Dropdown backgrounds
	case theme.ColorNameMenuBackground:
		return color.NRGBA{R: 19, G: 28, B: 48, A: 255}
	case theme.ColorNameOverlayBackground:
		return color.NRGBA{R: 26, G: 37, B: 64, A: 255}

	// Focus / Hover
	case theme.ColorNameFocus:
		return color.NRGBA{R: 110, G: 168, B: 255, A: 110}
	case theme.ColorNameHover:
		return color.NRGBA{R: 34, G: 46, B: 74, A: 255}

	// Separator
	case theme.ColorNameSeparator:
		return color.NRGBA{R: 42, G: 54, B: 85, A: 255}

	// Shadow
	case theme.ColorNameShadow:
		return color.NRGBA{R: 0, G: 0, B: 0, A: 55}

	// Selection highlight
	case theme.ColorNameSelection:
		return color.NRGBA{R: 110, G: 168, B: 255, A: 70}
	}
	return m.Theme.Color(name, variant)
}

func (m *modernTheme) Size(name fyne.ThemeSizeName) float32 {
	switch name {
	case theme.SizeNamePadding:
		return 10
	case theme.SizeNameInlineIcon:
		return 20
	case theme.SizeNameScrollBar:
		return 8
	case theme.SizeNameScrollBarSmall:
		return 6
	case theme.SizeNameText:
		return 14
	case theme.SizeNameHeadingText:
		return 21
	case theme.SizeNameSubHeadingText:
		return 17
	case theme.SizeNameCaptionText:
		return 11
	case theme.SizeNameInputBorder:
		return 1
	case theme.SizeNameInputRadius:
		return 14
	case theme.SizeNameSelectionRadius:
		return 10
	}
	return m.Theme.Size(name)
}

func (m *modernTheme) Font(style fyne.TextStyle) fyne.Resource {
	return m.Theme.Font(style)
}

func (m *modernTheme) Icon(name fyne.ThemeIconName) fyne.Resource {
	return m.Theme.Icon(name)
}
