package wailskit

import (
	"encoding/json"
	"fmt"
	"strings"
	"time"

	"github.com/topcheer/ggcode/internal/agentruntime"
	"github.com/topcheer/ggcode/internal/provider"
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
	Locked    bool   `json:"locked"`
}

// ListSessions returns sessions for the given workspace, sorted by UpdatedAt descending.
// If workingDir is empty, returns all sessions.
// The session currently held by the provided ChatBridge (if non-nil) is never
// reported as locked, since TryAcquireSessionLock would see the current process's own flock.
func ListSessions(workingDir string, bridge *ChatBridge) ([]SessionInfo, error) {
	store, err := session.NewDefaultStore()
	if err != nil {
		return nil, fmt.Errorf("open session store: %w", err)
	}
	sessions, err := store.List()
	if err != nil {
		return nil, fmt.Errorf("list sessions: %w", err)
	}

	summaries := agentruntime.SummarizeWorkspaceSessions(sessions, workingDir)
	storeDir, _ := session.DefaultDir()

	// Determine the session the current process already holds — skip lock check for it.
	activeID := ""
	if bridge != nil {
		activeID = bridge.CurrentSessionID()
	}

	result := make([]SessionInfo, 0, len(summaries))
	for _, s := range summaries {
		locked := false
		if s.ID != activeID {
			if lock, err := session.TryAcquireSessionLock(storeDir, s.ID); err == nil && lock != nil {
				if lock.Acquired() {
					lock.Release()
				} else {
					locked = true
				}
			}
		}
		result = append(result, SessionInfo{
			ID:        s.ID,
			Title:     s.Title,
			Workspace: s.Workspace,
			Vendor:    s.Vendor,
			Model:     s.Model,
			MsgCount:  s.MsgCount,
			UpdatedAt: s.UpdatedAt.Format(time.DateTime),
			Locked:    locked,
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

// GetChatBridge returns the active chat bridge.
func GetChatBridge() *ChatBridge {
	return activeChatBridge
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
	ID          string `json:"id,omitempty"`
	TurnID      string `json:"turn_id,omitempty"`
	Role        string `json:"role"`
	Content     string `json:"content"`
	ToolName    string `json:"toolName,omitempty"`
	ToolID      string `json:"toolID,omitempty"`
	ToolArgs    string `json:"toolArgs,omitempty"`
	ToolDisplay string `json:"toolDisplayName,omitempty"`
	ToolDetail  string `json:"toolDetail,omitempty"`
	IsError     bool   `json:"isError,omitempty"`
	Streaming   bool   `json:"streaming,omitempty"`
}

// shouldSkipHistoryTool mirrors the skip logic in the real-time streaming path.
// These tools create no visible chat item during streaming, so they should
// also be omitted when loading historical messages.
func shouldSkipHistoryTool(toolName string) bool {
	switch toolName {
	case "read_command_output", "wait_command", "stop_command",
		"write_command_input", "list_commands":
		return true
	case "enter_plan_mode":
		return true
	}
	if strings.HasPrefix(toolName, "lsp_") {
		return true
	}
	return false
}

// suppressHistoryToolResult applies the same result formatting as the real-time
// streaming path (chat_bridge.go suppressToolResult + tool.DescribeToolResult).
func suppressHistoryToolResult(toolName, rawArgs, result string, isError bool) string {
	switch toolName {
	case "web_fetch", "web_search", "stop_command", "todo_write", "list_agents":
		return ""
	case "start_command":
		return tool.StartCommandResultText(result, isError)
	case "team_create":
		return tool.TeamCreateResultText(result)
	case "swarm_task_create":
		return tool.SwarmTaskCreateResultMarkdown(result)
	case "task_create", "task_get", "task_update", "task_list", "task_stop", "task_output",
		"cron_create", "cron_delete", "cron_list", "lanchat", "teammate_spawn":
		if present, ok := tool.DescribeToolResult(toolName, rawArgs, result, isError); ok {
			if present.Payload != "" {
				return present.Payload
			}
			return present.Summary
		}
	}
	return result
}

// resolvedToolCall caches tool_use info for later tool_result matching.
type resolvedToolCall struct {
	toolName string
	rawArgs  string
}

func buildSessionHistoryFromMessages(msgs []provider.Message) []SessionMessage {
	result := make([]SessionMessage, 0, len(msgs))
	toolCalls := make(map[string]resolvedToolCall)

	for _, m := range msgs {
		if m.Role == "system" {
			continue
		}
		for _, block := range m.Content {
			switch block.Type {
			case "text":
				if strings.TrimSpace(block.Text) == "" {
					continue
				}
				result = append(result, SessionMessage{
					Role:    m.Role,
					Content: block.Text,
				})

			case "tool_use":
				argsStr := string(block.Input)
				toolName := block.ToolName

				// Cache for tool_result lookup
				if block.ToolID != "" {
					toolCalls[block.ToolID] = resolvedToolCall{
						toolName: toolName,
						rawArgs:  argsStr,
					}
				}

				// Skip tools that have no visible item in real-time
				if shouldSkipHistoryTool(toolName) {
					continue
				}

				// exit_plan_mode → render plan as assistant text
				if toolName == "exit_plan_mode" {
					var args struct {
						Plan string `json:"plan"`
					}
					if json.Unmarshal([]byte(argsStr), &args) == nil && args.Plan != "" {
						result = append(result, SessionMessage{
							Role:    "assistant",
							Content: args.Plan,
						})
					}
					continue
				}

				pres := tool.DescribeTool(toolName, argsStr)
				result = append(result, SessionMessage{
					Role:        "tool",
					ToolName:    toolName,
					ToolID:      block.ToolID,
					ToolArgs:    argsStr,
					Content:     "",
					ToolDisplay: pres.DisplayName,
					ToolDetail:  pres.Detail,
				})

			case "tool_result":
				// Resolve tool name and rawArgs from the tool_use that produced this result
				toolName := block.ToolName
				rawArgs := ""
				if tc, ok := toolCalls[block.ToolID]; ok {
					if toolName == "" {
						toolName = tc.toolName
					}
					rawArgs = tc.rawArgs
				}

				// Skip tool results for tools that have no visible item
				if shouldSkipHistoryTool(toolName) || toolName == "exit_plan_mode" {
					continue
				}

				// Format result the same way as real-time streaming
				content := suppressHistoryToolResult(toolName, rawArgs, block.Output, block.IsError)
				if content == "" && block.IsError {
					content = block.Output
					if content == "" {
						content = block.Text
					}
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
	return result
}

// GetSessionHistory loads messages from the current session.
func GetSessionHistory() ([]SessionMessage, error) {
	globalMu.RLock()
	chat := activeChatBridge
	globalMu.RUnlock()
	if chat == nil {
		return nil, nil
	}
	return chat.CurrentSessionHistory(), nil
}
