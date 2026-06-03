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

// ListSessions returns sessions for the given workspace, sorted by UpdatedAt descending.
// If workingDir is empty, returns all sessions.
func ListSessions(workingDir string) ([]SessionInfo, error) {
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
		if workingDir != "" && s.Workspace != workingDir {
			continue
		}
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

// NewSession clears the current session so next chat creates a fresh one.
// The chat bridge must be set via SetChatBridge before calling.
var activeChatBridge *ChatBridge

// SetChatBridge stores the active chat bridge for session management.
func SetChatBridge(cb *ChatBridge) {
	activeChatBridge = cb
}

func NewSession() error {
	if activeChatBridge != nil {
		activeChatBridge.ClearCurrentSession()
	}
	return nil
}

func LoadSession(id string) error {
	if activeChatBridge == nil {
		return fmt.Errorf("no active chat bridge")
	}
	return activeChatBridge.LoadSession(id)
}
