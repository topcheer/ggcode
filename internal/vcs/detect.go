package vcs

import (
	"os"
	"path/filepath"
)

// Detect identifies the VCS used in the given working directory by walking up
// the directory tree looking for well-known metadata directories/files.
// Returns nil if no known VCS is found.
func Detect(workingDir string) VCS {
	dir := workingDir
	for {
		// Git
		if info, err := os.Stat(filepath.Join(dir, ".git")); err == nil {
			// .git can be a directory (normal) or a file (worktree/submodule)
			_ = info
			return Git{}
		}
		// Mercurial
		if _, err := os.Stat(filepath.Join(dir, ".hg")); err == nil {
			return Mercurial{}
		}
		// Subversion
		if _, err := os.Stat(filepath.Join(dir, ".svn")); err == nil {
			return Subversion{}
		}
		// Jujutsu (can co-exist with git, so check after git)
		if _, err := os.Stat(filepath.Join(dir, ".jj")); err == nil {
			// jj usually wraps a git repo; prefer git if both exist.
			// Only return jj if there's no .git.
			return Jujutsu{}
		}

		parent := filepath.Dir(dir)
		if parent == dir {
			break // reached filesystem root
		}
		dir = parent
	}
	return nil
}

// DetectOrGit is like Detect but falls back to Git{} when no VCS is found,
// so that callers always get a non-nil VCS (commands will fail naturally if
// the directory is not actually a repo).
func DetectOrGit(workingDir string) VCS {
	if v := Detect(workingDir); v != nil {
		return v
	}
	return Git{}
}
