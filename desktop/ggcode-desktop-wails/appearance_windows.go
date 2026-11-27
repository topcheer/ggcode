//go:build windows

package main

import (
	"os/exec"
	"strings"
)

// detectMacDarkMode reports whether the OS prefers a dark UI. On Windows it
// reads AppsUseLightTheme from the registry (0 = dark). Previously every
// non-macOS platform returned true, so light-theme users got a dark window
// background on every launch.
func detectMacDarkMode() bool {
	out, err := exec.Command("reg", "query",
		`HKCU\Software\Microsoft\Windows\CurrentVersion\Themes\Personalize`,
		"/v", "AppsUseLightTheme").Output()
	if err != nil {
		return false
	}
	for _, line := range strings.Split(string(out), "\n") {
		line = strings.TrimSpace(line)
		if strings.HasPrefix(line, "AppsUseLightTheme") {
			fields := strings.Fields(line)
			if len(fields) >= 3 && fields[2] == "0x0" {
				return true
			}
		}
	}
	return false
}
