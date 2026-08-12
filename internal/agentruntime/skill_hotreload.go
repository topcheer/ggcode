package agentruntime

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"time"

	"github.com/topcheer/ggcode/internal/commands"
	"github.com/topcheer/ggcode/internal/debug"
	"github.com/topcheer/ggcode/internal/safego"
)

// fileEntry pairs a watched file path with its mtime.
type fileEntry struct {
	path  string
	mtime int64
}

// SkillHotReload watches skill/command directories for filesystem changes and
// triggers a reload of the commands.Manager so that edited/added/removed skills
// take effect immediately without restarting the session.
//
// It uses polling (no fsnotify dependency) with a configurable interval and a
// debounce window to coalesce rapid writes (e.g. editors that write via temp
// file + rename). Change detection is two-phase:
//  1. A cheap mtime-based signature over watched directories (catches structural
//     changes and content edits without reading file bodies on every poll).
//  2. When the signature changes, manager.Reload() re-reads files from disk and
//     applies the update (manager.Reload already has its own content signature
//     to avoid redundant map swaps).
type SkillHotReload struct {
	manager  *commands.Manager
	dirs     []string
	lastSig  string
	interval time.Duration
}

// NewSkillHotReload creates a watcher for the given command directories.
func NewSkillHotReload(mgr *commands.Manager, dirs []string) *SkillHotReload {
	return &SkillHotReload{
		manager:  mgr,
		dirs:     append([]string(nil), dirs...),
		interval: 5 * time.Second,
	}
}

// Start launches the watcher goroutine. It returns immediately.
// The watcher runs until ctx is cancelled.
func (w *SkillHotReload) Start(ctx context.Context) {
	if w == nil || w.manager == nil || len(w.dirs) == 0 {
		return
	}
	w.lastSig = w.computeSignature()

	safego.Go("skill.hotreload", func() {
		debug.Log("skill-hotreload", "watching %d skill dir(s) (interval=%v)", len(w.dirs), w.interval)
		ticker := time.NewTicker(w.interval)
		defer ticker.Stop()
		for {
			select {
			case <-ctx.Done():
				debug.Log("skill-hotreload", "watcher stopped")
				return
			case <-ticker.C:
				w.checkAndReload(ctx)
			}
		}
	})
}

func (w *SkillHotReload) checkAndReload(ctx context.Context) {
	if ctx.Err() != nil {
		return
	}
	sig := w.computeSignature()
	if sig == w.lastSig {
		return
	}

	// Debounce: wait a short window to let multi-write operations settle
	// (e.g. an editor writing frontmatter then body in two passes).
	time.Sleep(500 * time.Millisecond)
	if ctx.Err() != nil {
		return
	}

	w.lastSig = w.computeSignature()

	if w.manager.Reload() {
		debug.Log("skill-hotreload", "skill change detected, reloaded")
	}
}

// computeSignature builds a cheap mtime-based fingerprint of all watched skill
// directories and their .md command files. A change in any file's mtime, the
// addition/removal of a file, or a directory mtime change produces a different
// signature, triggering a reload. It does NOT read file bodies.
func (w *SkillHotReload) computeSignature() string {
	var entries []fileEntry
	for _, dir := range w.dirs {
		// Skip directory mtime — only watch file-level mtimes to avoid
		// false positives from OS-level directory metadata changes.
		collectFileEntries(dir, &entries)
	}
	sort.Slice(entries, func(i, j int) bool { return entries[i].path < entries[j].path })

	var b []byte
	for _, e := range entries {
		b = append(b, e.path...)
		b = append(b, '|')
		b = append(b, strconv.FormatInt(e.mtime, 10)...)
		b = append(b, '\n')
	}
	return string(b)
}

// collectFileEntries scans dir for skill subdirectories (SKILL.md) and legacy
// command files (*.md directly in the dir), appending their mtimes.
// Directory mtimes are excluded from the signature because macOS can update
// them for reasons unrelated to content (Spotlight indexing, attribute writes).
func collectFileEntries(dir string, entries *[]fileEntry) {
	dirEntries, err := os.ReadDir(dir)
	if err != nil {
		return
	}
	for _, de := range dirEntries {
		full := filepath.Join(dir, de.Name())
		if de.IsDir() {
			// Skill directory: watch the SKILL.md inside it.
			skillFile := filepath.Join(full, "SKILL.md")
			if info, statErr := os.Stat(skillFile); statErr == nil {
				*entries = append(*entries, fileEntry{path: skillFile, mtime: info.ModTime().UnixNano()})
			}
			continue
		}
		// Legacy command file (*.md directly in a commands dir).
		if filepath.Ext(de.Name()) == ".md" {
			if info, statErr := os.Stat(full); statErr == nil {
				*entries = append(*entries, fileEntry{path: full, mtime: info.ModTime().UnixNano()})
			}
		}
	}
}

// formatSigLine is a small helper used by tests to inspect the signature format.
func formatSigLine(path string, mtime int64) string {
	return fmt.Sprintf("%s|%d\n", path, mtime)
}
