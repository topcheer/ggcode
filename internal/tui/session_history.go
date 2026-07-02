package tui

import (
	"encoding/json"
	"strings"

	"github.com/topcheer/ggcode/internal/chat"
	"github.com/topcheer/ggcode/internal/provider"
)

type resumedToolCall struct {
	Presentation toolPresentation
	ToolName     string
	RawArgs      string
}

func (m *Model) rebuildConversationFromMessages(messages []provider.Message) {
	m.chatReset()
	m.streamBuffer = nil
	m.streamPrefixWritten = false
	toolCalls := make(map[string]resumedToolCall)
	for _, msg := range messages {
		m.renderConversationMessage(msg, toolCalls)
	}
	m.chatListScrollToBottom()
}

// restoreHistoryFromMessages extracts user text messages from a session
// and populates the input history so the user can recall previous prompts
// with ↑/↓ arrows after resuming a session.
func (m *Model) restoreHistoryFromMessages(messages []provider.Message) {
	m.history = m.history[:0]
	for _, msg := range messages {
		if msg.Role != "user" {
			continue
		}
		var parts []string
		for _, block := range msg.Content {
			if block.Type == "text" {
				if t := strings.TrimSpace(block.Text); t != "" {
					parts = append(parts, t)
				}
			}
		}
		if text := strings.Join(parts, "\n\n"); text != "" {
			m.history = append(m.history, text)
		}
	}
	m.historyIdx = len(m.history)
}

func (m *Model) renderConversationMessage(msg provider.Message, toolCalls map[string]resumedToolCall) {
	switch msg.Role {
	case "system":
		return
	case "user":
		m.renderConversationUserBlocks(msg.Content, toolCalls)
	default:
		m.renderConversationAssistantBlocks(msg.Content, toolCalls)
	}
}

// shouldSkipHistoryToolItem mirrors the skip logic in chatStartTool/chatFinishTool.
// Tools listed here are not rendered as visible chat items in real-time streaming,
// so they should also be skipped when loading historical messages.
func shouldSkipHistoryToolItem(toolName string) bool {
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

func (m *Model) renderConversationUserBlocks(blocks []provider.ContentBlock, toolCalls map[string]resumedToolCall) {
	var textParts []string
	for _, block := range blocks {
		switch block.Type {
		case "text":
			if strings.TrimSpace(block.Text) != "" {
				textParts = append(textParts, strings.TrimSpace(block.Text))
			}
		case "image":
			textParts = append(textParts, "_[image omitted]_")
		case "tool_result":
			if text := strings.TrimSpace(strings.Join(textParts, "\n\n")); text != "" {
				m.chatWriteUserMarkdown(nextChatID(), text)
				textParts = nil
			}
			// Resolve tool name and rawArgs from the tool_use that produced this result,
			// since tool_result blocks may not carry ToolName/RawArgs.
			toolName := block.ToolName
			rawArgs := ""
			if tc, ok := toolCalls[block.ToolID]; ok {
				if toolName == "" {
					toolName = tc.ToolName
				}
				rawArgs = tc.RawArgs
			}

			// Skip tool results for tools that have no visible chat item in real-time.
			if shouldSkipHistoryToolItem(toolName) || toolName == "todo_write" || toolName == "exit_plan_mode" {
				continue
			}

			// Update the corresponding chatList tool item with the result
			if block.ToolID != "" && m.chatList != nil {
				if item := m.chatList.FindByID(block.ToolID); item != nil {
					status := chat.StatusSuccess
					if block.IsError {
						status = chat.StatusError
					}
					m.chatUpdateToolStatus(block.ToolID, status)
					m.setToolResult(item, suppressToolResult(toolName, rawArgs, block.Output, block.IsError), block.IsError)
				}
			}
		}
	}
	if text := strings.TrimSpace(strings.Join(textParts, "\n\n")); text != "" {
		m.chatWriteUserMarkdown(nextChatID(), text)
	}
}

func (m *Model) renderConversationAssistantBlocks(blocks []provider.ContentBlock, toolCalls map[string]resumedToolCall) {
	var textParts []string
	for _, block := range blocks {
		switch block.Type {
		case "text":
			if strings.TrimSpace(block.Text) != "" {
				textParts = append(textParts, strings.TrimSpace(block.Text))
			}
		case "tool_use":
			if body := strings.TrimSpace(strings.Join(textParts, "\n\n")); body != "" {
				a := chat.NewAssistantItem(nextChatID(), m.chatStyles)
				a.SetText(body)
				a.SetFinished()
				m.chatList.Append(a)
				textParts = nil
			}
			toolID := block.ToolID
			if toolID == "" {
				toolID = nextChatID()
			}
			rawArgs := string(block.Input)
			toolName := block.ToolName

			// Record for later tool_result lookup
			if block.ToolID != "" {
				present := describeTool(m.currentLanguage(), toolName, rawArgs)
				toolCalls[block.ToolID] = resumedToolCall{
					Presentation: present,
					ToolName:     toolName,
					RawArgs:      rawArgs,
				}
			}

			// --- Match real-time chatStartTool/chatFinishTool behavior ---

			// 1. todo_write → restore TodoToolItem (same as real-time applyTodoWrite)
			if toolName == "todo_write" {
				if todos, ok := parseTodoSnapshot(rawArgs); ok {
					m.chatUpdateTodoItem(todos)
				}
				continue
			}

			// 2. spawn_agent → AgentToolItem (same as real-time chatStartTool)
			if toolName == "spawn_agent" {
				present := describeTool(m.currentLanguage(), toolName, rawArgs)
				taskDisplay := present.DisplayName
				if taskDisplay == "" {
					taskDisplay = present.Detail
				}
				if taskDisplay == "" {
					taskDisplay = rawArgs
				}
				item := chat.NewAgentToolItem(toolID, taskDisplay, chat.StatusSuccess, m.chatStyles)
				m.chatList.Append(item)
				continue
			}

			// 3. exit_plan_mode → render plan as assistant message (same as real-time chatFinishTool)
			if toolName == "exit_plan_mode" {
				var args struct {
					Plan string `json:"plan"`
				}
				if json.Unmarshal([]byte(rawArgs), &args) == nil && args.Plan != "" {
					a := chat.NewAssistantItem(toolID, m.chatStyles)
					a.SetText(args.Plan)
					a.SetFinished()
					m.chatList.Append(a)
				}
				continue
			}

			// 4. Skip tools that have no visible chat item in real-time
			if shouldSkipHistoryToolItem(toolName) {
				continue
			}

			// 5. Standard tool item (same as real-time chatStartTool)
			present := describeTool(m.currentLanguage(), toolName, rawArgs)
			item := chat.NewToolItem(toolID, chat.ToolContext{
				ToolName:    toolName,
				DisplayName: present.DisplayName,
				Detail:      present.Detail,
				RawArgs:     rawArgs,
				Lang:        string(m.currentLanguage()),
			}, chat.StatusSuccess, m.chatStyles)
			m.chatList.Append(item)
		case "image":
			textParts = append(textParts, "_[image omitted]_")
		}
	}
	if body := strings.TrimSpace(strings.Join(textParts, "\n\n")); body != "" {
		a := chat.NewAssistantItem(nextChatID(), m.chatStyles)
		a.SetText(body)
		a.SetFinished()
		m.chatList.Append(a)
	}
}
