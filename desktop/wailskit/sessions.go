package wailskit

import (
	"encoding/json"
	"fmt"
	"strings"
	"sync"
	"time"

	"github.com/topcheer/ggcode/internal/agentruntime"
	"github.com/topcheer/ggcode/internal/debug"
	"github.com/topcheer/ggcode/internal/provider"
	"github.com/topcheer/ggcode/internal/security"
	"github.com/topcheer/ggcode/internal/session"
	"github.com/topcheer/ggcode/internal/tool"
	"github.com/topcheer/ggcode/internal/util"
)

type SessionInfo struct {
	ID          string `json:"id"`
	Title       string `json:"title"`
	Workspace   string `json:"workspace"`
	Vendor      string `json:"vendor"`
	Model       string `json:"model"`
	MsgCount    int    `json:"msgCount"`
	UpdatedAt   string `json:"updatedAt"`
	Locked      bool   `json:"locked"`
	LastMessage string `json:"lastMessage"`
}

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
			ID:          s.ID,
			Title:       s.Title,
			Workspace:   s.Workspace,
			Vendor:      s.Vendor,
			Model:       s.Model,
			MsgCount:    s.MsgCount,
			UpdatedAt:   s.UpdatedAt.Format(time.DateTime),
			Locked:      locked,
			LastMessage: s.LastMessage,
		})
	}
	return result, nil
}

func DeleteSession(id string) error {
	store, err := session.NewDefaultStore()
	if err != nil {
		return fmt.Errorf("open session store: %w", err)
	}

	storeDir, _ := session.DefaultDir()
	if bridge := GetChatBridge(); bridge != nil && bridge.CurrentSessionID() == id {

		bridge.Cancel()
		bridge.ClearCurrentSession()
		return store.Delete(id)
	} else {

		lock, lerr := session.TryAcquireSessionLock(storeDir, id)
		if lerr != nil {

			debug.Log("wailskit", "DeleteSession: failed to acquire lock for %s, proceeding anyway: %v", id, lerr)
			return store.Delete(id)
		}
		if lock == nil {
			return store.Delete(id)
		}
		if !lock.Acquired() {
			lock.Release()
			return fmt.Errorf("session %s is locked by another instance", id)
		}

		defer lock.Release()
		return store.Delete(id)
	}
}

func RenameSession(id string, title string) error {
	store, err := session.NewDefaultStore()
	if err != nil {
		return fmt.Errorf("open session store: %w", err)
	}
	ses, err := store.Load(id)
	if err != nil {
		return fmt.Errorf("load session: %w", err)
	}
	ses.Title = title

	if !ses.HasUserInteraction() {
		onDisk, derr := store.HasUserInteractionOnDisk(id)
		if derr == nil && onDisk {
			if full, ferr := store.LoadWithOptions(id, true); ferr == nil {
				full.Title = title
				ses = full
			}

		}
	}
	if err := store.AppendMetaToDisk(ses); err != nil {
		return err
	}

	if bridge := GetChatBridge(); bridge != nil {
		bridge.syncRenamedTitle(id, title)
	}
	return nil
}

var activeChatBridge *ChatBridge
var activeChatBridgeMu sync.RWMutex

func SetChatBridge(cb *ChatBridge) {
	activeChatBridgeMu.Lock()
	defer activeChatBridgeMu.Unlock()
	activeChatBridge = cb
}

func GetChatBridge() *ChatBridge {
	activeChatBridgeMu.RLock()
	defer activeChatBridgeMu.RUnlock()
	return activeChatBridge
}

func LoadSession(id string) error {
	bridge := GetChatBridge()
	if bridge == nil {
		return fmt.Errorf("no active chat bridge")
	}
	return bridge.LoadSession(id)
}

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
		"cron_create", "cron_delete", "cron_list", "cron_update", "cron_pause", "cron_resume", "cron_get", "lanchat", "teammate_spawn":
		if present, ok := tool.DescribeToolResult(toolName, rawArgs, result, isError); ok {
			if present.Payload != "" {
				return present.Payload
			}
			return present.Summary
		}
	}
	return result
}

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
					ID:      m.ID,
					Role:    m.Role,
					Content: block.Text,
				})

			case "tool_use":
				argsStr := string(block.Input)
				toolName := block.ToolName

				if block.ToolID != "" {
					toolCalls[block.ToolID] = resolvedToolCall{
						toolName: toolName,
						rawArgs:  argsStr,
					}
				}

				if shouldSkipHistoryTool(toolName) {
					continue
				}

				if toolName == "exit_plan_mode" {
					var args struct {
						Plan string `json:"plan"`
					}
					if json.Unmarshal([]byte(argsStr), &args) == nil && args.Plan != "" {
						result = append(result, SessionMessage{
							ID:      m.ID,
							Role:    "assistant",
							Content: args.Plan,
						})
					}
					continue
				}

				pres := tool.DescribeTool(toolName, argsStr)
				result = append(result, SessionMessage{
					ID:          m.ID,
					Role:        "tool",
					ToolName:    toolName,
					ToolID:      block.ToolID,
					ToolArgs:    argsStr,
					Content:     "",
					ToolDisplay: pres.DisplayName,
					ToolDetail:  pres.Detail,
				})

			case "tool_result":

				toolName := block.ToolName
				rawArgs := ""
				if tc, ok := toolCalls[block.ToolID]; ok {
					if toolName == "" {
						toolName = tc.toolName
					}
					rawArgs = tc.rawArgs
				}

				if shouldSkipHistoryTool(toolName) || toolName == "exit_plan_mode" {
					continue
				}

				content := suppressHistoryToolResult(toolName, rawArgs, block.Output, block.IsError)
				if content == "" && block.IsError {
					content = block.Output
					if content == "" {
						content = block.Text
					}
				}

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

func GetSessionHistory() ([]SessionMessage, error) {
	chat := GetChatBridge()
	if chat == nil {
		return nil, nil
	}
	return chat.CurrentSessionHistory(), nil
}

func ExportSessionToMarkdown(sessionID string) (string, error) {
	msgs, title, err := loadSessionForExport(sessionID)
	if err != nil {
		return "", err
	}
	return formatMessagesAsMarkdown(msgs, title), nil
}

func redactAndClamp(s string) string {
	out := security.RedactForDisplay(s)
	if len(out) > 2000 {
		out = out[:util.SnapToRuneStart(out, 2000)] + "\n... (truncated)\n"
	}
	return out
}

func formatMessagesAsMarkdown(msgs []SessionMessage, title string) string {
	var b strings.Builder
	b.WriteString("# ")
	if title != "" {
		b.WriteString(title)
	} else {
		b.WriteString("GGCode Session")
	}
	b.WriteString("\n\n")

	for _, msg := range msgs {
		switch msg.Role {
		case "user":
			b.WriteString("## User\n\n")
			b.WriteString(redactAndClamp(msg.Content))
			b.WriteString("\n\n")
		case "assistant":
			b.WriteString("## Assistant\n\n")
			b.WriteString(redactAndClamp(msg.Content))
			b.WriteString("\n\n")
		case "tool":
			label := msg.ToolDisplay
			if label == "" {
				label = msg.ToolName
			}
			if label == "" {
				label = "Tool"
			}
			b.WriteString("### ")
			b.WriteString(label)
			b.WriteString("\n\n")
			if msg.ToolDetail != "" {

				detail := security.RedactForDisplay(msg.ToolDetail)
				b.WriteString("```\n")
				b.WriteString(detail)
				b.WriteString("\n```\n\n")
			}
			if msg.Content != "" {

				content := security.RedactForDisplay(msg.Content)
				if len(content) > 2000 {

					cut := content[:util.SnapToRuneStart(content, 2000)]
					b.WriteString(cut)
					b.WriteString("\n... (truncated)\n")
				} else {
					b.WriteString(content)
					b.WriteString("\n")
				}
				b.WriteString("\n")
			}
		}
	}
	return b.String()
}

func ExportSessionToJSON(sessionID string) (string, error) {
	msgs, title, err := loadSessionForExport(sessionID)
	if err != nil {
		return "", err
	}
	return formatMessagesAsJSON(msgs, title)
}

func formatMessagesAsJSON(msgs []SessionMessage, title string) (string, error) {

	cleanMsgs := make([]SessionMessage, 0, len(msgs))
	for _, msg := range msgs {
		clean := msg

		clean.ToolArgs = security.RedactForDisplay(clean.ToolArgs)
		if len(clean.ToolArgs) > 2000 {
			clean.ToolArgs = clean.ToolArgs[:util.SnapToRuneStart(clean.ToolArgs, 2000)]
		}

		if clean.ToolDetail != "" {
			clean.ToolDetail = security.RedactForDisplay(clean.ToolDetail)
		}

		clean.Content = security.RedactForDisplay(clean.Content)
		if len(clean.Content) > 2000 {
			clean.Content = clean.Content[:util.SnapToRuneStart(clean.Content, 2000)]
		}

		cleanMsgs = append(cleanMsgs, clean)
	}

	export := struct {
		Title    string           `json:"title"`
		Messages []SessionMessage `json:"messages"`
	}{
		Title:    title,
		Messages: cleanMsgs,
	}

	data, err := json.MarshalIndent(export, "", "  ")
	if err != nil {
		return "", fmt.Errorf("marshal export: %w", err)
	}
	return string(data), nil
}

func loadSessionForExport(sessionID string) ([]SessionMessage, string, error) {
	if sessionID == "" {
		chat := GetChatBridge()
		if chat == nil {
			return nil, "", fmt.Errorf("no active session")
		}

		currentID := chat.CurrentSessionID()
		if currentID == "" {

			msgs := chat.CurrentSessionHistory()
			title := ""
			if ses := chat.CurrentSession(); ses != nil {
				title = ses.Title
			}
			return msgs, title, nil
		}
		sessionID = currentID
	}

	store, err := session.NewDefaultStore()
	if err != nil {
		return nil, "", fmt.Errorf("open session store: %w", err)
	}
	ses, err := store.LoadWithOptions(sessionID, true)
	if err != nil {
		return nil, "", fmt.Errorf("load session: %w", err)
	}

	msgs := mergeTunnelUserMessages(
		buildSessionHistoryFromMessages(ses.Messages),
		ses.SnapshotTunnelEvents(),
	)
	return msgs, ses.Title, nil
}
