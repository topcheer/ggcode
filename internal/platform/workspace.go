package platform

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"
)

// ErrWorkspaceNotAllowed is returned when a requested path is not inside any
// whitelisted workspace root. Uniform wording avoids leaking which roots exist.
var ErrWorkspaceNotAllowed = errors.New("platform: workspace not allowed")

// WorkspaceSet is the workspace allowlist (workspaces.json). Jobs may only
// run inside these roots - the server-side replacement for a remote cloud VM.
type WorkspaceSet struct {
	mu    sync.Mutex
	path  string
	Roots []string `json:"roots"` // absolute, symlink-resolved
}

// LoadWorkspaceSet loads the allowlist; missing file => empty set.
func LoadWorkspaceSet(path string) (*WorkspaceSet, error) {
	w := &WorkspaceSet{path: path}
	b, err := os.ReadFile(path)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return w, nil
		}
		return nil, fmt.Errorf("platform: read workspaces: %w", err)
	}
	if err := json.Unmarshal(b, w); err != nil {
		return nil, fmt.Errorf("platform: parse workspaces %s: %w", path, err)
	}
	return w, nil
}

// Save persists the allowlist.
func (w *WorkspaceSet) Save() error {
	w.mu.Lock()
	defer w.mu.Unlock()
	if err := os.MkdirAll(filepath.Dir(w.path), 0o700); err != nil {
		return fmt.Errorf("platform: workspace dir: %w", err)
	}
	b, err := json.MarshalIndent(w, "", "  ")
	if err != nil {
		return err
	}
	if err := os.WriteFile(w.path, b, 0o600); err != nil {
		return fmt.Errorf("platform: write workspaces: %w", err)
	}
	return nil
}

// Add whitelists an existing directory (stored symlink-resolved and absolute).
func (w *WorkspaceSet) Add(root string) error {
	abs, err := resolveDir(root)
	if err != nil {
		return err
	}
	w.mu.Lock()
	defer w.mu.Unlock()
	for _, r := range w.Roots {
		if r == abs {
			return fmt.Errorf("platform: workspace %q already registered", abs)
		}
	}
	w.Roots = append(w.Roots, abs)
	return nil
}

// Remove drops a root (compare on the resolved form).
func (w *WorkspaceSet) Remove(root string) error {
	abs, err := resolveDir(root)
	if err != nil {
		return err
	}
	w.mu.Lock()
	defer w.mu.Unlock()
	for i, r := range w.Roots {
		if r == abs {
			w.Roots = append(w.Roots[:i], w.Roots[i+1:]...)
			return nil
		}
	}
	return fmt.Errorf("platform: workspace %q not registered", abs)
}

// List returns a copy of the roots.
func (w *WorkspaceSet) List() []string {
	w.mu.Lock()
	defer w.mu.Unlock()
	out := make([]string, len(w.Roots))
	copy(out, w.Roots)
	return out
}

// Resolve validates that dir exists, is a directory and sits inside one of
// the whitelisted roots (symlink-resolved on both sides). Returns the
// resolved absolute path for job execution.
func (w *WorkspaceSet) Resolve(dir string) (string, error) {
	abs, err := resolveDir(dir)
	if err != nil {
		return "", err
	}
	w.mu.Lock()
	defer w.mu.Unlock()
	for _, root := range w.Roots {
		rel, err := filepath.Rel(root, abs)
		if err != nil {
			continue
		}
		if rel == "." || !(rel == ".." || strings.HasPrefix(rel, ".."+string(filepath.Separator))) {
			return abs, nil
		}
	}
	return "", ErrWorkspaceNotAllowed
}

// resolveDir cleans, absolutizes, verifies existence and resolves symlinks.
// On Windows EvalSymlinks is a no-op for plain paths, which is fine.
func resolveDir(dir string) (string, error) {
	dir = strings.TrimSpace(dir)
	if dir == "" {
		return "", errors.New("platform: empty workspace path")
	}
	if !filepath.IsAbs(dir) {
		return "", fmt.Errorf("platform: workspace path must be absolute: %s", dir)
	}
	abs, err := filepath.Abs(dir)
	if err != nil {
		return "", fmt.Errorf("platform: resolve workspace: %w", err)
	}
	real, err := filepath.EvalSymlinks(abs)
	if err != nil {
		return "", fmt.Errorf("platform: workspace %s: %w", dir, err)
	}
	fi, err := os.Stat(real)
	if err != nil {
		return "", fmt.Errorf("platform: workspace %s: %w", dir, err)
	}
	if !fi.IsDir() {
		return "", fmt.Errorf("platform: workspace %s is not a directory", dir)
	}
	return real, nil
}
