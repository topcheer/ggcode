package main

import (
	"image/color"

	"fyne.io/fyne/v2"
	"fyne.io/fyne/v2/theme"
)

// ─── Palette Definition ──────────────────────────────

type palette struct {
	Background        color.Color
	Button            color.Color
	DisabledButton    color.Color
	Foreground        color.Color
	Disabled          color.Color
	Placeholder       color.Color
	Primary           color.Color
	FgOnPrimary       color.Color
	InputBackground   color.Color
	InputBorder       color.Color
	Error             color.Color
	Warning           color.Color
	Success           color.Color
	HeaderBackground  color.Color
	MenuBackground    color.Color
	OverlayBackground color.Color
	Focus             color.Color
	Hover             color.Color
	Separator         color.Color
	Shadow            color.Color
	Selection         color.Color
}

func c(r, g, b, a uint8) color.Color {
	return color.NRGBA{R: r, G: g, B: b, A: a}
}

// ─── Theme Palettes ──────────────────────────────────

var themePalettes = map[string]*palette{
	"midnight": {
		Background:        c(11, 16, 31, 255),
		Button:            c(26, 37, 64, 255),
		DisabledButton:    c(23, 31, 52, 255),
		Foreground:        c(245, 247, 251, 255),
		Disabled:          c(122, 136, 170, 255),
		Placeholder:       c(113, 128, 163, 255),
		Primary:           c(110, 168, 255, 255),
		FgOnPrimary:       c(10, 20, 50, 255),
		InputBackground:   c(19, 28, 48, 255),
		InputBorder:       c(42, 54, 85, 255),
		Error:             c(255, 108, 122, 255),
		Warning:           c(243, 179, 92, 255),
		Success:           c(86, 211, 155, 255),
		HeaderBackground:  c(19, 26, 43, 255),
		MenuBackground:    c(19, 28, 48, 255),
		OverlayBackground: c(26, 37, 64, 255),
		Focus:             c(110, 168, 255, 110),
		Hover:             c(34, 46, 74, 255),
		Separator:         c(42, 54, 85, 255),
		Shadow:            c(0, 0, 0, 55),
		Selection:         c(110, 168, 255, 70),
	},
	"oled": {
		Background:        c(0, 0, 0, 255),
		Button:            c(18, 18, 18, 255),
		DisabledButton:    c(14, 14, 14, 255),
		Foreground:        c(240, 240, 240, 255),
		Disabled:          c(90, 90, 90, 255),
		Placeholder:       c(100, 100, 100, 255),
		Primary:           c(100, 180, 255, 255),
		FgOnPrimary:       c(0, 0, 0, 255),
		InputBackground:   c(10, 10, 10, 255),
		InputBorder:       c(40, 40, 40, 255),
		Error:             c(255, 95, 95, 255),
		Warning:           c(255, 185, 50, 255),
		Success:           c(80, 220, 140, 255),
		HeaderBackground:  c(8, 8, 8, 255),
		MenuBackground:    c(10, 10, 10, 255),
		OverlayBackground: c(18, 18, 18, 255),
		Focus:             c(100, 180, 255, 100),
		Hover:             c(28, 28, 28, 255),
		Separator:         c(35, 35, 35, 255),
		Shadow:            c(0, 0, 0, 80),
		Selection:         c(100, 180, 255, 60),
	},
	"nord": {
		Background:        c(46, 52, 64, 255),
		Button:            c(59, 66, 82, 255),
		DisabledButton:    c(52, 58, 72, 255),
		Foreground:        c(236, 239, 244, 255),
		Disabled:          c(128, 137, 153, 255),
		Placeholder:       c(120, 132, 160, 255),
		Primary:           c(136, 192, 208, 255),
		FgOnPrimary:       c(46, 52, 64, 255),
		InputBackground:   c(52, 58, 72, 255),
		InputBorder:       c(67, 76, 94, 255),
		Error:             c(255, 110, 110, 255),
		Warning:           c(235, 203, 139, 255),
		Success:           c(163, 190, 140, 255),
		HeaderBackground:  c(49, 55, 68, 255),
		MenuBackground:    c(52, 58, 72, 255),
		OverlayBackground: c(59, 66, 82, 255),
		Focus:             c(136, 192, 208, 100),
		Hover:             c(67, 76, 94, 255),
		Separator:         c(67, 76, 94, 255),
		Shadow:            c(0, 0, 0, 55),
		Selection:         c(136, 192, 208, 60),
	},
	"rose": {
		Background:        c(25, 15, 20, 255),
		Button:            c(45, 28, 38, 255),
		DisabledButton:    c(36, 22, 30, 255),
		Foreground:        c(248, 240, 243, 255),
		Disabled:          c(150, 110, 125, 255),
		Placeholder:       c(130, 95, 110, 255),
		Primary:           c(244, 143, 177, 255),
		FgOnPrimary:       c(25, 15, 20, 255),
		InputBackground:   c(35, 20, 28, 255),
		InputBorder:       c(65, 40, 52, 255),
		Error:             c(239, 83, 80, 255),
		Warning:           c(255, 183, 77, 255),
		Success:           c(129, 199, 132, 255),
		HeaderBackground:  c(30, 18, 24, 255),
		MenuBackground:    c(35, 20, 28, 255),
		OverlayBackground: c(45, 28, 38, 255),
		Focus:             c(244, 143, 177, 100),
		Hover:             c(55, 35, 48, 255),
		Separator:         c(65, 40, 52, 255),
		Shadow:            c(0, 0, 0, 55),
		Selection:         c(244, 143, 177, 60),
	},
	"forest": {
		Background:        c(12, 20, 15, 255),
		Button:            c(22, 38, 28, 255),
		DisabledButton:    c(18, 30, 22, 255),
		Foreground:        c(235, 248, 240, 255),
		Disabled:          c(110, 145, 120, 255),
		Placeholder:       c(95, 130, 105, 255),
		Primary:           c(102, 210, 150, 255),
		FgOnPrimary:       c(12, 20, 15, 255),
		InputBackground:   c(16, 28, 20, 255),
		InputBorder:       c(35, 60, 42, 255),
		Error:             c(239, 100, 100, 255),
		Warning:           c(255, 190, 80, 255),
		Success:           c(76, 210, 130, 255),
		HeaderBackground:  c(14, 24, 18, 255),
		MenuBackground:    c(16, 28, 20, 255),
		OverlayBackground: c(22, 38, 28, 255),
		Focus:             c(102, 210, 150, 100),
		Hover:             c(30, 50, 36, 255),
		Separator:         c(35, 60, 42, 255),
		Shadow:            c(0, 0, 0, 55),
		Selection:         c(102, 210, 150, 60),
	},
	"light": {
		Background:        c(250, 250, 252, 255),
		Button:            c(235, 237, 242, 255),
		DisabledButton:    c(240, 242, 246, 255),
		Foreground:        c(30, 35, 45, 255),
		Disabled:          c(130, 138, 152, 255),
		Placeholder:       c(110, 118, 132, 255),
		Primary:           c(50, 100, 200, 255),
		FgOnPrimary:       c(255, 255, 255, 255),
		InputBackground:   c(242, 244, 248, 255),
		InputBorder:       c(210, 215, 225, 255),
		Error:             c(210, 50, 50, 255),
		Warning:           c(180, 130, 15, 255),
		Success:           c(20, 130, 55, 255),
		HeaderBackground:  c(244, 246, 250, 255),
		MenuBackground:    c(246, 248, 252, 255),
		OverlayBackground: c(235, 237, 242, 255),
		Focus:             c(50, 100, 200, 100),
		Hover:             c(225, 228, 235, 255),
		Separator:         c(210, 215, 225, 255),
		Shadow:            c(0, 0, 0, 20),
		Selection:         c(50, 100, 200, 40),
	},
}

// availableThemes returns the list of theme names for menus.
var availableThemes = []string{"midnight", "oled", "nord", "rose", "forest", "light"}

// ─── Theme Implementation ────────────────────────────

type modernTheme struct {
	fyne.Theme
	pal *palette
}

func newThemeForScheme(name string) fyne.Theme {
	name = normalizeThemeName(name)
	pal, ok := themePalettes[name]
	if !ok {
		pal = themePalettes["midnight"]
	}
	return &modernTheme{Theme: theme.DefaultTheme(), pal: pal}
}

func normalizeThemeName(name string) string {
	switch name {
	case "", "dark":
		return "midnight"
	default:
		if _, ok := themePalettes[name]; ok {
			return name
		}
		return "midnight"
	}
}

func (m *modernTheme) Color(name fyne.ThemeColorName, variant fyne.ThemeVariant) color.Color {
	p := m.pal
	switch name {
	case theme.ColorNameBackground:
		return p.Background
	case theme.ColorNameButton:
		return p.Button
	case theme.ColorNameDisabledButton:
		return p.DisabledButton
	case theme.ColorNameForeground:
		return p.Foreground
	case theme.ColorNameDisabled:
		return p.Disabled
	case theme.ColorNamePlaceHolder:
		return p.Placeholder
	case theme.ColorNamePrimary:
		return p.Primary
	case theme.ColorNameForegroundOnPrimary:
		return p.FgOnPrimary
	case theme.ColorNameInputBackground:
		return p.InputBackground
	case theme.ColorNameInputBorder:
		return p.InputBorder
	case theme.ColorNameError:
		return p.Error
	case theme.ColorNameWarning:
		return p.Warning
	case theme.ColorNameSuccess:
		return p.Success
	case theme.ColorNameHeaderBackground:
		return p.HeaderBackground
	case theme.ColorNameMenuBackground:
		return p.MenuBackground
	case theme.ColorNameOverlayBackground:
		return p.OverlayBackground
	case theme.ColorNameFocus:
		return p.Focus
	case theme.ColorNameHover:
		return p.Hover
	case theme.ColorNameSeparator:
		return p.Separator
	case theme.ColorNameShadow:
		return p.Shadow
	case theme.ColorNameSelection:
		return p.Selection
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
