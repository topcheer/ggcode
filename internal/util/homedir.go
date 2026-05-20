package util

import (
	"os"
	"path/filepath"
)

// HomeDir returns the user's home directory.
// It checks $HOME first (respecting overrides), then falls back to
// os.UserHomeDir (which handles $HOME on Unix, $USERPROFILE on Windows).
// Finally checks %USERPROFILE% directly (for edge cases on Windows).
// Returns empty string if neither is available.
func HomeDir() string {
	// Respect explicit HOME override (common in testing and CI)
	if h := os.Getenv("HOME"); h != "" {
		return h
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

// ConfigDir returns the path to the ggcode config directory (~/.ggcode).
// Returns empty string if home directory cannot be determined.
func ConfigDir() string {
	home := HomeDir()
	if home == "" {
		return ""
	}
	return filepath.Join(home, ".ggcode")
}
