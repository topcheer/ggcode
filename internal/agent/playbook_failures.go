package agent

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
	maxFailureEntries     = 20
	maxFailureHintChars   = 600
	failureEntryTTL       = 30 * 24 * time.Hour
	failureHintStaleAfter = 14 * 24 * time.Hour
)

type PlaybookFailureEntry struct {
	ID          string    `json:"id"`
	TaskType    string    `json:"task_type"`
	SuspectTool string    `json:"suspect_tool"`
	SuspectFile string    `json:"suspect_file"`
	MaxCRS      int       `json:"max_crs"`
	Repair      string    `json:"repair,omitempty"`
	Occurrences int       `json:"occurrences"`
	FirstSeen   time.Time `json:"first_seen"`
	LastSeen    time.Time `json:"last_seen"`
}

func failureStorePath(workingDir string) string {
	return filepath.Join(workingDir, ".ggcode", "playbook_failures.json")
}

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

func RecordFailureAttribution(workingDir, userPrompt, suspectTool, suspectFile string, crs int) {
	if workingDir == "" || suspectFile == "" {
		return
	}
	path := failureStorePath(workingDir)

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
				e.Repair = CounterfactualRepairStep(suspectTool)
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
		Repair:      CounterfactualRepairStep(suspectTool),
		Occurrences: 1,
		FirstSeen:   now,
		LastSeen:    now,
	})
	persistFailureEntries(path, pruneFailureEntries(entries))
	debug.Log("failure-attribution", "recorded entry %s%s (crs=%d)", taskType, suspectFile, crs)
}

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

func FailureHintsForPrompt(workingDir, runPrompt string, maxHints int) string {
	if workingDir == "" || maxHints <= 0 {
		return ""
	}
	entries := loadFailureEntries(failureStorePath(workingDir))
	if len(entries) == 0 {
		return ""
	}

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
		if e.Repair != "" {
			note += " → fix: " + e.Repair
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
