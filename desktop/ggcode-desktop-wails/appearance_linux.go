//go:build linux

package main

import (
	"os"
	"os/exec"
	"strings"
	"time"

	"github.com/topcheer/ggcode/internal/safego"
)

// detectMacDarkMode reports whether the OS prefers a dark UI. On Linux it asks
// gsettings (GNOME/KDE/Cinnamon implement the color-scheme key) and falls back
// to the GTK_THEME environment variable.
func detectMacDarkMode() bool {
	if cmd := exec.Command("gsettings", "get", "org.gnome.desktop.interface", "color-scheme"); true {
		done := make(chan string, 1)
		go func() {
			defer safego.Recover("desktop.appearance-gsettings")
			out, err := cmd.Output()
			if err != nil {
				done <- ""
				return
			}
			done <- string(out)
		}()
		select {
		case out := <-done:
			if strings.Contains(out, "prefer-dark") {
				return true
			}
			if out != "" {
				return false
			}
		case <-time.After(300 * time.Millisecond):
			_ = cmd.Process.Kill()
		}
	}
	return strings.Contains(strings.ToLower(os.Getenv("GTK_THEME")), "dark")
}
