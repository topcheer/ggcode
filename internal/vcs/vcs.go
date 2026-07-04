// Package vcs provides a unified abstraction over multiple version control
// systems (git, Mercurial, Subversion, Jujutsu). It auto-detects the VCS in a
// working directory and dispatches operations through a common interface.
package vcs

import (
	"context"
)

// VCS represents a version control system backend.
type VCS interface {
	// Name returns the short identifier: "git", "hg", "svn", "jj".
	Name() string

	// DisplayName returns a human-friendly name: "Git", "Mercurial", etc.
	DisplayName() string

	// Status returns the working-tree status as human-readable text.
	Status(ctx context.Context, dir string) (string, error)

	// Diff returns the diff. If cached is true, show staged changes only.
	// If file is non-empty, limit to that file.
	Diff(ctx context.Context, dir string, cached bool, file string) (string, error)

	// Log returns recent commit history. count limits the number of entries.
	Log(ctx context.Context, dir string, count int) (string, error)

	// Add stages the given files for commit.
	Add(ctx context.Context, dir string, files []string) (string, error)

	// Commit records a new commit with the given message.
	Commit(ctx context.Context, dir string, message string) (string, error)

	// CurrentBranch returns the current branch/bookmark/head name.
	CurrentBranch(ctx context.Context, dir string) (string, error)

	// IsClean reports whether the working tree has no uncommitted changes.
	IsClean(ctx context.Context, dir string) (bool, error)
}
