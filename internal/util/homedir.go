package util

import (
	"os"
	"path/filepath"
	"runtime"
	"strings"
)

func HomeDir() string {

	if h := os.Getenv("HOME"); h != "" {
		if runtime.GOOS != "windows" || isWindowsStylePath(h) {
			return h
		}
	}

	if h, err := os.UserHomeDir(); err == nil {
		return h
	}

	if h := os.Getenv("USERPROFILE"); h != "" {
		return h
	}
	return ""
}

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

func ConfigDir() string {
	home := HomeDir()
	if home == "" {
		return ""
	}
	return filepath.Join(home, ".ggcode")
}
