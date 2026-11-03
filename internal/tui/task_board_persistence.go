package tui

import (
	"encoding/json"

	"github.com/topcheer/ggcode/internal/session"
)

func (m *Model) snapshotTasksInto(ses *session.Session) {
	if m == nil || ses == nil || m.taskMgr == nil {
		return
	}
	if data, err := m.taskMgr.SnapshotJSON(); err == nil {
		ses.TasksJSON = data

		if fp := captureEnvFingerprint(cwdOrEmpty()); fp != nil {
			if fpJSON, err := json.Marshal(fp); err == nil {
				ses.TasksEnvJSON = fpJSON
			}
		}
	}
}

func (m *Model) restoreTasksFromSession(ses *session.Session) {
	if m == nil || ses == nil || m.taskMgr == nil {
		return
	}
	_ = m.taskMgr.RestoreJSON(ses.TasksJSON)
}
