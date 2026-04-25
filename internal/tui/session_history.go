package tui

import (
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
				m.chatWriteUser(nextChatID(), text)
				textParts = nil
			}
			// Update the corresponding chatList tool item with the result
			if block.ToolID != "" && m.chatList != nil {
				if item := m.chatList.FindByID(block.ToolID); item != nil {
					status := chat.StatusSuccess
					if block.IsError {
						status = chat.StatusError
					}
					m.chatUpdateToolStatus(block.ToolID, status)
					m.setToolResult(item, block.Output)
				}
			}
		}
	}
	if text := strings.TrimSpace(strings.Join(textParts, "\n\n")); text != "" {
		m.chatWriteUser(nextChatID(), text)
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
				m.chatList.Append(a)
				textParts = nil
			}
			toolID := block.ToolID
			if toolID == "" {
				toolID = nextChatID()
			}
			present := describeTool(m.currentLanguage(), block.ToolName, string(block.Input))
			item := chat.NewToolItem(toolID, chat.ToolContext{
				ToolName:    block.ToolName,
				DisplayName: present.DisplayName,
				Detail:      present.Detail,
				RawArgs:     string(block.Input),
			}, chat.StatusSuccess, m.chatStyles)
			m.chatList.Append(item)
			if block.ToolID != "" {
				toolCalls[block.ToolID] = resumedToolCall{
					Presentation: present,
					ToolName:     block.ToolName,
					RawArgs:      string(block.Input),
				}
			}
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
