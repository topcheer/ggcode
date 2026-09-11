package util

import (
	"os"
	"path/filepath"
	"runtime"
	"strings"
)

// HomeDir returns the user's home directory.
// It checks $HOME first (respecting overrides), then falls back to
// os.UserHomeDir (which handles $HOME on Unix, $USERPROFILE on Windows).
// Finally checks %USERPROFILE% directly (for edge cases on Windows).
// Returns empty string if neither is available.
func HomeDir() string {
	// Respect explicit HOME override (common in testing and CI).
	// #1837 case 1: on Windows, only trust a HOME that is itself a
	// Windows-style path (drive letter or UNC). Git Bash/MSYS export an
	// MSYS-style HOME like /c/Users/x; Go's standard library deliberately
	// ignores HOME on Windows, and honoring the POSIX-form value here split
	// the process into two homes — config/credentials via this helper
	// resolved to a drive-root-relative path while os.UserHomeDir callers
	// (write_file, skills, update, a2a registry, tui) used USERPROFILE.
	// Windows-form overrides (tests, CI) are still honored.
	if h := os.Getenv("HOME"); h != "" {
		if runtime.GOOS != "windows" || isWindowsStylePath(h) {
			return h
		}
	}
	// os.UserHomeDir handles:
	//   Unix: $HOME
	//   Windows: %USERPROFILE%, %HOMEDRIVE%%HOMEPATH%
	if h, err := os.UserHomeDir(); err == nil {
		return h
	}
	// Windows fallback when os.UserHomeDir fails
	if h := os.Getenv("USERPROFILE"); h != "" {
		return h
	}
	return ""
}

// isWindowsStylePath reports whether p is a Windows-form path: a drive
// letter root (C:\ or C:/) or a UNC path (\\server\share).
func isWindowsStylePath(p string) bool {
	if strings.HasPrefix(p, `\\`) {
		return true
	}
	if len(p) < 3 {
		return false
	}
	return isAsciiLetter(p[0]) && p[1] == ':' && (p[2] == '\\' || p[2] == '/')
}

func isAsciiLetter(c byte) bool {
	return (c >= 'a' && c <= 'z') || (c >= 'A' && c <= 'Z')
}

// ConfigDir returns the path to the ggcode config directory (~/.ggcode).
// Returns empty string if home directory cannot be determined.
func ConfigDir() string {
	home := HomeDir()
	if home == "" {
		return ""
	}
	return filepath.Join(home, ".ggcode")
}
