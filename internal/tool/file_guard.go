package tool

import (
	"os"
	"path/filepath"
	"strings"
	"sync"

	"github.com/topcheer/ggcode/internal/debug"
)

// FileGuard enforces user-configured write-protection patterns.
//
// Competitor analysis:
//   - Cursor: .cursorrules + workspace boundaries restrict agent writes
//   - Claude Code: permission system with file restrictions per mode
//   - OpenHands: configurable file access controls (deny/allow lists)
//   - Aider: .aider.conf.yml with "chat-files" / "read-only-files"
//
// ggcode previously only had AllowedPathChecker (sandbox boundary) — no
// per-path deny list. This guard adds that missing layer: patterns like
// ".env*", ".git/", "*.lock", or "src/secrets/**" block write tools from
// modifying sensitive files even within the allowed workspace.
//
// The guard is applied AFTER the sandbox check: the path must be inside
// the workspace AND not match any protected pattern. Read tools are never
// blocked — protection is write-only.

// DefaultProtectedPatterns are applied automatically unless the user
// explicitly sets protected_paths in config (which replaces these).
var DefaultProtectedPatterns = []string{
	".env",
	".env.*",
	".git/",
}

// FileGuard holds compiled protected-path patterns and checks paths against them.
type FileGuard struct {
	mu       sync.RWMutex
	patterns []string
	// explicit: true if the user set protected_paths in config (overrides defaults)
	explicit bool
}

// NewFileGuard creates a FileGuard from config patterns.
// If patterns is non-empty, those replace the defaults (explicit=true).
// If patterns is empty, DefaultProtectedPatterns are used.
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

// Patterns returns the current protected patterns (for display).
func (g *FileGuard) Patterns() []string {
	g.mu.RLock()
	defer g.mu.RUnlock()
	return append([]string{}, g.patterns...)
}

// IsProtected returns true if the given path matches any protected pattern,
// along with the matched pattern for the error message.
func (g *FileGuard) IsProtected(absPath, workingDir string) (bool, string) {
	g.mu.RLock()
	defer g.mu.RUnlock()

	if len(g.patterns) == 0 {
		return false, ""
	}

	// Normalize the path for pattern matching
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

// matchProtectedPattern checks if a path matches a protection pattern.
// Patterns support:
//   - Glob: ".env*", "*.lock", "src/secrets/**"
//   - Directory prefix: ".git/" matches any path under .git/
//   - Exact file: "go.sum" matches go.sum at workspace root
func matchProtectedPattern(relPath, slashPath, pattern string) bool {
	pat := strings.TrimSpace(pattern)
	if pat == "" {
		return false
	}

	// Directory prefix match: "dir/" matches "dir/anything" or "subdir/dir/anything"
	if strings.HasSuffix(pat, "/") {
		prefix := pat
		// Match at workspace root
		if strings.HasPrefix(relPath+"/", prefix) ||
			strings.HasPrefix(slashPath+"/", prefix) ||
			relPath+"/" == prefix {
			return true
		}
		// Match as any path component (e.g. ".git/" matches "subdir/.git/refs")
		dirName := strings.TrimSuffix(pat, "/")
		segments := strings.Split(relPath, "/")
		for _, seg := range segments {
			if seg == dirName {
				return true
			}
		}
		return false
	}

	// Try glob match against relative path
	matched, err := matchGlob(pat, relPath)
	if err == nil && matched {
		return true
	}

	// Try glob match against the full slash path
	matched, err = matchGlob(pat, slashPath)
	if err == nil && matched {
		return true
	}

	// Also check if the pattern matches just the basename
	base := filepath.Base(relPath)
	matched, err = matchGlob(pat, base)
	if err == nil && matched {
		return true
	}

	return false
}

// matchGlob is a thin wrapper around filepath.Match that also supports
// "**" (match any number of path components).
func matchGlob(pattern, name string) (bool, error) {
	// Handle ** patterns by splitting on **
	if strings.Contains(pattern, "**") {
		return matchDoubleStar(pattern, name)
	}
	return filepath.Match(pattern, name)
}

// matchDoubleStar handles glob patterns containing **.
// "src/**" matches anything under src/.
// "src/**/test" matches src/a/test, src/a/b/test, etc.
func matchDoubleStar(pattern, name string) (bool, error) {
	parts := strings.SplitN(pattern, "**", 2)
	prefix := strings.TrimSuffix(parts[0], "/")
	suffix := ""
	if len(parts) > 1 {
		suffix = strings.TrimPrefix(parts[1], "/")
	}

	// If no prefix, ** matches everything
	if prefix == "" {
		if suffix == "" {
			return true, nil
		}
		return filepath.Match(suffix, filepath.Base(name))
	}

	// Check name starts with prefix
	if name == prefix || strings.HasPrefix(name, prefix+"/") {
		if suffix == "" {
			return true, nil
		}
		// Get the part after prefix
		rest := strings.TrimPrefix(name, prefix+"/")
		// Check if rest ends with suffix (can be globbed)
		matched, err := filepath.Match(suffix, filepath.Base(rest))
		if err == nil && matched {
			return true, nil
		}
		// Also try matching suffix against the full rest path
		matched2, err2 := filepath.Match(suffix, rest)
		return matched2 && err2 == nil, nil
	}
	return false, nil
}

// CheckWritePath is called by write tools (write_file, edit_file, etc.)
// before writing. Returns an error message if the path is protected,
// or empty string if the write is allowed.
func (g *FileGuard) CheckWritePath(path, workingDir string) string {
	abs, err := filepath.Abs(path)
	if err != nil {
		abs = path
	}
	protected, pattern := g.IsProtected(abs, workingDir)
	if !protected {
		return ""
	}
	debug.Log("fileguard", "blocked write to %q (matched pattern %q)", path, pattern)
	return "Error: path is protected by file guard (matched pattern: " + pattern + "). " +
		"If you need to modify this file, remove or adjust the pattern in protected_paths config."
}

// MergeFileGuards wraps a sandbox AllowedPathChecker with file-guard protection.
// The returned checker returns false (deny) if EITHER the sandbox denies the
// path OR the file guard protects it.
func MergeFileGuards(sandbox AllowedPathChecker, guard *FileGuard, workingDir string) AllowedPathChecker {
	if guard == nil {
		return sandbox
	}
	return func(path string) bool {
		if sandbox != nil && !sandbox(path) {
			return false
		}
		if msg := guard.CheckWritePath(path, workingDir); msg != "" {
			// We can't return an error message from AllowedPathChecker,
			// so we log it and return false. The write tool will show a
			// generic "path not allowed" message.
			// For better UX, write tools should call CheckWritePath directly.
			debug.Log("fileguard", "denied by guard: %s", path)
			return false
		}
		return true
	}
}

// LoadProtectedPatternsFromFile reads protected patterns from a .ggcode/protect file.
// This is a simple newline-delimited format, # for comments.
// Returns nil if the file doesn't exist (use defaults).
func LoadProtectedPatternsFromFile(workspaceDir string) []string {
	if workspaceDir == "" {
		return nil
	}
	path := filepath.Join(workspaceDir, ".ggcode", "protect")
	data, err := os.ReadFile(path)
	if err != nil {
		return nil
	}
	var patterns []string
	for _, line := range strings.Split(string(data), "\n") {
		line = strings.TrimSpace(line)
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		patterns = append(patterns, line)
	}
	return patterns
}
