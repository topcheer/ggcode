package agent

// speculate_persist.go -- Cross-session persistence for the speculative
// tool-execution bigram model (r486).
//
// Gap closed (cron-runner r486 audit): speculate.go's learned tool-A->tool-B
// transition counts lived only in process memory, so every cold start
// re-climbed from zero and adaptiveMinCount restarted conservative - the
// first ~10 tool calls of each session had systematically low speculative
// hit rates (per-session warmup regression, user-perceivable latency).
//
// Design (mirrors provider/adaptive_cap.go persistence conventions):
//   - Load once per process (sync.Once); every newSpeculator() merges the
//     loaded counts into its fresh pattern map, so the first prediction of
//     the very first run can already fire.
//   - Save is throttled (specPersistInterval) and triggered at run end;
//     writes top-N transitions atomically (tmp file + rename).
//   - All failures are silent best-effort: speculation is an optimization
//     and must never break a run. Multi-process writers are
//     last-writer-wins - counts are heuristic, not authoritative.
//   - Stored payload is tool-name pairs and integer counts only: no user
//     content, no file paths, no secrets.

import (
	"encoding/json"
	"os"
	"path/filepath"
	"sort"
	"sync"
	"time"

	"github.com/topcheer/ggcode/internal/config"
	"github.com/topcheer/ggcode/internal/debug"
)

const (
	// specPersistDir is the cache subdirectory under ConfigDir().
	specPersistDir = "cache"
	// specPersistFile holds the JSON-encoded bigram transition counts.
	specPersistFile = "speculate_patterns.json"
	// specPersistMaxTransitions bounds the stored model: keep the most
	// observed prev->next edges, drop the long tail.
	specPersistMaxTransitions = 500
	// specPersistInterval throttles disk writes: at most one save per
	// window regardless of how many runs end inside it.
	specPersistInterval = 5 * time.Minute
)

var (
	specLoadOnce sync.Once
	// specPersistedPatterns is the process-wide model loaded from disk
	// (nil when the file is absent or unreadable).
	specPersistedPatterns map[string]map[string]int
	specPersistMu         sync.Mutex
	specLastPersist       time.Time
)

// specPatternsPath returns the persistence file location.
func specPatternsPath() string {
	return filepath.Join(config.ConfigDir(), specPersistDir, specPersistFile)
}

// safeSpecPatternsPath defuses the ConfigDir testguard: in test binaries
// with an un-isolated HOME, ConfigDir panics by design (it must not touch
// the real user home). Speculation persistence is optional - recover the
// panic and report "unavailable" so un-isolated legacy tests that merely
// construct an Agent (newSpeculator -> load) or end a RunStream (deferred
// save) are unaffected. Production is never guarded, so behavior there is
// unchanged.
func safeSpecPatternsPath() (path string, ok bool) {
	defer func() {
		if r := recover(); r != nil {
			path, ok = "", false
		}
	}()
	return specPatternsPath(), true
}

// loadSpecPatternsOnce performs the process-wide disk load exactly once.
// A missing file or malformed JSON degrades to an empty model.
func loadSpecPatternsOnce() {
	specLoadOnce.Do(func() {
		path, ok := safeSpecPatternsPath()
		if !ok {
			return
		}
		data, err := os.ReadFile(path)
		if err != nil {
			return // absent on first use - normal, not an error
		}
		var loaded map[string]map[string]int
		if err := json.Unmarshal(data, &loaded); err != nil {
			debug.Log("speculate", "persist: corrupt %s, starting fresh: %v", specPersistFile, err)
			return
		}
		specPersistedPatterns = loaded
		debug.Log("speculate", "persist: loaded %d pattern roots from %s", len(loaded), specPersistFile)
	})
}

// mergePersistedPatterns seeds a freshly constructed speculator with the
// process-wide persisted counts. Called from newSpeculator.
func mergePersistedPatterns(s *speculator) {
	loadSpecPatternsOnce()
	if specPersistedPatterns == nil {
		return
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	for prev, nexts := range specPersistedPatterns {
		if s.patterns[prev] == nil {
			s.patterns[prev] = make(map[string]int)
		}
		for next, cnt := range nexts {
			if cnt < 1 {
				continue
			}
			s.patterns[prev][next] += cnt
		}
	}
}

// resetSpecPersistForTest re-arms the process-wide load state. Test-only:
// specLoadOnce is deliberately one-shot in production (one disk read per
// process), which makes any test that varies the on-disk file
// order-dependent on whichever test ran first.
func resetSpecPersistForTest() {
	specPersistMu.Lock()
	defer specPersistMu.Unlock()
	specLastPersist = time.Time{}
	specPersistedPatterns = nil
	specLoadOnce = sync.Once{}
}

// maybePersistSpecPatterns saves the model to disk at most once per
// specPersistInterval. Called at run end; failures are silent.
func maybePersistSpecPatterns(s *speculator) {
	// #3594: the nil guard must precede the throttle window. Advancing
	// specLastPersist before the s == nil check burned a full window on
	// every nil-receiver call, contradicting the documented semantics
	// ("at most one save per window" - a save that can never happen must
	// not consume the window).
	if s == nil {
		return
	}
	specPersistMu.Lock()
	if time.Since(specLastPersist) < specPersistInterval {
		specPersistMu.Unlock()
		return
	}
	specLastPersist = time.Now()
	specPersistMu.Unlock()

	s.mu.Lock()
	snapshot := make(map[string]map[string]int, len(s.patterns))
	for prev, nexts := range s.patterns {
		cp := make(map[string]int, len(nexts))
		for next, cnt := range nexts {
			cp[next] = cnt
		}
		snapshot[prev] = cp
	}
	s.mu.Unlock()

	trimmed := topTransitions(snapshot, specPersistMaxTransitions)

	path, ok := safeSpecPatternsPath()
	if !ok {
		return
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return
	}
	data, err := json.Marshal(trimmed)
	if err != nil {
		return
	}
	tmp := path + ".tmp"
	if err := os.WriteFile(tmp, data, 0o644); err != nil {
		return
	}
	if err := os.Rename(tmp, path); err != nil {
		_ = os.Remove(tmp)
		return
	}
	debug.Log("speculate", "persist: saved %d roots to %s", len(trimmed), path)
}

// topTransitions keeps the highest-count edges overall until at most limit
// remain. Root maps whose edges are all trimmed away are dropped.
func topTransitions(patterns map[string]map[string]int, limit int) map[string]map[string]int {
	type edge struct {
		prev string
		next string
		cnt  int
	}
	var edges []edge
	for prev, nexts := range patterns {
		for next, cnt := range nexts {
			edges = append(edges, edge{prev, next, cnt})
		}
	}
	if len(edges) <= limit {
		return patterns
	}
	sort.Slice(edges, func(i, j int) bool { return edges[i].cnt > edges[j].cnt })
	out := make(map[string]map[string]int)
	for _, e := range edges[:limit] {
		if out[e.prev] == nil {
			out[e.prev] = make(map[string]int)
		}
		out[e.prev][e.next] = e.cnt
	}
	return out
}
