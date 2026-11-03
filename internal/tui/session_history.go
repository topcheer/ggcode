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
		if text := strings.Join(parts, "\n\n"); text != "" && !isAutoInjectedUserMessage(text) {
			m.history = append(m.history, text)
		}
	}
	m.historyIdx = len(m.history)
}

func isAutoInjectedUserMessage(text string) bool {

	if len(text) < 20 {
		return false
	}

	if text[0] == '[' {

		head := text
		if len(head) > 50 {
			head = head[:50]
		}
		if strings.Contains(head, "]") {
			return true
		}
	}

	knownPhrases := []string{
		"Code quality advisory:",
		"Before finishing:",
		"Before finishing your request",
		"You are entering autopilot mode",
		"You are in autopilot mode",
	}
	for _, phrase := range knownPhrases {
		if strings.HasPrefix(text, phrase) {
			return true
		}
	}

	return false
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

			toolName := block.ToolName
			rawArgs := ""
			if tc, ok := toolCalls[block.ToolID]; ok {
				if toolName == "" {
					toolName = tc.ToolName
				}
				rawArgs = tc.RawArgs
			}

			if shouldSkipHistoryToolItem(toolName) || toolName == "todo_write" || toolName == "exit_plan_mode" {
				continue
			}

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

			if block.ToolID != "" {
				present := describeTool(m.currentLanguage(), toolName, rawArgs)
				toolCalls[block.ToolID] = resumedToolCall{
					Presentation: present,
					ToolName:     toolName,
					RawArgs:      rawArgs,
				}
			}

			if toolName == "todo_write" {
				if todos, ok := parseTodoSnapshot(rawArgs); ok {
					m.chatUpdateTodoItem(todos)
				}
				continue
			}

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

			if shouldSkipHistoryToolItem(toolName) {
				continue
			}

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
