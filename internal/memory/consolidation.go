package memory

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"sync"
	"time"

	"github.com/topcheer/ggcode/internal/debug"
)

// Sleep-time memory consolidation (Letta / UC Berkeley, arXiv:2504.13171):
// during the idle window between sessions — when the agent is not serving
// the user — reorganize the memory store so the NEXT session starts with a
// cleaner, more trustworthy context. ggcode already had every maintenance
// primitive this needs (ScanStaleness, pairwise claim contradiction checks,
// key-token near-duplicate detection, curation) but nothing ever invoked
// them as a lifecycle step: staleness/contradiction findings were computed
// only inside one-shot write-time checks and never persisted.
//
// Consolidate closes that loop. It is deliberately NON-DESTRUCTIVE: it never
// deletes or rewrites entry files (curation and GarbageCollect remain the
// only mutators, following the #779 doctrine that only GC deletes), so
// annotation cannot refresh entry ModTimes and skew expiry/dedup decisions.
// Findings are recorded in a sidecar state file (consolidation-state.json)
// that curation ignores, with firstSeen timestamps so repeat runs can report
// how long a conflict has gone unresolved. Consolidate is idempotent and
// safe to run at startup or periodically.

// consolidationStateFile is the sidecar that stores consolidation findings.
// It lives in the memory directory but is a .json file, so collectMetas
// (which only reads *.md) never treats it as a memory entry.
const consolidationStateFile = "consolidation-state.json"

// maxConsolidationEntries bounds the O(n²) pairwise scans. Curation caps the
// prompt-visible set at maxActiveMemories; allow headroom beyond that.
const maxConsolidationEntries = 120

// maxReadBytesPerEntry bounds how much of each entry file is loaded for
// claim extraction. Claims (key: value directives) live in the head of the
// file; unbounded reads would let one oversized memory dominate the sweep.
const maxReadBytesPerEntry = 16 * 1024

// maxConsolidationWarnings bounds the human-readable summary.
const maxConsolidationWarnings = 10

// consolidationConflict records one contradictory entry pair.
type consolidationConflict struct {
	A         string `json:"a"`
	B         string `json:"b"`
	Subject   string `json:"subject"`
	FirstSeen string `json:"firstSeen"` // RFC3339; preserved across runs
}

// consolidationState is the persisted sidecar payload.
type consolidationState struct {
	LastRun    string                  `json:"lastRun"`
	Scanned    int                     `json:"scanned"`
	Stale      map[string][]string     `json:"stale,omitempty"` // key -> "reason: detail"
	Conflicts  []consolidationConflict `json:"conflicts,omitempty"`
	Superseded map[string]string       `json:"superseded,omitempty"` // older key -> newer key
}

// ConsolidationReport summarizes one consolidation sweep.
type ConsolidationReport struct {
	Scanned         int      // active entries examined
	StaleFindings   int      // broken-path / oversized entries
	ConflictPairs   int      // entry pairs with contradictory claims
	SupersededPairs int      // near-duplicate key pairs (older superseded)
	UnresolvedDays  int      // oldest firstSeen age across persisted findings, 0 if none
	Warnings        []string // human-readable findings, capped
}

// HasFindings reports whether the sweep surfaced anything worth logging.
func (r ConsolidationReport) HasFindings() bool {
	return len(r.Warnings) > 0
}

// String returns a one-line summary for debug logs.
func (r ConsolidationReport) String() string {
	return fmt.Sprintf("memory consolidation: scanned=%d stale=%d conflicts=%d superseded=%d unresolvedDays=%d",
		r.Scanned, r.StaleFindings, r.ConflictPairs, r.SupersededPairs, r.UnresolvedDays)
}

// Consolidate runs the sleep-time sweep: staleness scan, pairwise
// contradiction scan, and near-duplicate key scan over the active entry set.
// It persists findings in the sidecar state file and returns a report.
// workingDir is used to resolve relative paths for staleness checks
// (pass "" to skip path checks, e.g. for global memory).
func (am *AutoMemory) Consolidate(workingDir string) ConsolidationReport {
	now := time.Now()
	state := am.loadConsolidationState()

	// --- 1. Staleness scan (existing primitive, now actually persisted). ---
	staleReport := am.ScanStaleness(workingDir)
	staleMap := make(map[string][]string, len(staleReport.Findings))
	for _, f := range staleReport.Findings {
		line := fmt.Sprintf("%s: %s", f.Reason, f.Detail)
		staleMap[f.Key] = append(staleMap[f.Key], line)
	}

	// --- 2. Load active entries once for the pairwise scans. ---
	type entry struct {
		meta    MemoryMeta
		content string
	}
	metas, err := am.collectMetas()
	if err != nil {
		debug.Log("memory", "consolidation: failed to read dir %s: %v", am.dir, err)
		return ConsolidationReport{}
	}
	active, _, _, _ := curateEntries(metas, now)
	if len(active) > maxConsolidationEntries {
		sort.Slice(active, func(i, j int) bool { return active[i].CreatedAt.After(active[j].CreatedAt) })
		active = active[:maxConsolidationEntries]
	}

	entries := make([]entry, 0, len(active))
	for _, m := range active {
		data, err := readEntryHead(am.dir, m.Key)
		if err != nil {
			continue
		}
		entries = append(entries, entry{meta: m, content: string(data)})
	}

	// --- 3. Pairwise contradiction scan (claim-level, deterministic). ---
	var conflicts []consolidationConflict
	conflictSeen := make(map[string]bool)
	for i := 0; i < len(entries); i++ {
		claimsA := extractClaims(entries[i].content)
		if len(claimsA) == 0 {
			continue
		}
		for j := i + 1; j < len(entries); j++ {
			claimsB := extractClaims(entries[j].content)
			for subject, valA := range claimsA {
				valB, ok := claimsB[subject]
				if !ok || !claimsConflict(valA, valB) {
					continue
				}
				pairKey := entries[i].meta.Key + "\x00" + entries[j].meta.Key + "\x00" + subject
				if conflictSeen[pairKey] {
					continue
				}
				conflictSeen[pairKey] = true
				conflicts = append(conflicts, consolidationConflict{
					A:       entries[i].meta.Key,
					B:       entries[j].meta.Key,
					Subject: subject,
				})
			}
		}
	}

	// --- 4. Near-duplicate key scan. Evolving entries already have
	// same-DedupKey keep-newest semantics in curation (and GC deletes the
	// losers), so only non-evolving pairs are recorded here as superseded
	// relationships — informational, never deletions.
	superseded := make(map[string]string)
	for i := 0; i < len(entries); i++ {
		for j := i + 1; j < len(entries); j++ {
			a, b := entries[i], entries[j]
			if a.meta.Category == CategoryEvolving && b.meta.Category == CategoryEvolving {
				continue
			}
			if a.meta.Category != b.meta.Category {
				continue
			}
			tokA, tokB := tokenize(a.meta.Key), tokenize(b.meta.Key)
			if len(tokA) == 0 || len(tokB) == 0 {
				continue
			}
			if jaccardSimilarity(tokA, tokB) < 0.6 {
				continue
			}
			oldKey, newKey := a.meta.Key, b.meta.Key
			if b.meta.CreatedAt.Before(a.meta.CreatedAt) {
				oldKey, newKey = newKey, oldKey
			}
			if prev, exists := superseded[oldKey]; !exists || prev == newKey {
				superseded[oldKey] = newKey
			}
		}
	}

	// --- 5. Merge firstSeen timestamps with the previous state. ---
	newState := &consolidationState{
		LastRun:    now.UTC().Format(time.RFC3339),
		Scanned:    len(entries),
		Stale:      staleMap,
		Conflicts:  conflicts,
		Superseded: superseded,
	}
	prev := make(map[string]consolidationConflict, len(state.Conflicts))
	for _, c := range state.Conflicts {
		prev[c.A+"\x00"+c.B+"\x00"+c.Subject] = c
	}
	for i := range newState.Conflicts {
		c := &newState.Conflicts[i]
		key := c.A + "\x00" + c.B + "\x00" + c.Subject
		if p, ok := prev[key]; ok && p.FirstSeen != "" {
			c.FirstSeen = p.FirstSeen
		} else {
			c.FirstSeen = newState.LastRun
		}
	}
	am.saveConsolidationState(newState)

	// --- 6. Build the report. ---
	report := ConsolidationReport{
		Scanned:         len(entries),
		StaleFindings:   len(staleReport.Findings),
		ConflictPairs:   len(conflicts),
		SupersededPairs: len(superseded),
	}
	oldestSeen := time.Time{}
	for _, f := range staleReport.Findings {
		if len(report.Warnings) >= maxConsolidationWarnings {
			break
		}
		report.Warnings = append(report.Warnings, fmt.Sprintf("stale %q: %s (%s)", f.Key, f.Reason, f.Detail))
	}
	for _, c := range conflicts {
		if len(report.Warnings) >= maxConsolidationWarnings {
			break
		}
		report.Warnings = append(report.Warnings,
			fmt.Sprintf("conflict: %q and %q disagree on %q", c.A, c.B, truncateForReport(c.Subject, 40)))
		if t, err := time.Parse(time.RFC3339, c.FirstSeen); err == nil {
			if oldestSeen.IsZero() || t.Before(oldestSeen) {
				oldestSeen = t
			}
		}
	}
	for oldKey, newKey := range superseded {
		if len(report.Warnings) >= maxConsolidationWarnings {
			break
		}
		report.Warnings = append(report.Warnings, fmt.Sprintf("near-duplicate: %q superseded by %q", oldKey, newKey))
	}
	if !oldestSeen.IsZero() {
		report.UnresolvedDays = int(now.Sub(oldestSeen).Hours() / 24)
	}
	return report
}

// readEntryHead reads at most maxReadBytesPerEntry bytes of an entry file.
func readEntryHead(dir, key string) ([]byte, error) {
	f, err := os.Open(filepath.Join(dir, key+".md"))
	if err != nil {
		return nil, err
	}
	defer f.Close()
	buf := make([]byte, maxReadBytesPerEntry)
	n, err := f.Read(buf)
	if err != nil && n == 0 {
		return nil, err
	}
	return buf[:n], nil
}

// loadConsolidationState reads the sidecar state file (best-effort; missing
// or corrupt state is equivalent to a fresh store).
func (am *AutoMemory) loadConsolidationState() *consolidationState {
	data, err := os.ReadFile(filepath.Join(am.dir, consolidationStateFile))
	if err != nil {
		return &consolidationState{}
	}
	var state consolidationState
	if err := json.Unmarshal(data, &state); err != nil {
		debug.Log("memory", "consolidation: corrupt state file ignored: %v", err)
		return &consolidationState{}
	}
	return &state
}

// saveConsolidationState persists the sidecar state atomically (temp+rename,
// same discipline as SaveMemory) under the per-path write mutex.
func (am *AutoMemory) saveConsolidationState(state *consolidationState) {
	data, err := json.Marshal(state)
	if err != nil {
		return
	}
	path := filepath.Join(am.dir, consolidationStateFile)
	muAny, _ := writeMu.LoadOrStore(path, &sync.Mutex{})
	mu := muAny.(*sync.Mutex)
	mu.Lock()
	defer mu.Unlock()
	tmp, err := os.CreateTemp(am.dir, "consolidation-state.tmp-*")
	if err != nil {
		return
	}
	tmpName := tmp.Name()
	if _, err := tmp.Write(data); err != nil {
		tmp.Close()
		os.Remove(tmpName)
		return
	}
	if err := tmp.Close(); err != nil {
		os.Remove(tmpName)
		return
	}
	if err := os.Chmod(tmpName, 0644); err != nil {
		os.Remove(tmpName)
		return
	}
	if err := os.Rename(tmpName, path); err != nil {
		os.Remove(tmpName)
	}
}

// truncateForReport clamps a string for report lines.
func truncateForReport(s string, max int) string {
	if len(s) <= max {
		return s
	}
	return s[:max] + "..."
}
