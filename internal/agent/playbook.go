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
	"github.com/topcheer/ggcode/internal/util"
)

// Strategy Playbook — inspired by ACE (Agentic Context Engineering,
// Zhang et al., ICLR 2026, arXiv:2510.04618).
//
// ACE treats contexts as "evolving playbooks that accumulate, refine, and
// organize strategies." ggcode's ratchet rules learn from FAILURES (error
// patterns → prevention rules). The playbook learns from run outcomes:
// successes seed strategy patterns, and subsequent failed runs are
// attributed back to those patterns so ranking stays outcome-driven
// ("Library Drift", arXiv:2605.19576: skill libraries without
// outcome-driven lifecycle management suffer retrieval degradation and
// false-positive injections).
//
// Key design:
//   - Records successful tool call sequences categorized by task type
//   - Attributes failed runs to existing patterns (never creates entries)
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
	TaskType     string    `json:"task_type"`          // bugfix, feature, refactor, review, test, build, other
	ToolSequence string    `json:"tool_sequence"`      // abstracted: "read→edit→build"
	FileTypes    string    `json:"file_types"`         // ".go", ".ts", ".py", mixed
	Uses         int       `json:"uses"`               // total runs observed (successes + failures)
	SuccessRate  float64   `json:"success_rate"`       // observed success rate (successes/uses, 0-1)
	Failures     int       `json:"failures,omitempty"` // failed runs attributed to this pattern
	AvgIter      float64   `json:"avg_iter"`           // average iterations on successful runs
	AvgDurationS float64   `json:"avg_duration_s"`     // average duration in seconds
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
// Keywords are matched on word boundaries (#2745): bare substring matching
// classified "upgrade to the latest version" as test (laTEST), "refactor
// the contest module" as test (conTEST), "address the failing build" as
// feature (ADDRESS), and "rebuild the parser" as build (REBUILD) - the
// misclassified type then polluted the persisted playbook fingerprint and
// system prompt injection. Keywords written with an explicit leading or
// trailing space (" fail", "make ", "ci ", "new ") already encode their
// own anchoring and keep the substring behavior.
func classifyTaskType(userPrompt string) string {
	p := strings.ToLower(userPrompt)
	switch {
	case containsAnyWord(p, "test", "spec", "coverage", "mock"):
		return "test"
	case containsAnyWord(p, "build", "compile", "make ", "ci ", "deploy", "release", "publish"):
		return "build"
	case containsAnyWord(p, "fix", "bug", "error", "crash", "broken", " fail", "panic", "traceback"):
		return "bugfix"
	case containsAnyWord(p, "refactor", "clean", "rename", "reorganize", "simplify", "extract"):
		return "refactor"
	case containsAnyWord(p, "review", "check", "audit", "inspect", "scan", "analyze"):
		return "review"
	case containsAnyWord(p, "add", "implement", "create", "new ", "support"):
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
		"lsp_outgoing_calls":      "lsp",
		"lsp_document_highlights": "lsp",
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
	pb.record(stats, true)
}

// RecordFailure attributes a failed run to the playbook entry matching the
// run's strategy fingerprint (taskType|toolSeq|fileTypes). It never creates
// a new entry: patterns are born only from successes, but once a pattern
// exists its observed failures must count against it. Otherwise SuccessRate
// is a constant 1.0 and ranking cannot distinguish reliable patterns from
// lucky or degrading ones. Averages stay success-only so the efficiency
// signal in hints remains clean; the outcome signal lives in Failures.
func (pb *Playbook) RecordFailure(stats *RunStats) {
	pb.record(stats, false)
}

func (pb *Playbook) record(stats *RunStats, success bool) {
	if pb == nil || stats == nil || success != stats.Success {
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

	// #2726: recordPlaybook builds a FRESH Playbook per run, so the
	// instance mutex serializes nothing across instances. Two ggcode
	// processes (multi-window / multi-IM on one workspace) recording near-
	// simultaneously both loaded the same on-disk state and the last writer
	// erased the other's entries. Hold the cross-process file lock around
	// the whole load->modify->save cycle; on acquisition failure proceed
	// unlocked (fail-open, same degraded semantics as the cron/probe-cache
	// callers) rather than blocking the reflection path. The directory must
	// exist BEFORE the lock open, or O_CREATE fails ENOENT and every
	// first-run Record silently takes the unlocked path.
	if pb.path != "" {
		_ = os.MkdirAll(filepath.Dir(pb.path), 0755)
	}
	if unlock, err := util.FileLock(pb.path + ".lock"); err == nil {
		defer unlock()
	} else {
		debug.Log("playbook", "record: failed to acquire playbook lock (proceeding unlocked): %v", err)
	}

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
			if success {
				pb.updateEntry(e, stats)
				debug.Log("playbook", "updated entry %s (uses=%d, success=%.1f%%)", e.TaskType, e.Uses, e.SuccessRate*100)
			} else {
				pb.updateFailure(e)
				debug.Log("playbook", "attributed failure to entry %s (uses=%d, success=%.1f%%)", e.TaskType, e.Uses, e.SuccessRate*100)
			}
			pb.save()
			return
		}
	}

	// Failed runs never create entries: a pattern with zero successes has
	// nothing to hint about and would pollute the hint space with an
	// unproven fingerprint.
	if !success {
		debug.Log("playbook", "failed run matched no playbook entry; no entry created")
		return
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

// updateEntry merges a new successful observation into an existing entry
// using incremental averaging. Averages are success-only; failures are
// tracked separately by updateFailure.
func (pb *Playbook) updateEntry(e *PlaybookEntry, stats *RunStats) {
	n := float64(e.Uses)
	e.AvgIter = (e.AvgIter*n + float64(stats.Iterations)) / (n + 1)
	e.AvgDurationS = (e.AvgDurationS*n + stats.Duration.Seconds()) / (n + 1)
	e.Uses++
	e.SuccessRate = successRate(*e)
	e.LastSeen = time.Now()
}

// updateFailure attributes a failed run to an existing entry. Uses grows so
// that the "N runs" figure in hints reflects total observations; Averages
// are untouched so the efficiency signal stays success-only.
func (pb *Playbook) updateFailure(e *PlaybookEntry) {
	e.Uses++
	e.Failures++
	e.SuccessRate = successRate(*e)
	e.LastSeen = time.Now()
}

// successRate computes the observed success rate (0-1) from run counts.
func successRate(e PlaybookEntry) float64 {
	if e.Uses <= 0 {
		return 1.0
	}
	ok := e.Uses - e.Failures
	if ok < 0 {
		ok = 0
	}
	return float64(ok) / float64(e.Uses)
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
		okHint := ""
		if r := successRate(e); r < 1.0 {
			okHint = fmt.Sprintf(", %d%% ok", int(r*100+0.5))
		}
		lines = append(lines, fmt.Sprintf("- %s%s: %s (%d runs%s, ~%.0f iter%s)",
			e.TaskType, fileHint, e.ToolSequence, e.Uses, okHint, e.AvgIter, durHint))
	}
	return strings.Join(lines, "\n")
}

// playbookScore computes a composite score for ranking playbook entries.
// Higher is better. Combines frequency (more observations = higher
// confidence), efficiency (fewer iterations = better strategy) and outcome
// (observed success rate demotes patterns whose follow-up runs tend to fail).
//
// Formula: score = min(uses, 10) * (10 / max(avgIter, 1)) * successRate
//   - A pattern used 5 times at ~10 iterations scores 5.0
//   - A pattern used 10 times at ~50 iterations scores 2.0
//   - A pattern used 3 times at ~5 iterations scores 6.0
//   - The same pattern at 50% observed success would score 3.0
func playbookScore(e PlaybookEntry) float64 {
	freq := float64(e.Uses)
	if freq > 10 {
		freq = 10
	}
	iter := e.AvgIter
	if iter < 1 {
		iter = 1
	}
	return freq * (10.0 / iter) * successRate(e)
}

// recordPlaybook is called from maybeReflect to record run outcomes:
// successes create or refine strategy patterns; failed runs are attributed
// to existing patterns so their SuccessRate reflects reality.
func (a *Agent) recordPlaybook(stats *RunStats) {
	if stats == nil {
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
	if stats.Success {
		pb.Record(stats)
	} else {
		pb.RecordFailure(stats)
	}
}

func containsAny(s string, substrs ...string) bool {
	for _, sub := range substrs {
		if strings.Contains(s, sub) {
			return true
		}
	}
	return false
}

// containsAnyWord matches whole words only (#2745). A keyword containing
// an explicit space (" fail", "make ", "new ") encodes its own anchoring
// and falls back to substring matching, preserving the original intent.
func containsAnyWord(s string, keywords ...string) bool {
	for _, kw := range keywords {
		if strings.ContainsAny(kw, " \t") {
			if strings.Contains(s, kw) {
				return true
			}
			continue
		}
		start := 0
		for {
			i := strings.Index(s[start:], kw)
			if i < 0 {
				break
			}
			at := start + i
			end := at + len(kw)
			if wordBoundaryAt(s, at) && wordBoundaryAt(s, end) {
				return true
			}
			start = at + 1
		}
	}
	return false
}

// wordBoundaryAt reports whether position i in s is a word boundary:
// either string edge, or the neighboring bytes are not word bytes on both
// sides of the boundary. Reuses isWordByte from success_declare.go
// (identifier semantics: [a-z0-9_]).
func wordBoundaryAt(s string, i int) bool {
	if i <= 0 || i >= len(s) {
		return true
	}
	return !isWordByte(s[i-1]) || !isWordByte(s[i])
}

func randomID() string {
	b := make([]byte, 6)
	rand.Read(b)
	return hex.EncodeToString(b)
}
