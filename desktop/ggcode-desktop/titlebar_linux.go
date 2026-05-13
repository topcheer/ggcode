//go:build linux

package main

import (
	"fyne.io/fyne/v2"
)

func setupNativeTitlebar(w fyne.Window) {
	// Linux (X11/Wayland): GLFW uses GTK/Adwaita theme. The dark theme is
	// controlled by the GTK theme setting, not per-window.
	// Fyne's custom theme already handles the content area styling.
	// On GNOME, users can set system dark mode via:
	//   gsettings set org.gnome.desktop.interface color-scheme 'prefer-dark'
	// No per-window API available on Linux.
}
