package memory

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sync"
	"time"

	"github.com/topcheer/ggcode/internal/debug"
)

// Usage & provenance tracking for the memory store (sa-85).
//
// Research basis:
//   - "Agent Zero Memory: Provenance-Aware Long-Term Memory for LLM
//     Agents" (arXiv:2608.29606). Every stored fact should carry a
//     traceable origin (who wrote it, when) and a usage signal, so the
//     agent can reason about WHY it should trust a memory instead of
//     treating the store as an undifferentiated blob.
//   - mem0, "State of AI Agent Memory 2026". Production memory systems
//     converge on a usage-feedback loop: injection events are counted,
//     and hit-rate stats drive curation (what to surface, what to
//     retire). ggcode's store previously had neither.
//
// Design:
//   - Sidecar state file .usage.json lives next to the .md files. It is
//     dot-prefixed so collectMetas/List ignore it.
//   - Provenance: the first write of a key records its SOURCE (who
//     created it: the save_memory tool, the reflection daemon, ...).
//     The source never changes on subsequent overwrites of the same key.
//   - Usage: every prompt injection (LoadIndex / LoadForPrompt) counts
//     as one "use" for the injected keys, debounced to at most one
//     record per key per usageDebounce window so prompt-refresh storms
//     (SetAfterSave rebuilds) don't inflate the counters.
//   - HealthReport surfaces never-used entries; the index shown to the
//     model carries a compact provenance suffix for used entries.

// usageInfo is the per-key provenance/usage record.
type usageInfo struct {
	FirstSeen time.Time `json:"first_seen,omitempty"`
	LastUsed  time.Time `json:"last_used,omitempty"`
	Uses      int       `json:"uses,omitempty"`
	Source    string    `json:"source,omitempty"`
}

// usageIndex is the sidecar document. Keyed by the sanitized filename
// base (identical to MemoryMeta.Key), stable across sessions.
type usageIndex struct {
	Version int                   `json:"version"`
	Entries map[string]*usageInfo `json:"entries"`
}

// usageFileName is the sidecar state file inside am.dir.
const usageFileName = ".usage.json"

// usageVersion is the current sidecar schema version.
const usageVersion = 1

// usageProvenanceUnknown replaces a missing source label.
const usageProvenanceUnknown = "unknown"

// usageDebounce suppresses repeated use-records for the same key within
// one process. Prompt refreshes rebuild the index on every save; without
// debounce a single burst of saves would multiply the counters.
const usageDebounce = 10 * time.Minute

func (am *AutoMemory) usagePath() string {
	return filepath.Join(am.dir, usageFileName)
}

// loadUsage reads the sidecar. A missing or corrupt file yields an empty
// (non-nil) index - provenance is best-effort telemetry, never a hard
// dependency of the memory store.
func (am *AutoMemory) loadUsage() usageIndex {
	idx := usageIndex{Version: usageVersion, Entries: make(map[string]*usageInfo)}
	data, err := os.ReadFile(am.usagePath())
	if err != nil {
		return idx
	}
	var parsed usageIndex
	if err := json.Unmarshal(data, &parsed); err != nil || parsed.Entries == nil {
		debug.Log("memory", "usage sidecar unreadable, resetting: %v", err)
		return idx
	}
	idx.Entries = parsed.Entries
	return idx
}

// saveUsage atomically persists the sidecar (temp+rename, same pattern
// as SaveMemory so concurrent readers never see a torn file).
func (am *AutoMemory) saveUsage(idx usageIndex) error {
	data, err := json.Marshal(idx)
	if err != nil {
		return err
	}
	path := am.usagePath()
	tmp, err := os.CreateTemp(am.dir, usageFileName+".tmp-*")
	if err != nil {
		return err
	}
	tmpName := tmp.Name()
	if _, err := tmp.Write(data); err != nil {
		tmp.Close()
		os.Remove(tmpName)
		return err
	}
	if err := tmp.Close(); err != nil {
		os.Remove(tmpName)
		return err
	}
	if err := os.Chmod(tmpName, 0644); err != nil {
		os.Remove(tmpName)
		return err
	}

	muAny, _ := writeMu.LoadOrStore(path, &sync.Mutex{})
	mu := muAny.(*sync.Mutex)
	mu.Lock()
	defer mu.Unlock()
	if err := os.Rename(tmpName, path); err != nil {
		os.Remove(tmpName)
		return err
	}
	return nil
}

// RecordProvenance registers the ORIGIN of a memory key. Called from the
// save paths; the first registration wins and later overwrites of the
// same key keep the original source (provenance traces creation, not
// the latest edit).
func (am *AutoMemory) RecordProvenance(key, source string) {
	if source == "" {
		source = usageProvenanceUnknown
	}
	am.mu.Lock()
	defer am.mu.Unlock()
	idx := am.loadUsage()
	if rec, ok := idx.Entries[key]; ok && rec != nil {
		if rec.Source == "" {
			rec.Source = source // backfill legacy entries
		}
		if rec.FirstSeen.IsZero() {
			rec.FirstSeen = time.Now()
		}
	} else {
		now := time.Now()
		idx.Entries[key] = &usageInfo{FirstSeen: now, Source: source}
	}
	if err := am.saveUsage(idx); err != nil {
		debug.Log("memory", "usage provenance persist failed for %s: %v", key, err)
	}
}

// RecordUse counts one prompt-injection exposure of the given keys.
// Debounced per key per process via am.useOnce, so index/prompt rebuild
// storms within usageDebounce count once.
func (am *AutoMemory) RecordUse(keys []string, source string) {
	if len(keys) == 0 {
		return
	}
	now := time.Now()
	var fresh []string
	for _, k := range keys {
		if last, ok := am.useOnce.Load(k); ok {
			if now.Sub(last.(time.Time)) < usageDebounce {
				continue
			}
		}
		am.useOnce.Store(k, now)
		fresh = append(fresh, k)
	}
	if len(fresh) == 0 {
		return
	}

	am.mu.Lock()
	defer am.mu.Unlock()
	idx := am.loadUsage()
	for _, k := range fresh {
		rec := idx.Entries[k]
		if rec == nil {
			rec = &usageInfo{}
			idx.Entries[k] = rec
		}
		rec.Uses++
		rec.LastUsed = now
		if rec.FirstSeen.IsZero() {
			rec.FirstSeen = now
		}
		if rec.Source == "" {
			rec.Source = source
		}
	}
	if err := am.saveUsage(idx); err != nil {
		debug.Log("memory", "usage record persist failed: %v", err)
	}
}

// ForgetUsage drops the provenance/usage record when the key is deleted,
// so deleted keys cannot linger as ghost provenance entries.
func (am *AutoMemory) ForgetUsage(key string) {
	am.mu.Lock()
	defer am.mu.Unlock()
	idx := am.loadUsage()
	if _, ok := idx.Entries[key]; !ok {
		return
	}
	delete(idx.Entries, key)
	if err := am.saveUsage(idx); err != nil {
		debug.Log("memory", "usage forget persist failed for %s: %v", key, err)
	}
}

// UsageOf returns the recorded provenance/usage info for a key.
// Returns the zero value and false when the key has no record.
func (am *AutoMemory) UsageOf(key string) (usageInfo, bool) {
	idx := am.loadUsage()
	rec, ok := idx.Entries[key]
	if !ok || rec == nil {
		return usageInfo{}, false
	}
	return *rec, true
}

// provenanceSuffix renders the compact provenance marker appended to
// index lines for entries with recorded usage. Format stays under ~30
// bytes so 60 entries cost at most ~1.8KB of prompt budget.
func provenanceSuffix(info usageInfo) string {
	if info.Uses <= 0 {
		return ""
	}
	return fmt.Sprintf(" [uses=%d src=%s]", info.Uses, info.Source)
}
