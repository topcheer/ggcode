package memory

import (
	"fmt"
	"os"
	"path/filepath"
	"time"

	"github.com/topcheer/ggcode/internal/debug"
)

// GCStats holds statistics from a garbage collection run.
// All counts are informational; GC never fails the session.
type GCStats struct {
	ExpiredRemoved int // transient files past their TTL that were deleted from disk
	DedupRemoved   int // superseded evolving files that were deleted from disk
	Total          int // total files scanned before GC
}

// GarbageCollect physically removes memory files that the curation logic
// considers expired or deduped. The existing LoadAll/LoadIndex/LoadForPrompt
// methods already filter these at load time, so this method is purely a disk
// cleanup optimisation — it prevents the memory directory from growing without
// bound across hundreds of sessions.
//
// GarbageCollect is safe to call at startup or periodically. It logs results
// via the debug package and never returns an error (disk cleanup is
// best-effort).
func (am *AutoMemory) GarbageCollect() GCStats {
	metas, err := am.collectMetas()
	if err != nil {
		debug.Log("memory", "GC: failed to read dir %s: %v", am.dir, err)
		return GCStats{}
	}

	now := time.Now()
	active, _, _, _ := curateEntries(metas, now)

	// Build a set of keys that survived curation.
	activeKeys := make(map[string]bool, len(active))
	for _, m := range active {
		activeKeys[m.Key] = true
	}

	stats := GCStats{Total: len(metas)}

	for _, m := range metas {
		if activeKeys[m.Key] {
			continue
		}
		path := filepath.Join(am.dir, m.Key+".md")
		if err := os.Remove(path); err != nil {
			debug.Log("memory", "GC: failed to remove %s: %v", path, err)
			continue
		}
		if m.Category == CategoryTransient {
			stats.ExpiredRemoved++
		} else if m.Category == CategoryEvolving {
			stats.DedupRemoved++
		}
	}

	removed := stats.ExpiredRemoved + stats.DedupRemoved
	if removed > 0 {
		debug.Log("memory", "GC: removed %d files (%d expired, %d deduped) from %s",
			removed, stats.ExpiredRemoved, stats.DedupRemoved, am.dir)
	}

	return stats
}

// DeleteMemory removes a single memory entry by key. Returns an error if the
// file does not exist or cannot be removed.
func (am *AutoMemory) DeleteMemory(key string) error {
	// #775: must resolve the filename exactly like SaveMemory, otherwise the
	// hash-suffixed file written for a non-injective key can never be deleted.
	safe := disambiguateKey(key, sanitizeKey(key))
	path := filepath.Join(am.dir, safe+".md")
	if _, err := os.Stat(path); os.IsNotExist(err) {
		return fmt.Errorf("memory %q not found", key)
	}
	return os.Remove(path)
}

// GCFormatSummary returns a human-readable summary of GC results.
func (s GCStats) String() string {
	if s.ExpiredRemoved == 0 && s.DedupRemoved == 0 {
		return fmt.Sprintf("memory GC: %d files scanned, 0 removed", s.Total)
	}
	return fmt.Sprintf("memory GC: %d files scanned, %d removed (%d expired, %d deduped)",
		s.Total, s.ExpiredRemoved+s.DedupRemoved, s.ExpiredRemoved, s.DedupRemoved)
}
