package main

import (
	_ "embed"

	"fyne.io/fyne/v2"
	"fyne.io/fyne/v2/app"
)

//go:embed icon.png
var iconBytes []byte

func main() {
	a := app.NewWithID("com.ggcode.desktop")

	// Load config to determine theme
	cfg := LoadDesktopConfig()
	a.Settings().SetTheme(newThemeForScheme(cfg.Theme))

	// Set application icon.
	a.SetIcon(fyne.NewStaticResource("icon.png", iconBytes))

	desktop := NewApp(a)
	desktop.Run()
}

// setWindowIcon sets the window icon from the embedded resource.
func setWindowIcon(w fyne.Window) {
	w.SetIcon(fyne.NewStaticResource("icon.png", iconBytes))

	// macOS: write icon to temp file and set as dock icon via native API.
	tmpIcon, err := writeTempDataFile("ggcode-icon", ".png", iconBytes)
	if err == nil {
		setDockIconMac(tmpIcon)
	}
}
