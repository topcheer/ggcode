package tool

import (
	"path/filepath"
	"runtime"
	"strings"
	"sync"

	"github.com/topcheer/ggcode/internal/debug"
)

var DefaultProtectedPatterns = []string{
	".env",
	".env.*",
	".git/",
}

type FileGuard struct {
	mu       sync.RWMutex
	patterns []string
	explicit bool
}

func NewFileGuard(patterns []string) *FileGuard {
	fg := &FileGuard{}
	if len(patterns) > 0 {
		fg.patterns = append([]string{}, patterns...)
		fg.explicit = true
	} else {
		fg.patterns = append([]string{}, DefaultProtectedPatterns...)
		fg.explicit = false
	}
	return fg
}

func (g *FileGuard) Patterns() []string {
	g.mu.RLock()
	defer g.mu.RUnlock()
	return append([]string{}, g.patterns...)
}

func (g *FileGuard) IsProtected(absPath, workingDir string) (bool, string) {
	g.mu.RLock()
	defer g.mu.RUnlock()

	if len(g.patterns) == 0 {
		return false, ""
	}

	rel := absPath
	if workingDir != "" {
		if r, err := filepath.Rel(workingDir, absPath); err == nil && !strings.HasPrefix(r, "..") {
			rel = filepath.ToSlash(r)
		}
	}
	rel = strings.TrimPrefix(rel, "./")
	slashPath := filepath.ToSlash(absPath)

	for _, pat := range g.patterns {
		if matchProtectedPattern(rel, slashPath, pat) {
			return true, pat
		}
	}
	return false, ""
}

func fsCaseInsensitive() bool {
	goos := runtime.GOOS
	return goos == "darwin" || goos == "windows"
}

func matchSegFS(pattern, name string) bool {
	if fsCaseInsensitive() {
		pattern = strings.ToLower(pattern)
		name = strings.ToLower(name)
	}
	matched, err := filepath.Match(pattern, name)
	return err == nil && matched
}

func matchProtectedPattern(relPath, slashPath, pattern string) bool {
	pat := strings.TrimSpace(pattern)
	if pat == "" {
		return false
	}

	if strings.HasSuffix(pat, "/") {
		prefix := pat

		hasPrefix := strings.HasPrefix
		if fsCaseInsensitive() {
			hasPrefix = func(s, p string) bool {
				return strings.HasPrefix(strings.ToLower(s), strings.ToLower(p))
			}
		}

		if hasPrefix(relPath+"/", prefix) ||
			hasPrefix(slashPath+"/", prefix) ||
			relPath+"/" == prefix {
			return true
		}

		dirName := strings.TrimSuffix(pat, "/")
		for _, p := range []string{relPath, slashPath} {
			for _, seg := range strings.Split(p, "/") {

				if seg == dirName || (fsCaseInsensitive() && strings.EqualFold(seg, dirName)) {
					return true
				}
			}
		}
		return false
	}

	matched, err := matchGlob(pat, relPath)
	if err == nil && matched {
		return true
	}

	matched, err = matchGlob(pat, slashPath)
	if err == nil && matched {
		return true
	}

	base := filepath.Base(relPath)
	matched, err = matchGlob(pat, base)
	if err == nil && matched {
		return true
	}

	return false
}

func matchGlob(pattern, name string) (bool, error) {

	if strings.Contains(pattern, "**") {
		return matchDoubleStar(pattern, name)
	}
	matched := matchSegFS(pattern, name)
	return matched, nil
}

func matchDoubleStar(pattern, name string) (bool, error) {
	return matchSegsFS(strings.Split(pattern, "/"), strings.Split(name, "/")), nil
}

func matchSegsFS(p, n []string) bool {
	for len(p) > 0 {
		if p[0] == "**" {
			if len(p) == 1 {

				return true
			}

			for i := 0; i <= len(n); i++ {
				if matchSegsFS(p[1:], n[i:]) {
					return true
				}
			}
			return false
		}
		if len(n) == 0 || !matchSegFS(p[0], n[0]) {
			return false
		}
		p, n = p[1:], n[1:]
	}
	return len(n) == 0
}

func resolveSymlinkPrefix(path string) string {
	if resolved, err := filepath.EvalSymlinks(path); err == nil {
		return filepath.Clean(resolved)
	}
	dir, remaining := path, ""
	for {
		if resolved, err := filepath.EvalSymlinks(dir); err == nil {
			if remaining != "" {
				return filepath.Join(filepath.Clean(resolved), remaining)
			}
			return filepath.Clean(resolved)
		}
		base := filepath.Base(dir)
		if remaining == "" {
			remaining = base
		} else {
			remaining = filepath.Join(base, remaining)
		}
		parent := filepath.Dir(dir)
		if parent == dir {
			return path
		}
		dir = parent
	}
}

func (g *FileGuard) CheckWritePath(path, workingDir string) string {
	abs, err := filepath.Abs(path)
	if err != nil {
		abs = path
	}
	protected, pattern := g.IsProtected(abs, workingDir)
	if !protected {

		resolved := resolveSymlinkPrefix(abs)
		if resolved != abs {
			protected, pattern = g.IsProtected(resolved, workingDir)
		}
	}
	if !protected {
		return ""
	}
	debug.Log("fileguard", "blocked write to %q (matched pattern %q)", path, pattern)
	return "Error: path is protected by file guard (matched pattern: " + pattern + "). " +
		"If you need to modify this file, remove or adjust the pattern in protected_paths config."
}

func MergeFileGuards(sandbox AllowedPathChecker, guard *FileGuard, workingDir string) AllowedPathChecker {
	if guard == nil {
		return sandbox
	}
	return func(path string) bool {
		if sandbox != nil && !sandbox(path) {
			return false
		}
		if msg := guard.CheckWritePath(path, workingDir); msg != "" {

			debug.Log("fileguard", "denied by guard: %s", path)
			return false
		}
		return true
	}
}
