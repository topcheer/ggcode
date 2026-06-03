package wailskit

import (
	"fmt"
	"time"

	"github.com/topcheer/ggcode/internal/session"
	"github.com/topcheer/ggcode/internal/tool"
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

// SessionMessage is a message from session history for the frontend.
type SessionMessage struct {
	Role        string `json:"role"`
	Content     string `json:"content"`
	ToolName    string `json:"toolName,omitempty"`
	ToolID      string `json:"toolID,omitempty"`
	ToolArgs    string `json:"toolArgs,omitempty"`
	ToolDisplay string `json:"toolDisplayName,omitempty"`
	ToolDetail  string `json:"toolDetail,omitempty"`
	IsError     bool   `json:"isError,omitempty"`
}

// GetSessionHistory loads messages from the current session.
func GetSessionHistory() ([]SessionMessage, error) {
	globalMu.RLock()
	chat := activeChatBridge
	globalMu.RUnlock()
	if chat == nil || chat.currentSes == nil {
		return nil, nil
	}
	msgs := chat.currentSes.Messages
	result := make([]SessionMessage, 0, len(msgs))
	for _, m := range msgs {
		for _, block := range m.Content {
			switch block.Type {
			case "text":
				result = append(result, SessionMessage{
					Role:    m.Role,
					Content: block.Text,
				})
			case "tool_use":
				argsStr := string(block.Input)
				pres := tool.DescribeTool(block.ToolName, argsStr)
				result = append(result, SessionMessage{
					Role:        "tool",
					ToolName:    block.ToolName,
					ToolID:      block.ToolID,
					ToolArgs:    argsStr,
					Content:     "",
					ToolDisplay: pres.DisplayName,
					ToolDetail:  pres.Detail,
				})
			case "tool_result":
				content := block.Output
				if content == "" {
					content = block.Text
				}
				// Update matching tool message with result
				for i := len(result) - 1; i >= 0; i-- {
					if result[i].ToolID == block.ToolID && result[i].Role == "tool" && result[i].Content == "" {
						result[i].Content = content
						result[i].IsError = block.IsError
						break
					}
				}
			}
		}
	}
	return result, nil
}
