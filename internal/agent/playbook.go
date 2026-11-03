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

const (
	defaultMaxPlaybookEntries = 30
)

type PlaybookEntry struct {
	ID           string    `json:"id"`
	TaskType     string    `json:"task_type"`
	ToolSequence string    `json:"tool_sequence"`
	FileTypes    string    `json:"file_types"`
	Uses         int       `json:"uses"`
	SuccessRate  float64   `json:"success_rate"`
	AvgIter      float64   `json:"avg_iter"`
	AvgDurationS float64   `json:"avg_duration_s"`
	LastSeen     time.Time `json:"last_seen"`
	CreatedAt    time.Time `json:"created_at"`
}

type Playbook struct {
	mu         sync.Mutex
	path       string
	entries    []PlaybookEntry
	loaded     bool
	maxEntries int
}

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
		return
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

	tmp := pb.path + ".tmp"
	if err := os.WriteFile(tmp, data, 0644); err != nil {
		debug.Log("playbook", "failed to write playbook tmp: %v", err)
		return
	}
	if err := os.Rename(tmp, pb.path); err != nil {
		debug.Log("playbook", "failed to rename playbook: %v", err)
		os.Remove(tmp)
	}
}

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

func abstractToolSequence(tools map[string]int) string {
	categories := map[string]string{

		"read_file": "read", "multi_file_read": "read", "list_directory": "read",
		"glob": "read", "grep": "read", "search_files": "read",

		"edit_file": "edit", "multi_edit_file": "edit", "multi_file_edit": "edit",
		"write_file": "edit", "multi_file_write": "edit", "notebook_edit": "edit",

		"run_command": "exec", "start_command": "exec",

		"web_search": "search", "web_fetch": "search",

		"git_status": "vcs", "git_diff": "vcs", "git_log": "vcs", "git_add": "vcs",
		"git_commit": "vcs", "git_show": "vcs", "git_branch_list": "vcs",
		"git_remote": "vcs", "git_blame": "vcs", "git_stash": "vcs",
		"git_stash_list": "vcs",

		"lsp_definition": "lsp", "lsp_references": "lsp", "lsp_hover": "lsp",
		"lsp_symbols": "lsp", "lsp_workspace_symbols": "lsp", "lsp_diagnostics": "lsp",
		"lsp_rename": "lsp", "lsp_implementation": "lsp", "lsp_code_actions": "lsp",
		"lsp_prepare_call_hierarchy": "lsp", "lsp_incoming_calls": "lsp",
		"lsp_outgoing_calls":      "lsp",
		"lsp_document_highlights": "lsp",
	}

	seen := map[string]bool{}
	var parts []string

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
	var sorted []string
	for ext := range exts {
		sorted = append(sorted, ext)
	}
	sort.Strings(sorted)
	return strings.Join(sorted, "+")
}

func (pb *Playbook) Record(stats *RunStats) {
	if pb == nil || stats == nil {
		return
	}

	totalCalls := 0
	for _, c := range stats.ToolCalls {
		totalCalls += c
	}
	if totalCalls < 3 {
		return
	}

	pb.mu.Lock()
	defer pb.mu.Unlock()

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

	fingerprint := taskType + "|" + toolSeq + "|" + fileTypes

	for i := range pb.entries {
		e := &pb.entries[i]
		ep := e.TaskType + "|" + e.ToolSequence + "|" + e.FileTypes
		if ep == fingerprint {

			pb.updateEntry(e, stats)
			pb.prune()
			pb.save()
			debug.Log("playbook", "updated entry %s (uses=%d, success=%.1f%%)", e.TaskType, e.Uses, e.SuccessRate*100)
			return
		}
	}
	if !stats.Success {
		return
	}

	now := time.Now()
	entry := PlaybookEntry{
		ID:           randomID(),
		TaskType:     taskType,
		ToolSequence: toolSeq,
		FileTypes:    fileTypes,
		Uses:         1,
		SuccessRate:  1.0,
		AvgIter:      float64(stats.Iterations),
		AvgDurationS: stats.Duration.Seconds(),
		LastSeen:     now,
		CreatedAt:    now,
	}
	pb.entries = append(pb.entries, entry)

	pb.prune()

	if len(pb.entries) > pb.maxEntries {
		pb.evict()
	}

	pb.save()
	debug.Log("playbook", "recorded new %s strategy: %s (files=%s)", taskType, toolSeq, fileTypes)
}

func (pb *Playbook) updateEntry(e *PlaybookEntry, stats *RunStats) {
	n := float64(e.Uses)
	e.AvgIter = (e.AvgIter*n + float64(stats.Iterations)) / (n + 1)
	e.AvgDurationS = (e.AvgDurationS*n + stats.Duration.Seconds()) / (n + 1)
	outcome := 0.0
	if stats.Success {
		outcome = 1.0
	}
	e.SuccessRate = (e.SuccessRate*n + outcome) / (n + 1)
	e.Uses++
	e.LastSeen = time.Now()
}

const (
	pruneMinUses      = 5
	pruneSuccessFloor = 0.3
)

func (pb *Playbook) prune() {
	kept := pb.entries[:0]
	for _, e := range pb.entries {
		if e.Uses >= pruneMinUses && e.SuccessRate < pruneSuccessFloor {
			debug.Log("playbook", "pruned degraded %s strategy: %s (uses=%d, success=%.1f%%)",
				e.TaskType, e.ToolSequence, e.Uses, e.SuccessRate*100)
			continue
		}
		kept = append(kept, e)
	}
	pb.entries = kept
}

func (pb *Playbook) evict() {
	if len(pb.entries) <= pb.maxEntries {
		return
	}

	sort.Slice(pb.entries, func(i, j int) bool {
		return pb.entries[i].LastSeen.After(pb.entries[j].LastSeen)
	})
	pb.entries = pb.entries[:pb.maxEntries]
}

func (pb *Playbook) HintsForPrompt(runPrompt string, maxHints int) string {
	if pb == nil {
		return ""
	}
	pb.mu.Lock()
	defer pb.mu.Unlock()
	pb.load()

	if len(pb.entries) == 0 {
		return ""
	}

	intent := classifyTaskType(runPrompt)
	intentMatch := func(e PlaybookEntry) bool {
		return intent != "" && intent != "other" && e.TaskType == intent
	}
	sorted := make([]PlaybookEntry, len(pb.entries))
	copy(sorted, pb.entries)
	sort.Slice(sorted, func(i, j int) bool {
		mi, mj := intentMatch(sorted[i]), intentMatch(sorted[j])
		if mi != mj {
			return mi
		}
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

	for i := 0; i < maxHints; i++ {
		if sorted[i].Uses >= 3 {
			lines = append(lines, "Tip: a recurring read-only tool pattern above can be persisted once with the macro tool (action=define) and replayed by name (action=run).")
			break
		}
	}
	return strings.Join(lines, "\n")
}

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
