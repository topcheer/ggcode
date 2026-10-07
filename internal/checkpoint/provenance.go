package checkpoint

// sa-91: agent-native edit provenance queries (PROV-AGENT, arXiv 2508.02866;
// agent-native version control discussions 2025-2026). Checkpoints carry
// EditIntent (which todo item the edit served) stamped at save time; these
// helpers turn that trail into task-scoped views: "what did this task touch"
// and a narrative grouping suitable for PR descriptions or review. Deliberately
// read-only - RevertTask-style selective rollback is intentionally NOT offered
// here: interleaved edits to the same file from concurrent tasks make
// content-based revert unsafe, and that risk deserves its own design round.

import (
	"fmt"
	"strings"
)

// EditsForTask returns all checkpoints whose Intent.TaskID equals taskID, in
// chronological order. Empty slice when nothing matches (unknown task, legacy
// checkpoints without intent, or a read-only task).
func (m *Manager) EditsForTask(taskID string) []Checkpoint {
	if taskID == "" {
		return nil
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	var out []Checkpoint
	for _, cp := range m.checkpoints {
		if cp.Intent.TaskID == taskID {
			out = append(out, cp)
		}
	}
	return out
}

// TaskSummary names a task once from its first attributed checkpoint.
type TaskSummary struct {
	TaskID   string
	TaskDesc string
	Files    []string // unique file paths, first-touch order
	Edits    int      // number of attributed checkpoints
}

// TasksWithEdits summarizes every task that has at least one attributed
// checkpoint, in first-touch order. This is the "what did each of my todo
// items actually change" index.
func (m *Manager) TasksWithEdits() []TaskSummary {
	m.mu.Lock()
	defer m.mu.Unlock()
	var order []string
	byTask := map[string]*TaskSummary{}
	for _, cp := range m.checkpoints {
		id := cp.Intent.TaskID
		if id == "" {
			continue
		}
		ts, ok := byTask[id]
		if !ok {
			ts = &TaskSummary{TaskID: id, TaskDesc: cp.Intent.TaskDesc}
			byTask[id] = ts
			order = append(order, id)
		}
		ts.Edits++
		if !containsPath(ts.Files, cp.FilePath) {
			ts.Files = append(ts.Files, cp.FilePath)
		}
	}
	out := make([]TaskSummary, 0, len(order))
	for _, id := range order {
		out = append(out, *byTask[id])
	}
	return out
}

// Narrate renders a human-readable provenance report: one block per task
// (edit intent -> files -> edit count), plus a trailing count of unattributed
// checkpoints so legacy/no-todo edits stay visible instead of silently
// vanishing from the story. Empty string when there are no checkpoints.
func (m *Manager) Narrate() string {
	m.mu.Lock()
	snapshot := make([]Checkpoint, len(m.checkpoints))
	copy(snapshot, m.checkpoints)
	m.mu.Unlock()
	if len(snapshot) == 0 {
		return ""
	}
	unattributed := 0
	for _, cp := range snapshot {
		if cp.Intent.TaskID == "" {
			unattributed++
		}
	}
	var sb strings.Builder
	for _, ts := range m.TasksWithEdits() {
		desc := ts.TaskDesc
		if desc == "" {
			desc = "(no description)"
		}
		fmt.Fprintf(&sb, "[%s] %s\n  files: %s\n  edits: %d\n", ts.TaskID, desc, strings.Join(ts.Files, ", "), ts.Edits)
	}
	if unattributed > 0 {
		fmt.Fprintf(&sb, "(unattributed: %d checkpoint(s) without task intent)\n", unattributed)
	}
	return sb.String()
}

func containsPath(paths []string, p string) bool {
	for _, x := range paths {
		if x == p {
			return true
		}
	}
	return false
}
