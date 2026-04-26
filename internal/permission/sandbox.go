package permission

import (
	"os"
	"path/filepath"
	"strings"
)

// PathSandbox restricts file operations to allowed directories.
type PathSandbox struct {
	allowedDirs []string
}

// NewPathSandbox creates a sandbox with the given allowed directories.
// If empty, defaults to the current working directory.
func NewPathSandbox(allowedDirs []string) *PathSandbox {
	if len(allowedDirs) == 0 {
		if wd, err := os.Getwd(); err == nil {
			allowedDirs = []string{wd}
		}
	}
	// Normalize paths (resolve symlinks like /tmp -> /private/tmp on macOS)
	normalized := make([]string, 0, len(allowedDirs))
	for _, d := range allowedDirs {
		resolved := resolvePath(d)
		if resolved == "" {
			continue
		}
		normalized = append(normalized, resolved)
	}
	return &PathSandbox{allowedDirs: normalized}
}

// resolvePath tries to resolve symlinks as far as possible.
// For existing paths, returns the fully resolved path.
// For non-existing paths, resolves the longest existing prefix.
func resolvePath(path string) string {
	abs, err := filepath.Abs(path)
	if err != nil {
		return ""
	}
	abs = filepath.Clean(abs)

	resolved, err := filepath.EvalSymlinks(abs)
	if err == nil {
		return filepath.Clean(resolved)
	}

	// File doesn't exist; walk up to find longest existing prefix
	dir := abs
	remaining := ""
	for {
		resolved, err := filepath.EvalSymlinks(dir)
		if err == nil {
			resolved = filepath.Clean(resolved)
			if remaining != "" {
				return filepath.Join(resolved, remaining)
			}
			return resolved
		}
		// Go up one level
		base := filepath.Base(dir)
		if remaining == "" {
			remaining = base
		} else {
			remaining = filepath.Join(base, remaining)
		}
		parent := filepath.Dir(dir)
		if parent == dir {
			break // reached root
		}
		dir = parent
	}
	return abs // fallback to original
}

// Allowed returns true if the path is within an allowed directory.
// It resolves symlinks to prevent sandbox escapes.
func (s *PathSandbox) Allowed(path string) bool {
	if len(s.allowedDirs) == 0 {
		return true // no restrictions
	}

	resolved := resolvePath(path)
	if resolved == "" {
		return false
	}

	for _, dir := range s.allowedDirs {
		if strings.HasPrefix(resolved, dir+string(os.PathSeparator)) || resolved == dir {
			return true
		}
	}
	return false
}

// AllowedDirs returns the list of allowed directories.
func (s *PathSandbox) AllowedDirs() []string {
	return s.allowedDirs
}
