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
	a.Settings().SetTheme(newModernTheme())

	// Set application icon.
	a.SetIcon(fyne.NewStaticResource("icon.png", iconBytes))

	desktop := NewApp(a)
	desktop.Run()
}

// setWindowIcon sets the window icon from the embedded resource.
func setWindowIcon(w fyne.Window) {
	w.SetIcon(fyne.NewStaticResource("icon.png", iconBytes))
}
