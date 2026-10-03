package agent

// Persisted Failure Attribution - r413 (Trajectory Credit Assignment).
//
// Research: CausalFlow (arXiv:2605.25338) computes step-level causal
// responsibility, and ggcode's causal_attribution.go already injects the
// top suspect into in-run guidance. But that knowledge evaporates at run
// end: the next session repeats the same class of mistake with no memory
// of WHICH step historically caused it. The strategy playbook learns from
// successes; ratchet rules learn error-text patterns. Neither persists
// "this file/tool was the causal suspect, watch it first" across runs.
//
// This file closes that loop (minimal scope, task-level terminal hook):
//   1. On a failed run, the run's final causal suspect (tool+file+CRS)
//      is persisted to .ggcode/playbook_failures.json, aggregated by
//      (taskType, suspectFile) with an occurrence count.
//   2. At the next run's prompt assembly, the highest-occurrence entries
//      matching the task intent are injected as one compact hint block.
//
// Design constraints (mirrors playbook.go):
//   - Zero LLM cost; deterministic aggregation.
//   - Cross-process file lock around load->modify->save (fail-open).
//   - Cap 20 entries; pruned by (occurrences, recency).
//   - Read path is lock-free (single read, tolerate stale).

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"github.com/topcheer/ggcode/internal/debug"
	"github.com/topcheer/ggcode/internal/util"
)

const (
	// maxFailureEntries bounds the persisted attribution memory.
	maxFailureEntries = 20
	// maxFailureHintChars bounds the injected hint block.
	maxFailureHintChars = 400
	// failureEntryTTL is how long an attribution entry survives without
	// recurrence (#3146): a suspect that has not been re-attributed for
	// this long is presumed fixed and evicted at the next prune (write
	// time). Without it the memory is a monotonic false-positive
	// amplifier - a fixed file keeps occupying hint budget forever.
	failureEntryTTL = 30 * 24 * time.Hour
	// failureHintStaleAfter is the render-side guard (#3146): prune runs
	// only on writes, so an entry can sit between TTL expiry and the next
	// write. Entries unseen for this long are skipped at injection time
	// (strictly earlier than the TTL so the two never disagree).
	failureHintStaleAfter = 14 * 24 * time.Hour
)

// PlaybookFailureEntry records an aggregated terminal failure attribution.
type PlaybookFailureEntry struct {
	ID          string    `json:"id"`
	TaskType    string    `json:"task_type"`    // classifyTaskType of the failing run's prompt
	SuspectTool string    `json:"suspect_tool"` // edit_file, write_file, ...
	SuspectFile string    `json:"suspect_file"` // attributed file path
	MaxCRS      int       `json:"max_crs"`      // highest causal responsibility score seen
	Occurrences int       `json:"occurrences"`  // aggregated count
	FirstSeen   time.Time `json:"first_seen"`
	LastSeen    time.Time `json:"last_seen"`
}

// failureStorePath returns the persistence path for a working dir.
func failureStorePath(workingDir string) string {
	return filepath.Join(workingDir, ".ggcode", "playbook_failures.json")
}

// loadFailureEntries reads the persisted entries (missing file = empty).
func loadFailureEntries(path string) []PlaybookFailureEntry {
	var entries []PlaybookFailureEntry
	data, err := os.ReadFile(path)
	if err != nil {
		return nil
	}
	if err := json.Unmarshal(data, &entries); err != nil {
		debug.Log("failure-attribution", "failed to load playbook_failures: %v", err)
		return nil
	}
	return entries
}

// RecordFailureAttribution persists a failed run's terminal causal suspect.
// Aggregation key: (taskType, suspectFile). Package-level so the agent
// hook stays a thin wrapper (same shape as NewPlaybook/Record split).
func RecordFailureAttribution(workingDir, userPrompt, suspectTool, suspectFile string, crs int) {
	if workingDir == "" || suspectFile == "" {
		return
	}
	path := failureStorePath(workingDir)
	// Directory must exist before the lock open or O_CREATE fails ENOENT
	// and every first-run record silently takes the unlocked path (#2726).
	_ = os.MkdirAll(filepath.Dir(path), 0755)
	if unlock, err := util.FileLock(path + ".lock"); err == nil {
		defer unlock()
	} else {
		debug.Log("failure-attribution", "record: lock unavailable (proceeding unlocked): %v", err)
	}

	entries := loadFailureEntries(path)
	taskType := classifyTaskType(userPrompt)

	now := time.Now()
	for i := range entries {
		e := &entries[i]
		if e.TaskType == taskType && e.SuspectFile == suspectFile {
			e.Occurrences++
			e.LastSeen = now
			if crs > e.MaxCRS {
				e.MaxCRS = crs
			}
			if suspectTool != "" {
				e.SuspectTool = suspectTool
			}
			persistFailureEntries(path, pruneFailureEntries(entries))
			debug.Log("failure-attribution", "updated entry %s%s (occurrences=%d)", taskType, suspectFile, e.Occurrences)
			return
		}
	}

	entries = append(entries, PlaybookFailureEntry{
		ID:          randomID(),
		TaskType:    taskType,
		SuspectTool: suspectTool,
		SuspectFile: suspectFile,
		MaxCRS:      crs,
		Occurrences: 1,
		FirstSeen:   now,
		LastSeen:    now,
	})
	persistFailureEntries(path, pruneFailureEntries(entries))
	debug.Log("failure-attribution", "recorded entry %s%s (crs=%d)", taskType, suspectFile, crs)
}

// pruneFailureEntries caps the list and enforces the memory TTL
// (#3146). Time-first: entries whose LastSeen predates the TTL are
// evicted as presumably-fixed (a stale false positive must not survive
// just because the store is not full); the remaining entries are capped
// at maxFailureEntries evicting the least valuable ones (lowest
// occurrences first, then oldest).
func pruneFailureEntries(entries []PlaybookFailureEntry) []PlaybookFailureEntry {
	cutoff := time.Now().Add(-failureEntryTTL)
	kept := entries[:0]
	for _, e := range entries {
		if e.LastSeen.After(cutoff) {
			kept = append(kept, e)
		}
	}
	entries = kept
	if len(entries) <= maxFailureEntries {
		return entries
	}
	sort.SliceStable(entries, func(i, j int) bool {
		if entries[i].Occurrences != entries[j].Occurrences {
			return entries[i].Occurrences > entries[j].Occurrences
		}
		return entries[i].LastSeen.After(entries[j].LastSeen)
	})
	return entries[:maxFailureEntries]
}

func persistFailureEntries(path string, entries []PlaybookFailureEntry) {
	if len(entries) == 0 {
		return
	}
	data, err := json.MarshalIndent(entries, "", "  ")
	if err != nil {
		debug.Log("failure-attribution", "marshal failed: %v", err)
		return
	}
	tmp := path + ".tmp"
	if err := os.WriteFile(tmp, data, 0600); err != nil {
		debug.Log("failure-attribution", "write tmp failed: %v", err)
		return
	}
	if err := os.Rename(tmp, path); err != nil {
		debug.Log("failure-attribution", "rename failed: %v", err)
	}
}

// recordFailureAttribution is the agent's terminal hook (r413): called from
// the run defer for non-cancelled failed runs, it snapshots the final
// causal suspect and persists it. takeFinalSuspect consumes the suspect so
// a stale entry can never leak into a later turn's record.
func (a *Agent) recordFailureAttribution(stats *RunStats) {
	if stats == nil || stats.Success {
		return
	}
	if a.causalAttribution == nil {
		return
	}
	step, crs := a.causalAttribution.takeFinalSuspect()
	if step == nil {
		return
	}
	RecordFailureAttribution(a.WorkingDir(), stats.UserPrompt, step.toolName, step.filePath, crs)
}

// FailureHintsForPrompt renders the persisted failure-attribution memory
// for the system prompt. Intent-aware like HintsForPrompt: entries whose
// task type matches the prompt form the primary tier; the global ranking
// fills the remaining budget. Lock-free single read.
func FailureHintsForPrompt(workingDir, runPrompt string, maxHints int) string {
	if workingDir == "" || maxHints <= 0 {
		return ""
	}
	entries := loadFailureEntries(failureStorePath(workingDir))
	if len(entries) == 0 {
		return ""
	}
	// #3146 render-side staleness guard: prune runs only on writes, so an
	// entry can sit between TTL expiry and the next record. Skip anything
	// not re-attributed within failureHintStaleAfter - a fixed file must
	// not keep occupying the hint budget in that window.
	staleCutoff := time.Now().Add(-failureHintStaleAfter)
	fresh := entries[:0]
	for _, e := range entries {
		if e.LastSeen.After(staleCutoff) {
			fresh = append(fresh, e)
		}
	}
	entries = fresh
	if len(entries) == 0 {
		return ""
	}

	intent := classifyTaskType(runPrompt)
	intentMatch := func(e PlaybookFailureEntry) bool {
		return intent != "" && intent != "other" && e.TaskType == intent
	}
	sorted := make([]PlaybookFailureEntry, len(entries))
	copy(sorted, entries)
	sort.SliceStable(sorted, func(i, j int) bool {
		mi, mj := intentMatch(sorted[i]), intentMatch(sorted[j])
		if mi != mj {
			return mi
		}
		if sorted[i].Occurrences != sorted[j].Occurrences {
			return sorted[i].Occurrences > sorted[j].Occurrences
		}
		return sorted[i].LastSeen.After(sorted[j].LastSeen)
	})

	if maxHints > len(sorted) {
		maxHints = len(sorted)
	}

	var lines []string
	lines = append(lines, "## Failure Attribution Memory (learned from past failed runs)")
	for i := 0; i < maxHints; i++ {
		e := sorted[i]
		note := ""
		switch {
		case e.Occurrences >= 3:
			note = " - recurring suspect, verify this file early"
		case e.Occurrences == 2:
			note = " - seen twice, double-check before editing"
		}
		tool := e.SuspectTool
		if tool == "" {
			tool = "edit"
		}
		lines = append(lines, fmt.Sprintf("- %s runs previously failed with %s on %s (CRS=%d, %d×)%s",
			e.TaskType, tool, e.SuspectFile, e.MaxCRS, e.Occurrences, note))
	}
	out := strings.Join(lines, "\n")
	if len(out) > maxFailureHintChars {
		// #3142 review follow-up: truncate at a line boundary, never
		// mid-rune (the hint block is user-visible and may contain CJK).
		cut := maxFailureHintChars
		for cut > 0 && out[cut-1] != '\n' {
			cut--
		}
		if cut > 0 {
			out = out[:cut]
		} else {
			out = out[:maxFailureHintChars]
		}
	}
	return out
}
