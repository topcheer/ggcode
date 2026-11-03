package permission

import (
	"os"
	"path/filepath"
	"strings"

	"github.com/topcheer/ggcode/internal/debug"
)

type PathSandbox struct {
	allowedDirs []string

	getwdFailed bool
}

func NewPathSandbox(allowedDirs []string) *PathSandbox {
	if len(allowedDirs) == 0 {
		if wd, err := os.Getwd(); err == nil {
			allowedDirs = []string{wd}
		} else {

			debug.Log("permission", "PathSandbox: os.Getwd failed (%v); sandbox will fail closed", err)
			return &PathSandbox{getwdFailed: true}
		}
	}

	normalized := make([]string, 0, len(allowedDirs))
	for _, d := range allowedDirs {
		resolved := resolvePath(d)
		if resolved == "" {
			continue
		}
		normalized = append(normalized, resolved)
	}

	if len(normalized) == 0 {
		debug.Log("permission", "PathSandbox: all %d allowedDirs failed to resolve; sandbox will fail closed", len(allowedDirs))
		return &PathSandbox{getwdFailed: true}
	}
	return &PathSandbox{allowedDirs: normalized}
}

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

		base := filepath.Base(dir)
		if remaining == "" {
			remaining = base
		} else {
			remaining = filepath.Join(base, remaining)
		}
		parent := filepath.Dir(dir)
		if parent == dir {
			break
		}
		dir = parent
	}
	return abs
}

func (s *PathSandbox) Allowed(path string) bool {
	if len(s.allowedDirs) == 0 {
		if s.getwdFailed {

			debug.Log("permission", "PathSandbox: denying %q — sandbox unavailable (os.Getwd failed)", path)
			return false
		}
		return true
	}

	path = expandTilde(path)

	if !filepath.IsAbs(path) && !strings.HasPrefix(path, "/") {
		path = filepath.Join(s.allowedDirs[0], path)
	}

	resolved := resolvePath(path)
	if resolved == "" {
		return false
	}

	for _, dir := range s.allowedDirs {

		if pathHasPrefixFold(resolved, dir+string(os.PathSeparator)) || pathEqualFold(resolved, dir) {
			return true
		}
	}
	return false
}

func (s *PathSandbox) AllowedDirs() []string {
	return s.allowedDirs
}
