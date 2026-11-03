package checkpoint

import (
	"fmt"
	"strings"
)

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

type TaskSummary struct {
	TaskID   string
	TaskDesc string
	Files    []string
	Edits    int
}

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
