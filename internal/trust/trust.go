// Package trust implements a workspace trust gate for ggcode.
//
// Frontier agent harnesses (VS Code Workspace Trust, Claude Code, Cursor)
// treat an untrusted checkout as a prompt-injection / code-execution vector:
// a cloned repository can ship project-scoped skills, slash commands, MCP
// server definitions, and AGENTS.md-style memory that ggcode would otherwise
// auto-load at startup. This package answers a single question for every
// project-scoped loader: "has the user explicitly trusted this folder?"
//
// Design (2026 HITL approval-flow pattern):
//   - fail-closed: an unknown folder is untrusted; loaders stay restricted
//   - nearest-ancestor-wins: trusting a parent covers child worktrees
//   - one-time interactive approval surface (TUI prompt / `ggcode trust`)
//   - headless (pipe) runs default to restricted with an explicit warning
package trust

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"github.com/topcheer/ggcode/internal/config"
)

// Entry records a trust decision for one absolute directory path.
type Entry struct {
	Path      string `json:"path"`
	Trusted   bool   `json:"trusted"`
	TrustedAt string `json:"trusted_at,omitempty"`
	RevokedAt string `json:"revoked_at,omitempty"`
	Host      string `json:"host,omitempty"`
}

// store is the on-disk document at ~/.ggcode/trust.json.
// Entries are keyed by cleaned absolute path so renames of the same
// directory keep working when only case or trailing separators change.
type store struct {
	Version  int              `json:"version"`
	Entries  map[string]Entry `json:"entries"`
	Upgraded string           `json:"upgraded,omitempty"`
}

const storeVersion = 1

// StorePath returns the path of the trust store file.
func StorePath() string {
	return filepath.Join(config.HomeDir(), ".ggcode", "trust.json")
}

func loadStore() (*store, error) {
	path := StorePath()
	data, err := os.ReadFile(path)
	if err != nil {
		if os.IsNotExist(err) {
			return &store{Version: storeVersion, Entries: map[string]Entry{}}, nil
		}
		return nil, err
	}
	var s store
	if err := json.Unmarshal(data, &s); err != nil {
		// A corrupt store must never fail-closed the whole CLI with an
		// error loop; treat as empty but preserve the file for inspection.
		return &store{Version: storeVersion, Entries: map[string]Entry{}, Upgraded: "corrupt"}, nil
	}
	if s.Entries == nil {
		s.Entries = map[string]Entry{}
	}
	s.Version = storeVersion
	return &s, nil
}

func (s *store) save() error {
	path := StorePath()
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return err
	}
	data, err := json.MarshalIndent(s, "", "  ")
	if err != nil {
		return err
	}
	data = append(data, '\n')
	tmp := path + ".tmp"
	if err := os.WriteFile(tmp, data, 0o600); err != nil {
		return err
	}
	return os.Rename(tmp, path)
}

// Normalize canonicalizes a directory path for use as a store key.
func Normalize(dir string) string {
	if dir == "" {
		return ""
	}
	abs, err := filepath.Abs(dir)
	if err != nil {
		abs = dir
	}
	abs = filepath.Clean(abs)
	// EvalSymlinks keeps macOS /tmp vs /private/tmp and similar aliasing
	// from creating duplicate entries. Missing dirs fall back to abs.
	if resolved, err := filepath.EvalSymlinks(abs); err == nil {
		abs = resolved
	}
	return abs
}

// Trust marks dir (and, by nearest-ancestor inheritance, its subfolders)
// as trusted.
func Trust(dir string) error {
	path := Normalize(dir)
	if path == "" {
		return fmt.Errorf("trust: empty directory")
	}
	s, err := loadStore()
	if err != nil {
		return err
	}
	entry := Entry{
		Path:      path,
		Trusted:   true,
		TrustedAt: time.Now().UTC().Format(time.RFC3339),
		Host:      hostLabel(),
	}
	s.Entries[path] = entry
	return s.save()
}

// Untrust records an explicit revocation for dir. Revoking a child of a
// trusted parent keeps the child restricted (nearest-ancestor-wins).
func Untrust(dir string) error {
	path := Normalize(dir)
	if path == "" {
		return fmt.Errorf("trust: empty directory")
	}
	s, err := loadStore()
	if err != nil {
		return err
	}
	s.Entries[path] = Entry{
		Path:      path,
		Trusted:   false,
		RevokedAt: time.Now().UTC().Format(time.RFC3339),
		Host:      hostLabel(),
	}
	return s.save()
}

// Forget removes dir's entry entirely (back to inherit-from-parent).
func Forget(dir string) error {
	path := Normalize(dir)
	if path == "" {
		return fmt.Errorf("trust: empty directory")
	}
	s, err := loadStore()
	if err != nil {
		return err
	}
	delete(s.Entries, path)
	return s.save()
}

// IsTrusted reports whether dir is trusted, honoring nearest-ancestor
// inheritance: walking upward from dir, the first directory with a stored
// entry decides. A missing or unreadable store is untrusted (fail-closed).
func IsTrusted(dir string) bool {
	state, _ := Decide(dir)
	return state == StateTrusted
}

// State is the trust decision for dir.
type State string

const (
	// StateTrusted: an explicit grant covers dir (itself or an ancestor).
	StateTrusted State = "trusted"
	// StateRestricted: no decision covers dir, or a revocation does.
	StateRestricted State = "restricted"
)

// Decide returns the trust decision plus the entry that decided it
// ("" when no entry applies).
func Decide(dir string) (State, Entry) {
	path := Normalize(dir)
	if path == "" {
		return StateRestricted, Entry{}
	}
	s, err := loadStore()
	if err != nil {
		return StateRestricted, Entry{}
	}
	for {
		if entry, ok := s.Entries[path]; ok {
			if entry.Trusted {
				return StateTrusted, entry
			}
			return StateRestricted, entry
		}
		parent := filepath.Dir(path)
		if parent == path || parent == "." || parent == string(filepath.Separator) || parent == "" {
			break
		}
		path = parent
	}
	return StateRestricted, Entry{}
}

// Entries returns all stored decisions sorted by path.
func Entries() ([]Entry, error) {
	s, err := loadStore()
	if err != nil {
		return nil, err
	}
	out := make([]Entry, 0, len(s.Entries))
	for _, entry := range s.Entries {
		out = append(out, entry)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Path < out[j].Path })
	return out, nil
}

func hostLabel() string {
	host, err := os.Hostname()
	if err != nil || strings.TrimSpace(host) == "" {
		return "unknown"
	}
	return host
}

// projectPromptAssets lists prompt files a cloned repository can ship that
// ggcode auto-loads as project memory. Together with .ggcode/skills,
// .ggcode/commands and .mcp.json they form the project trust surface.
var projectPromptAssets = []string{"GGCODE.md", "AGENTS.md", "CLAUDE.md", "COPILOT.md"}

// NeedsTrustPrompt reports whether dir is restricted AND carries
// project-scoped assets whose auto-load the trust gate suppresses.
// The approval surface uses this to avoid prompting in plain directories
// that ship nothing project-scoped.
func NeedsTrustPrompt(dir string) bool {
	if IsTrusted(dir) {
		return false
	}
	root := Normalize(dir)
	if root == "" {
		return false
	}
	for _, rel := range []string{
		filepath.Join(".ggcode", "skills"),
		filepath.Join(".ggcode", "commands"),
		".mcp.json",
	} {
		if _, err := os.Stat(filepath.Join(root, rel)); err == nil {
			return true
		}
	}
	for _, name := range projectPromptAssets {
		if _, err := os.Stat(filepath.Join(root, name)); err == nil {
			return true
		}
	}
	return false
}
