package task

import (
	"encoding/json"
	"fmt"
	"sort"
	"time"
)

// snapshotVersion is the schema version of the persisted task board.
// Bump when the snapshot format changes; RestoreJSON rejects newer
// versions instead of silently misinterpreting their fields.
const snapshotVersion = 1

// persistedTask is the JSON representation of a Task for persistence.
// A dedicated DTO with explicit tags keeps the on-disk schema stable even
// if Task's own marshaled form changes (Task is also serialized into tool
// results shown to the model, so its shape must not be repurposed).
type persistedTask struct {
	ID          string            `json:"id"`
	Subject     string            `json:"subject"`
	Description string            `json:"description"`
	ActiveForm  string            `json:"active_form,omitempty"`
	Status      TaskStatus        `json:"status"`
	Owner       string            `json:"owner,omitempty"`
	Blocks      []string          `json:"blocks,omitempty"`
	BlockedBy   []string          `json:"blocked_by,omitempty"`
	Metadata    map[string]string `json:"metadata,omitempty"`
	CreatedAt   time.Time         `json:"created_at"`
	UpdatedAt   time.Time         `json:"updated_at"`
}

// snapshotFormat is the versioned envelope for a persisted task board.
type snapshotFormat struct {
	Version int             `json:"version"`
	NextID  int             `json:"next_id"`
	Tasks   []persistedTask `json:"tasks"`
}

// SnapshotJSON serializes the full board - tasks, statuses, dependency
// edges, metadata and the ID counter - so it can be persisted outside the
// process (e.g. alongside the session on disk) and restored on resume.
// Output is deterministic: tasks are ordered by ID.
func (m *Manager) SnapshotJSON() ([]byte, error) {
	m.mu.Lock()
	defer m.mu.Unlock()

	tasks := make([]persistedTask, 0, len(m.tasks))
	for _, t := range m.tasks {
		cp := t.Snapshot()
		tasks = append(tasks, persistedTask{
			ID:          cp.ID,
			Subject:     cp.Subject,
			Description: cp.Description,
			ActiveForm:  cp.ActiveForm,
			Status:      cp.Status,
			Owner:       cp.Owner,
			Blocks:      cp.Blocks,
			BlockedBy:   cp.BlockedBy,
			Metadata:    cp.Metadata,
			CreatedAt:   cp.CreatedAt,
			UpdatedAt:   cp.UpdatedAt,
		})
	}
	sort.Slice(tasks, func(i, j int) bool { return tasks[i].ID < tasks[j].ID })
	return json.Marshal(snapshotFormat{Version: snapshotVersion, NextID: m.nextID, Tasks: tasks})
}

// RestoreJSON replaces the board with a snapshot produced by SnapshotJSON.
// Nil/empty input resets the board to empty - correct for brand-new
// sessions and for resumes of sessions that never used tasks. On a decode
// or version error the live board is left untouched, so a corrupt or
// future-versioned snapshot can never wipe in-memory tasks.
func (m *Manager) RestoreJSON(data []byte) error {
	m.mu.Lock()
	defer m.mu.Unlock()

	if len(data) == 0 {
		m.tasks = make(map[string]*Task)
		m.nextID = 0
		return nil
	}
	var snap snapshotFormat
	if err := json.Unmarshal(data, &snap); err != nil {
		return fmt.Errorf("decode task board snapshot: %w", err)
	}
	if snap.Version > snapshotVersion {
		return fmt.Errorf("task board snapshot version %d is newer than supported version %d", snap.Version, snapshotVersion)
	}

	rebuilt := make(map[string]*Task, len(snap.Tasks))
	for _, pt := range snap.Tasks {
		if pt.ID == "" {
			continue
		}
		t := &Task{
			ID:          pt.ID,
			Subject:     pt.Subject,
			Description: pt.Description,
			ActiveForm:  pt.ActiveForm,
			Status:      pt.Status,
			Owner:       pt.Owner,
			CreatedAt:   pt.CreatedAt,
			UpdatedAt:   pt.UpdatedAt,
		}
		if len(pt.Blocks) > 0 {
			t.Blocks = append([]string(nil), pt.Blocks...)
		}
		if len(pt.BlockedBy) > 0 {
			t.BlockedBy = append([]string(nil), pt.BlockedBy...)
		}
		if len(pt.Metadata) > 0 {
			t.Metadata = make(map[string]string, len(pt.Metadata))
			for k, v := range pt.Metadata {
				t.Metadata[k] = v
			}
		}
		rebuilt[pt.ID] = t
	}
	m.tasks = rebuilt
	m.nextID = snap.NextID
	// Never reuse an ID that was already handed out, even if the snapshot
	// came from an older writer that forgot next_id (or hand-edited data).
	for id := range rebuilt {
		if n := numericTaskID(id); n >= m.nextID {
			m.nextID = n + 1
		}
	}
	return nil
}

// numericTaskID extracts the numeric suffix of a "task-<n>" ID.
// Returns 0 for malformed or non-numeric IDs (which are then never reused
// as generated IDs either - they simply don't advance the counter).
func numericTaskID(id string) int {
	var n int
	if _, err := fmt.Sscanf(id, "task-%d", &n); err != nil {
		return 0
	}
	return n
}

// BoardStats summarizes a persisted task-board snapshot without building a
// live Manager. Used by resume reconciliation to report board scale alongside
// environment-drift findings. ok is false for empty or undecodable snapshots.
func BoardStats(data []byte) (completed, inProgress, pending int, ok bool) {
	if len(data) == 0 {
		return 0, 0, 0, false
	}
	var snap snapshotFormat
	if err := json.Unmarshal(data, &snap); err != nil {
		return 0, 0, 0, false
	}
	for _, t := range snap.Tasks {
		switch t.Status {
		case StatusCompleted:
			completed++
		case StatusInProgress:
			inProgress++
		case StatusPending:
			pending++
		}
	}
	return completed, inProgress, pending, true
}
