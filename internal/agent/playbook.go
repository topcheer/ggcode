package agent

import (
	"crypto/rand"
	"encoding/hex"
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

// Strategy Playbook — inspired by ACE (Agentic Context Engineering,
// Zhang et al., ICLR 2026, arXiv:2510.04618).
//
// ACE treats contexts as "evolving playbooks that accumulate, refine, and
// organize strategies." ggcode's ratchet rules learn from FAILURES (error
// patterns → prevention rules). The playbook learns from SUCCESSES: which
// tool call patterns lead to efficient task completion.
//
// Key design:
//   - Records successful tool call sequences categorized by task type
//   - Persists to .ggcode/playbook.json (per-workspace)
//   - Injects brief strategy hints into the system prompt at run start
//   - Uses incremental updates (ACE principle: prevent "context collapse")
//   - Groups similar patterns to avoid unbounded growth
//   - No LLM cost — pure heuristic pattern extraction

const (
	defaultMaxPlaybookEntries = 30
)

// PlaybookEntry records a successful strategy pattern for a task type.
type PlaybookEntry struct {
	ID           string    `json:"id"`
	TaskType     string    `json:"task_type"`      // bugfix, feature, refactor, review, test, build, other
	ToolSequence string    `json:"tool_sequence"`  // abstracted: "read→edit→build"
	FileTypes    string    `json:"file_types"`     // ".go", ".ts", ".py", mixed
	Uses         int       `json:"uses"`           // how many times this pattern was seen
	SuccessRate  float64   `json:"success_rate"`   // running success rate (0-1)
	AvgIter      float64   `json:"avg_iter"`       // average iterations to complete
	AvgDurationS float64   `json:"avg_duration_s"` // average duration in seconds
	LastSeen     time.Time `json:"last_seen"`
	CreatedAt    time.Time `json:"created_at"`
}

// Playbook accumulates successful strategy patterns across sessions.
type Playbook struct {
	mu         sync.Mutex
	path       string
	entries    []PlaybookEntry
	loaded     bool
	maxEntries int
}

// NewPlaybook creates a Playbook for the given working directory.
// Returns nil if workingDir is empty.
func NewPlaybook(workingDir string) *Playbook {
	if workingDir == "" {
		return nil
	}
	path := filepath.Join(workingDir, ".ggcode", "playbook.json")
	return &Playbook{
		path:       path,
		maxEntries: defaultMaxPlaybookEntries,
	}
}

func (pb *Playbook) load() {
	if pb.loaded || pb.path == "" {
		return
	}
	pb.loaded = true
	data, err := os.ReadFile(pb.path)
	if err != nil {
		return // first run — no playbook yet
	}
	if err := json.Unmarshal(data, &pb.entries); err != nil {
		debug.Log("playbook", "failed to load playbook: %v", err)
		return
	}
}

func (pb *Playbook) save() {
	if pb.path == "" {
		return
	}
	dir := filepath.Dir(pb.path)
	if err := os.MkdirAll(dir, 0755); err != nil {
		debug.Log("playbook", "failed to create playbook dir: %v", err)
		return
	}
	data, err := json.MarshalIndent(pb.entries, "", "  ")
	if err != nil {
		debug.Log("playbook", "failed to marshal playbook: %v", err)
		return
	}
	// Atomic write: write to temp file, then rename. Prevents corruption
	// if the process is interrupted mid-write.
	tmp := pb.path + ".tmp"
	if err := os.WriteFile(tmp, data, 0644); err != nil {
		debug.Log("playbook", "failed to write playbook tmp: %v", err)
		return
	}
	if err := os.Rename(tmp, pb.path); err != nil {
		debug.Log("playbook", "failed to rename playbook: %v", err)
		os.Remove(tmp) // cleanup
	}
}

// classifyTaskType determines the task category from the user prompt.
// Order matters: more specific categories are checked first to avoid
// misclassification (e.g., "add test" should be "test" not "feature").
func classifyTaskType(userPrompt string) string {
	p := strings.ToLower(userPrompt)
	switch {
	case containsAny(p, "test", "spec", "coverage", "mock"):
		return "test"
	case containsAny(p, "build", "compile", "make ", "ci ", "deploy", "release", "publish"):
		return "build"
	case containsAny(p, "fix", "bug", "error", "crash", "broken", " fail", "panic", "traceback"):
		return "bugfix"
	case containsAny(p, "refactor", "clean", "rename", "reorganize", "simplify", "extract"):
		return "refactor"
	case containsAny(p, "review", "check", "audit", "inspect", "scan", "analyze"):
		return "review"
	case containsAny(p, "add", "implement", "create", "new ", "support"):
		return "feature"
	default:
		return "other"
	}
}

// abstractToolSequence converts a tool call map into a compact sequence string.
// Tools are grouped into categories to generalize patterns:
// read, edit, execute, search, vcs, lsp, agent, other.
func abstractToolSequence(tools map[string]int) string {
	categories := map[string]string{
		// read
		"read_file": "read", "multi_file_read": "read", "list_directory": "read",
		"glob": "read", "grep": "read", "search_files": "read",
		// edit
		"edit_file": "edit", "multi_edit_file": "edit", "multi_file_edit": "edit",
		"write_file": "edit", "multi_file_write": "edit", "notebook_edit": "edit",
		// execute
		"run_command": "exec", "start_command": "exec",
		// search
		"web_search": "search", "web_fetch": "search",
		// vcs
		"git_status": "vcs", "git_diff": "vcs", "git_log": "vcs", "git_add": "vcs",
		"git_commit": "vcs", "git_show": "vcs", "git_branch_list": "vcs",
		"git_remote": "vcs", "git_blame": "vcs", "git_stash": "vcs",
		"git_stash_list": "vcs",
		// lsp
		"lsp_definition": "lsp", "lsp_references": "lsp", "lsp_hover": "lsp",
		"lsp_symbols": "lsp", "lsp_workspace_symbols": "lsp", "lsp_diagnostics": "lsp",
		"lsp_rename": "lsp", "lsp_implementation": "lsp", "lsp_code_actions": "lsp",
		"lsp_prepare_call_hierarchy": "lsp", "lsp_incoming_calls": "lsp",
		"lsp_outgoing_calls": "lsp",
	}

	seen := map[string]bool{}
	var parts []string
	// Use a deterministic order for categories
	order := []string{"read", "edit", "exec", "search", "vcs", "lsp", "agent", "other"}
	for tool := range tools {
		cat := categories[tool]
		if cat == "" {
			cat = "other"
		}
		if !seen[cat] {
			seen[cat] = true
		}
	}
	for _, cat := range order {
		if seen[cat] {
			parts = append(parts, cat)
		}
	}
	return strings.Join(parts, "→")
}

// extractFileTypes determines the primary file types from edited files.
func extractFileTypes(filesEdited []string) string {
	exts := map[string]bool{}
	for _, f := range filesEdited {
		ext := strings.ToLower(filepath.Ext(f))
		if ext != "" {
			exts[ext] = true
		}
	}
	if len(exts) == 0 {
		return ""
	}
	if len(exts) == 1 {
		for ext := range exts {
			return ext
		}
	}
	// Multiple extensions — sort for deterministic fingerprint
	var sorted []string
	for ext := range exts {
		sorted = append(sorted, ext)
	}
	sort.Strings(sorted)
	return strings.Join(sorted, "+")
}

// Record extracts a strategy pattern from a successful run and updates the playbook.
// Called from maybeReflect after a successful agent run.
func (pb *Playbook) Record(stats *RunStats) {
	if pb == nil || stats == nil || !stats.Success {
		return
	}

	// Only record meaningful runs
	totalCalls := 0
	for _, c := range stats.ToolCalls {
		totalCalls += c
	}
	if totalCalls < 3 {
		return
	}

	pb.mu.Lock()
	defer pb.mu.Unlock()
	pb.load()

	taskType := classifyTaskType(stats.UserPrompt)
	toolSeq := abstractToolSequence(stats.ToolCalls)
	fileTypes := extractFileTypes(stats.FilesEdited)

	// Pattern fingerprint: taskType + toolSeq + fileTypes
	fingerprint := taskType + "|" + toolSeq + "|" + fileTypes

	// Try to find an existing entry with the same fingerprint
	for i := range pb.entries {
		e := &pb.entries[i]
		ep := e.TaskType + "|" + e.ToolSequence + "|" + e.FileTypes
		if ep == fingerprint {
			// Update existing entry with incremental average (ACE principle:
			// "structured, incremental updates that preserve detailed knowledge")
			pb.updateEntry(e, stats)
			pb.save()
			debug.Log("playbook", "updated entry %s (uses=%d, success=%.1f%%)", e.TaskType, e.Uses, e.SuccessRate*100)
			return
		}
	}

	// Create new entry
	now := time.Now()
	entry := PlaybookEntry{
		ID:           randomID(),
		TaskType:     taskType,
		ToolSequence: toolSeq,
		FileTypes:    fileTypes,
		Uses:         1,
		SuccessRate:  1.0, // first observation was successful
		AvgIter:      float64(stats.Iterations),
		AvgDurationS: stats.Duration.Seconds(),
		LastSeen:     now,
		CreatedAt:    now,
	}
	pb.entries = append(pb.entries, entry)

	// Evict if over capacity (keep most recently used)
	if len(pb.entries) > pb.maxEntries {
		pb.evict()
	}

	pb.save()
	debug.Log("playbook", "recorded new %s strategy: %s (files=%s)", taskType, toolSeq, fileTypes)
}

// updateEntry merges a new observation into an existing entry using incremental averaging.
func (pb *Playbook) updateEntry(e *PlaybookEntry, stats *RunStats) {
	n := float64(e.Uses)
	e.AvgIter = (e.AvgIter*n + float64(stats.Iterations)) / (n + 1)
	e.AvgDurationS = (e.AvgDurationS*n + stats.Duration.Seconds()) / (n + 1)
	e.Uses++
	e.SuccessRate = 1.0 // only successful runs are recorded, so rate stays 1.0
	// Note: if we later record failures too, SuccessRate would decrease
	e.LastSeen = time.Now()
}

// evict removes the least recently used entries to stay within capacity.
func (pb *Playbook) evict() {
	if len(pb.entries) <= pb.maxEntries {
		return
	}
	// Sort by LastSeen descending (most recent first), keep top maxEntries
	sort.Slice(pb.entries, func(i, j int) bool {
		return pb.entries[i].LastSeen.After(pb.entries[j].LastSeen)
	})
	pb.entries = pb.entries[:pb.maxEntries]
}

// HintsForPrompt generates brief strategy hints for the system prompt.
// Returns at most maxHints entries, prioritized by a composite score that
// considers both frequency and efficiency.
//
// Inspired by SICA's utility function (Robeyns et al., arXiv:2504.15228):
// patterns that lead to faster completion are more valuable than patterns
// used frequently but slowly. The score combines:
//   - Frequency weight: more observations = higher confidence
//   - Efficiency weight: fewer iterations = better strategy
//
// This ensures that a pattern observed 3 times at ~5 iterations ranks higher
// than one observed 5 times at ~50 iterations.
func (pb *Playbook) HintsForPrompt(maxHints int) string {
	if pb == nil {
		return ""
	}
	pb.mu.Lock()
	defer pb.mu.Unlock()
	pb.load()

	if len(pb.entries) == 0 {
		return ""
	}

	// Sort entries by composite score (descending).
	// Score = frequency × efficiency, where:
	//   frequency = min(uses, 10) — cap at 10 to prevent over-weighting
	//   efficiency = 10 / avgIter — fewer iterations = higher score
	// This rewards patterns that are both well-observed AND efficient.
	sorted := make([]PlaybookEntry, len(pb.entries))
	copy(sorted, pb.entries)
	sort.Slice(sorted, func(i, j int) bool {
		return playbookScore(sorted[i]) > playbookScore(sorted[j])
	})

	if maxHints > len(sorted) {
		maxHints = len(sorted)
	}

	var lines []string
	lines = append(lines, "## Strategy Playbook (learned from past successes)")
	for i := 0; i < maxHints; i++ {
		e := sorted[i]
		durHint := ""
		if e.AvgDurationS > 0 {
			if e.AvgDurationS < 60 {
				durHint = fmt.Sprintf(", ~%.0fs", e.AvgDurationS)
			} else {
				durHint = fmt.Sprintf(", ~%.0fm", e.AvgDurationS/60)
			}
		}
		fileHint := ""
		if e.FileTypes != "" {
			fileHint = fmt.Sprintf(" [%s]", e.FileTypes)
		}
		lines = append(lines, fmt.Sprintf("- %s%s: %s (%d runs, ~%.0f iter%s)",
			e.TaskType, fileHint, e.ToolSequence, e.Uses, e.AvgIter, durHint))
	}
	return strings.Join(lines, "\n")
}

// playbookScore computes a composite score for ranking playbook entries.
// Higher is better. Combines frequency (more observations = higher confidence)
// with efficiency (fewer iterations = better strategy).
//
// Formula: score = min(uses, 10) * (10 / max(avgIter, 1))
//   - A pattern used 5 times at ~10 iterations scores 5.0
//   - A pattern used 10 times at ~50 iterations scores 2.0
//   - A pattern used 3 times at ~5 iterations scores 6.0
func playbookScore(e PlaybookEntry) float64 {
	freq := float64(e.Uses)
	if freq > 10 {
		freq = 10
	}
	iter := e.AvgIter
	if iter < 1 {
		iter = 1
	}
	return freq * (10.0 / iter)
}

// recordPlaybook is called from maybeReflect to record successful strategies.
func (a *Agent) recordPlaybook(stats *RunStats) {
	if stats == nil || !stats.Success {
		return
	}
	workingDir := a.WorkingDir()
	if workingDir == "" {
		return
	}
	pb := NewPlaybook(workingDir)
	if pb == nil {
		return
	}
	pb.Record(stats)
}

func containsAny(s string, substrs ...string) bool {
	for _, sub := range substrs {
		if strings.Contains(s, sub) {
			return true
		}
	}
	return false
}

func randomID() string {
	b := make([]byte, 6)
	rand.Read(b)
	return hex.EncodeToString(b)
}
