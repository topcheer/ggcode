package memory

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/topcheer/ggcode/internal/debug"
)

const consolidationStateFile = "consolidation-state.json"

const maxConsolidationEntries = 120

const maxReadBytesPerEntry = 16 * 1024

const maxConsolidationWarnings = 10

type consolidationConflict struct {
	A         string `json:"a"`
	B         string `json:"b"`
	Subject   string `json:"subject"`
	FirstSeen string `json:"firstSeen"`
}

type consolidationState struct {
	LastRun    string                  `json:"lastRun"`
	Scanned    int                     `json:"scanned"`
	Stale      map[string][]string     `json:"stale,omitempty"`
	Conflicts  []consolidationConflict `json:"conflicts,omitempty"`
	Superseded map[string]string       `json:"superseded,omitempty"`
}

type ConsolidationReport struct {
	Scanned         int
	StaleFindings   int
	ConflictPairs   int
	SupersededPairs int
	UnresolvedDays  int
	Warnings        []string
}

func (r ConsolidationReport) HasFindings() bool {
	return len(r.Warnings) > 0
}

func (r ConsolidationReport) String() string {
	return fmt.Sprintf("memory consolidation: scanned=%d stale=%d conflicts=%d superseded=%d unresolvedDays=%d",
		r.Scanned, r.StaleFindings, r.ConflictPairs, r.SupersededPairs, r.UnresolvedDays)
}

func (am *AutoMemory) Consolidate(workingDir string) ConsolidationReport {
	now := time.Now()
	state := am.loadConsolidationState()

	staleReport := am.ScanStaleness(workingDir)
	staleMap := make(map[string][]string, len(staleReport.Findings))
	for _, f := range staleReport.Findings {
		line := fmt.Sprintf("%s: %s", f.Reason, f.Detail)
		staleMap[f.Key] = append(staleMap[f.Key], line)
	}

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

// LastConsolidationRun reports when the last Consolidate scan completed
// (zero time when never run or the state is unreadable). Startup paths use
// it to skip re-scans that ran recently (#5519).
func (am *AutoMemory) LastConsolidationRun() time.Time {
	st := am.loadConsolidationState()
	if st == nil || st.LastRun == "" {
		return time.Time{}
	}
	ts, err := time.Parse(time.RFC3339, st.LastRun)
	if err != nil {
		return time.Time{}
	}
	return ts
}

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

func truncateForReport(s string, max int) string {
	if len(s) <= max {
		return s
	}
	return s[:max] + "..."
}

const repairSuggestionMinAgeDays = 7

const repairSuggestionMaxLen = 600

func (am *AutoMemory) RepairSuggestions() string {
	state := am.loadConsolidationState()
	if state == nil {
		return ""
	}
	now := time.Now()
	var lines []string

	for key, reasons := range state.Stale {
		if len(reasons) == 0 {
			continue
		}
		lines = append(lines, fmt.Sprintf("- stale %q (%s): verify or rewrite this memory", key, reasons[0]))
		if len(lines) >= 2 {
			break
		}
	}

	conflicts := 0
	for _, c := range state.Conflicts {
		seen, err := time.Parse(time.RFC3339, c.FirstSeen)
		if err != nil || now.Sub(seen).Hours()/24 < repairSuggestionMinAgeDays {
			continue
		}
		lines = append(lines, fmt.Sprintf("- contradiction on %q (%s vs %s, first seen %d days ago): supersede the outdated side via save_memory", c.Subject, c.A, c.B, int(now.Sub(seen).Hours()/24)))
		conflicts++
		if conflicts >= 2 {
			break
		}
	}
	if len(lines) == 0 {
		return ""
	}
	sort.Strings(lines)
	block := "Memory repair suggestions (from sleep-time consolidation; fix with save_memory/supersede, user in the loop):\n" + strings.Join(lines, "\n")
	if len(block) > repairSuggestionMaxLen {
		block = block[:repairSuggestionMaxLen]
	}
	return block
}
