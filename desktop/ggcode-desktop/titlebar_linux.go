//go:build linux

package main

import (
	"fyne.io/fyne/v2"
	"image/color"
)

func setupNativeTitlebar(w fyne.Window) nativeTitlebarConfig {
	// Linux (X11/Wayland): GLFW uses GTK/Adwaita theme. The dark theme is
	// controlled by the GTK theme setting, not per-window.
	// Fyne's custom theme already handles the content area styling.
	// On GNOME, users can set system dark mode via:
	//   gsettings set org.gnome.desktop.interface color-scheme 'prefer-dark'
	// No per-window API available on Linux.
	return nativeTitlebarConfig{}
}

func updateNativeTitlebarAppearance(w fyne.Window, bg, fg color.NRGBA) {}

func updateNativeWindowTitle(w fyne.Window, title string) {}

func setDockIconMac(path string) {}
