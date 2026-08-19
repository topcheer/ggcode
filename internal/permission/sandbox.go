package permission

import (
	"os"
	"path/filepath"
	"strings"

	"github.com/topcheer/ggcode/internal/debug"
)

// PathSandbox restricts file operations to allowed directories.
type PathSandbox struct {
	allowedDirs []string
	// getwdFailed records that the cwd fallback could not be established at
	// construction time (os.Getwd error, e.g. deleted cwd on Linux). The
	// sandbox then fails closed instead of silently allowing everything
	// (#573-F).
	getwdFailed bool
}

// NewPathSandbox creates a sandbox with the given allowed directories.
// If empty, defaults to the current working directory.
func NewPathSandbox(allowedDirs []string) *PathSandbox {
	if len(allowedDirs) == 0 {
		if wd, err := os.Getwd(); err == nil {
			allowedDirs = []string{wd}
		} else {
			// #573-F: an empty dir list makes Allowed() fail open with no
			// trace. Record the failure and fail closed instead.
			debug.Log("permission", "PathSandbox: os.Getwd failed (%v); sandbox will fail closed", err)
			return &PathSandbox{getwdFailed: true}
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
		if s.getwdFailed {
			// #573-F: fail closed — the sandbox could not be established, so
			// silently allowing all paths would be an invisible security hole.
			debug.Log("permission", "PathSandbox: denying %q — sandbox unavailable (os.Getwd failed)", path)
			return false
		}
		return true // no restrictions
	}

	// Relative paths are anchored at the first allowed directory, not the
	// process CWD (#551-F). Since #542, file tools resolve relative paths
	// against their WorkingDir (the workspace root), so the sandbox must
	// judge the same path the tool will actually touch; anchoring at the
	// process CWD judged a different file, producing false Deny/Allow
	// verdicts whenever WorkingDir != process CWD (worktrees, sub-agents).
	// Per-agent policies are constructed with the agent's working directory
	// as allowedDirs[0], so this reproduces the tool-side resolution.
	// Absolute paths (the other caller, AllowedPathForTool) are unaffected.
	// A POSIX-rooted path ("/etc/passwd") is not absolute on Windows
	// (filepath.IsAbs requires a drive or UNC prefix), so it used to fall
	// into the relative-path anchoring below and got joined under the
	// sandbox root ("C:\...\sandbox\etc\passwd") — every POSIX-style path
	// then passed the containment check and the mode-level Deny/Ask gates
	// never fired. Treat a leading "/" as rooted: resolvePath's
	// filepath.Abs maps it to the current drive root ("C:\etc\passwd"),
	// which correctly fails the allowed-dir prefix compare. On Unix this
	// clause is unreachable for such paths (IsAbs already true).
	if !filepath.IsAbs(path) && !strings.HasPrefix(path, "/") {
		path = filepath.Join(s.allowedDirs[0], path)
	}

	resolved := resolvePath(path)
	if resolved == "" {
		return false
	}

	for _, dir := range s.allowedDirs {
		// #713: case-insensitive platforms (macOS APFS, Windows NTFS) compare
		// paths case-insensitively, but filepath.EvalSymlinks does NOT rewrite
		// to the real-case spelling. A wrong-case variant of the workspace dir
		// then failed the byte-exact prefix compare in BOTH the existing and
		// non-existing path cases — a false Deny in auto mode, false Ask
		// elsewhere. Segment-wise ASCII fold (pathFoldActive-aware) instead;
		// case-sensitive platforms keep byte equality.
		if pathHasPrefixFold(resolved, dir+string(os.PathSeparator)) || pathEqualFold(resolved, dir) {
			return true
		}
	}
	return false
}

// AllowedDirs returns the list of allowed directories.
func (s *PathSandbox) AllowedDirs() []string {
	return s.allowedDirs
}
