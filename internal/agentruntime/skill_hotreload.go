package agentruntime

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
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
	manager         *commands.Manager
	dirs            []string
	lastSig         string
	lastContentHash string
	interval        time.Duration
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

	// Content hash check: mtime can change without content changes
	// (e.g. git checkout, Spotlight indexing, xattr writes).
	contentHash := w.computeContentHash()
	if contentHash == w.lastContentHash {
		w.lastSig = w.computeSignature()
		return
	}

	w.lastSig = w.computeSignature()
	w.lastContentHash = contentHash

	if w.manager.Reload() {
		debug.Log("skill-hotreload", "skill change detected, reloaded")
	}
}

// computeSignature builds a cheap mtime-based fingerprint of all watched skill
// directories and their .md command files. A change in any file's mtime, the
// addition/removal of a file, or a directory mtime change produces a different
// signature, triggering a reload. It does NOT read file bodies.
func (w *SkillHotReload) computeSignature() string {
	var paths []string
	for _, dir := range w.dirs {
		collectFilePaths(dir, &paths)
	}
	sort.Strings(paths)

	var b []byte
	for _, p := range paths {
		info, err := os.Stat(p)
		if err != nil {
			continue
		}
		b = append(b, p...)
		b = append(b, '|')
		b = append(b, strconv.FormatInt(info.ModTime().UnixNano(), 10)...)
		b = append(b, '\n')
	}
	return string(b)
}

// collectFilePaths gathers all watched file paths (SKILL.md and legacy *.md).
func collectFilePaths(dir string, paths *[]string) {
	for _, de := range dirEntries(dir) {
		full := filepath.Join(dir, de.Name())
		if de.IsDir() {
			skillFile := filepath.Join(full, "SKILL.md")
			if _, err := os.Stat(skillFile); err == nil {
				*paths = append(*paths, skillFile)
			}
			continue
		}
		if filepath.Ext(de.Name()) == ".md" {
			*paths = append(*paths, full)
		}
	}
}

func dirEntries(dir string) []os.DirEntry {
	entries, err := os.ReadDir(dir)
	if err != nil {
		return nil
	}
	return entries
}

// computeContentHash reads all watched skill files and produces a SHA-256
// hash of their concatenated content. This is used as a secondary check:
// only trigger a reload when file contents actually change, not just mtimes.
func (w *SkillHotReload) computeContentHash() string {
	var paths []string
	for _, dir := range w.dirs {
		collectFilePaths(dir, &paths)
	}
	if len(paths) == 0 {
		return ""
	}
	sort.Strings(paths)
	h := sha256.New()
	for _, p := range paths {
		data, err := os.ReadFile(p)
		if err != nil {
			continue
		}
		h.Write(data)
	}
	return hex.EncodeToString(h.Sum(nil))
}
