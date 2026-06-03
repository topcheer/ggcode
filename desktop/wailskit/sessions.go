package wailskit

import (
	"fmt"
	"time"

	"github.com/topcheer/ggcode/internal/session"
)

// SessionInfo is a lightweight session record for the frontend.
type SessionInfo struct {
	ID        string `json:"id"`
	Title     string `json:"title"`
	Workspace string `json:"workspace"`
	Vendor    string `json:"vendor"`
	Model     string `json:"model"`
	MsgCount  int    `json:"msgCount"`
	UpdatedAt string `json:"updatedAt"`
}

// ListSessions returns all sessions sorted by UpdatedAt descending.
func ListSessions() ([]SessionInfo, error) {
	store, err := session.NewDefaultStore()
	if err != nil {
		return nil, fmt.Errorf("open session store: %w", err)
	}
	sessions, err := store.List()
	if err != nil {
		return nil, fmt.Errorf("list sessions: %w", err)
	}
	var result []SessionInfo
	for _, s := range sessions {
		result = append(result, SessionInfo{
			ID:        s.ID,
			Title:     s.Title,
			Workspace: s.Workspace,
			Vendor:    s.Vendor,
			Model:     s.Model,
			MsgCount:  len(s.Messages),
			UpdatedAt: s.UpdatedAt.Format(time.DateTime),
		})
	}
	return result, nil
}

// DeleteSession removes a session by ID.
func DeleteSession(id string) error {
	store, err := session.NewDefaultStore()
	if err != nil {
		return fmt.Errorf("open session store: %w", err)
	}
	return store.Delete(id)
}
