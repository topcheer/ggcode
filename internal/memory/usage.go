package memory

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sync"
	"time"

	"github.com/topcheer/ggcode/internal/debug"
	"github.com/topcheer/ggcode/internal/util"
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
	// Actor (r29) names the WRITER identity behind the source label -
	// "main" agent, a sub-agent id, a swarm teammate, a daemon. Source
	// says which subsystem wrote; Actor says who was running it. Empty =
	// legacy entries written before r29 (backfilled on next write).
	Actor string `json:"actor,omitempty"`
	// Outcome (sa-139, MemGuard arXiv:2608.21867) records the verification
	// signal of the run that LAST wrote this key: "success", "partial", or
	// "failed". Unlike Source/Actor it is overwrite-on-write semantics - a
	// re-saved key carries the new run's outcome. Empty = legacy entries
	// written before sa-139 (consumers treat as unknown, no bonus/penalty).
	Outcome string `json:"outcome,omitempty"`
	// Conflicts (sa-146, REALM arXiv:2609.33226 retrieval-driven
	// reconsolidation) counts high-confidence recall arbitration losses:
	// times this entry lost to a clearly-higher-trust contradicting entry
	// while being injected. Unlike Uses (positive retrieval signal) this is
	// a negative retrieval-time signal that feeds curation eviction order
	// and arbitration trust scoring. Debounced like Uses.
	Conflicts int `json:"conflicts,omitempty"`

	// Consumed (sa-147, LIMBO arXiv:2609.14138 inference-time memory
	// allocation) counts consumption events: run-end scans found this
	// entry's key or first-line fingerprint quoted in the assistant corpus
	// AFTER injection. Uses counts exposure; Consumed measures whether the
	// exposure paid off. The store-wide Consumed/Uses ratio adapts the
	// inline budget (consumption.go). Debounced like Uses.
	Consumed int `json:"consumed,omitempty"`
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
//
// #3120 lock contract: callers must hold the am.dir cross-process file
// lock (the auto.go SaveMemory path does - it acquires util.FileLock
// before calling this). That is what serializes this read-modify-write
// against cross-process RecordUse writers; do NOT call this from an
// unlocked path.
func (am *AutoMemory) RecordProvenance(key, source string) {
	am.RecordProvenanceActor(key, source, "")
}

// RecordProvenanceActor (r29) is RecordProvenance with the WRITER
// identity. First-write-wins applies per field: an existing entry keeps
// its original Source AND Actor (provenance traces creation), while
// legacy entries written before r29 get their empty Actor backfilled.
// Lock contract: same as RecordProvenance (#3120 - caller holds the
// am.dir cross-process file lock).
func (am *AutoMemory) RecordProvenanceActor(key, source, actor string) {
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
		if rec.Actor == "" && actor != "" {
			rec.Actor = actor // backfill legacy entries (r29)
		}
		if rec.FirstSeen.IsZero() {
			rec.FirstSeen = time.Now()
		}
	} else {
		now := time.Now()
		idx.Entries[key] = &usageInfo{FirstSeen: now, Source: source, Actor: actor}
	}
	if err := am.saveUsage(idx); err != nil {
		debug.Log("memory", "usage provenance persist failed for %s: %v", key, err)
	}
}

// RecordOutcome (sa-139, MemGuard) attaches the writing run's verification
// outcome to the sidecar record. Overwrite semantics: re-writing a key with
// new content makes the new run's outcome the authoritative signal.
func (am *AutoMemory) RecordOutcome(key, outcome string) {
	if outcome != "success" && outcome != "partial" && outcome != "failed" {
		return // unknown outcomes are simply not recorded
	}
	safe := disambiguateKey(key, sanitizeKey(key))
	// #3882: full read-modify-write of .usage.json under only am.mu loses a
	// concurrent writer's entry to last-writer-wins across processes. Every
	// sibling sidecar writer holds the #3120 cross-process lock
	// (FileLock outer, am.mu inner) - RecordOutcome was the lone exception.
	unlock, lockErr := util.FileLock(am.dir + ".lock")
	if lockErr != nil {
		debug.Log("memory", "usage lock unavailable, recording outcome unlocked: %v", lockErr)
	}
	defer func() {
		if unlock != nil {
			unlock()
		}
	}()
	am.mu.Lock()
	defer am.mu.Unlock()
	idx := am.loadUsage()
	rec, ok := idx.Entries[safe]
	if !ok || rec == nil {
		rec = &usageInfo{FirstSeen: time.Now()}
		idx.Entries[safe] = rec
	}
	if rec.Outcome == outcome {
		return
	}
	rec.Outcome = outcome
	if err := am.saveUsage(idx); err != nil {
		debug.Log("memory", "usage outcome persist failed for %s: %v", safe, err)
	}
}

// RecordConflictLoss (sa-146, REALM arXiv:2609.33226 retrieval-driven
// reconsolidation) persists one high-confidence arbitration loss per key:
// the retrieval episode itself revises the entry's standing instead of the
// verdict being use-and-forget. Debounced per key in the same window as
// RecordUse (separate "loss:" namespace so the two counters never fight
// over one debounce slot), and fail-open with a log like every sidecar
// write. Callers pass ALREADY-SANITIZED keys (MemoryEntry.Key).
func (am *AutoMemory) RecordConflictLoss(keys []string) {
	if len(keys) == 0 {
		return
	}
	now := time.Now()
	var fresh []string
	for _, k := range keys {
		dk := "loss:" + k
		if last, ok := am.useOnce.Load(dk); ok {
			if now.Sub(last.(time.Time)) < usageDebounce {
				continue
			}
		}
		am.useOnce.Store(dk, now)
		fresh = append(fresh, k)
	}
	if len(fresh) == 0 {
		return
	}
	// #3120 lock contract, same as RecordUse: FileLock outer, am.mu inner.
	if unlock, err := util.FileLock(am.dir + ".lock"); err == nil {
		defer unlock()
	} else {
		debug.Log("memory", "automemory sidecar filelock failed, degraded to unlocked write: %v", err)
	}
	am.mu.Lock()
	defer am.mu.Unlock()
	idx := am.loadUsage()
	for _, k := range fresh {
		rec := idx.Entries[k]
		if rec == nil {
			rec = &usageInfo{FirstSeen: now}
			idx.Entries[k] = rec
		}
		rec.Conflicts++
	}
	if err := am.saveUsage(idx); err != nil {
		debug.Log("memory", "conflict-loss persist failed: %v", err)
	}
}

// RecordConsumption (sa-147, LIMBO arXiv:2609.14138 inference-time memory
// allocation) persists consumption evidence: the entry's key or first-line
// fingerprint appeared in the assistant corpus after injection, so the
// prompt bytes it occupied paid for themselves. Feeds EffectiveInlineBudget.
// Debounced per key in a "consum:" namespace (independent of the Uses and
// "loss:" debouncers); fail-open with a log like every sidecar write.
func (am *AutoMemory) RecordConsumption(keys []string) {
	if len(keys) == 0 {
		return
	}
	now := time.Now()
	var fresh []string
	for _, k := range keys {
		dk := "consum:" + k
		if v, ok := am.useOnce.Load(dk); ok {
			if last, ok2 := v.(time.Time); ok2 && now.Sub(last) < usageDebounce {
				continue
			}
		}
		am.useOnce.Store(dk, now)
		fresh = append(fresh, k)
	}
	if len(fresh) == 0 {
		return
	}
	// #3120 lock contract, same as RecordUse: FileLock outer, am.mu inner.
	if unlock, err := util.FileLock(am.dir + ".lock"); err == nil {
		defer unlock()
	} else {
		debug.Log("memory", "automemory sidecar filelock failed, degraded to unlocked write: %v", err)
	}
	am.mu.Lock()
	defer am.mu.Unlock()
	idx := am.loadUsage()
	for _, k := range fresh {
		rec := idx.Entries[k]
		if rec == nil {
			rec = &usageInfo{FirstSeen: now}
			idx.Entries[k] = rec
		}
		rec.Consumed++
	}
	if err := am.saveUsage(idx); err != nil {
		debug.Log("memory", "consumption persist failed: %v", err)
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

	// #3120: cross-process serialization for the sidecar read-modify-write,
	// same lock file and lock order (FileLock outer, am.mu inner) as the
	// SaveMemory path in auto.go. r401 GAP-B locked only the .md save path;
	// RecordUse (main agent, index/inject time) raced RecordProvenance
	// (subagent saving a memory) last-write-wins on the SAME .usage.json,
	// silently losing use counts and provenance updates. Fail-open with a
	// log - same degradation contract as auto.go, but observable.
	if unlock, err := util.FileLock(am.dir + ".lock"); err == nil {
		defer unlock()
	} else {
		debug.Log("memory", "automemory sidecar filelock failed, degraded to unlocked write: %v", err)
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
	// #3602 / #3120: cross-process serialization for the sidecar
	// read-modify-write, same lock file and lock order (FileLock outer,
	// am.mu inner) as RecordUse/RecordConsumption. ForgetUsage used to hold
	// only am.mu while doing loadUsage -> delete -> saveUsage full
	// overwrite: a concurrent writer in another process could interleave
	// and either lose its update or resurrect the deleted ghost entry.
	// Fail-open with a log - same degradation contract as RecordUse.
	if unlock, err := util.FileLock(am.dir + ".lock"); err == nil {
		defer unlock()
	} else {
		debug.Log("memory", "automemory sidecar filelock failed, degraded to unlocked forget: %v", err)
	}
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
	// r29: the writer identity rides the same marker when recorded, so a
	// memory quietly overwritten by a sub-agent is visible in the index
	// line itself ([uses=N src=save_memory:project actor=agent-7]).
	if info.Actor != "" {
		return fmt.Sprintf(" [uses=%d src=%s actor=%s]", info.Uses, info.Source, info.Actor)
	}
	return fmt.Sprintf(" [uses=%d src=%s]", info.Uses, info.Source)
}
