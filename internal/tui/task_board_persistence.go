package tui

import (
	"encoding/json"

	"github.com/topcheer/ggcode/internal/session"
)

// snapshotTasksInto serializes the live task board into the session's
// persisted field. Call it before a session's metadata is flushed to disk
// so the board survives process restarts and session switches (long-running
// harness property: the board is durable session state, not process state).
func (m *Model) snapshotTasksInto(ses *session.Session) {
	if m == nil || ses == nil || m.taskMgr == nil {
		return
	}
	if data, err := m.taskMgr.SnapshotJSON(); err == nil {
		ses.TasksJSON = data
		// Capture the workspace fingerprint alongside the board so a later
		// resume can reconcile persisted claims against environment drift.
		// Best-effort: a fingerprint failure must not block the snapshot.
		if fp := captureEnvFingerprint(cwdOrEmpty()); fp != nil {
			if fpJSON, err := json.Marshal(fp); err == nil {
				ses.TasksEnvJSON = fpJSON
			}
		}
	}
}

// restoreTasksFromSession replaces the live task board with the one
// persisted in the session. An empty session field resets the board —
// correct both for brand-new sessions and for resumes of sessions that
// never used tasks.
func (m *Model) restoreTasksFromSession(ses *session.Session) {
	if m == nil || ses == nil || m.taskMgr == nil {
		return
	}
	_ = m.taskMgr.RestoreJSON(ses.TasksJSON)
}
