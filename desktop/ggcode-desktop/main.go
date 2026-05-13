package main

import (
	"fyne.io/fyne/v2/app"
)

func main() {
	a := app.NewWithID("com.ggcode.desktop")
	a.Settings().SetTheme(newModernTheme())

	desktop := NewApp(a)
	desktop.Run()
}
