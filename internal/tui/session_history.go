package tui

import (
	"strings"

	"charm.land/lipgloss/v2"
	"github.com/topcheer/ggcode/internal/provider"
)

type resumedToolCall struct {
	Presentation toolPresentation
	ToolName     string
	RawArgs      string
}

func (m *Model) rebuildConversationFromMessages(messages []provider.Message) {
	m.output.Reset()
	m.chatEntries.Reset()
	m.streamBuffer = nil
	m.streamPrefixWritten = false
	toolCalls := make(map[string]resumedToolCall)
	for _, msg := range messages {
		m.renderConversationMessage(msg, toolCalls)
	}
	m.syncConversationViewport()
	m.viewport.GotoBottom()
}

func (m *Model) renderConversationMessage(msg provider.Message, toolCalls map[string]resumedToolCall) {
	switch msg.Role {
	case "system":
		return
	case "user":
		m.ensureOutputHasBlankLine()
		m.renderConversationUserBlocks(msg.Content, toolCalls)
	default:
		m.ensureOutputHasBlankLine()
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
				m.output.WriteString(m.renderConversationUserEntry("❯ ", text))
				m.output.WriteString("\n")
				textParts = nil
			}
			m.output.WriteString(m.renderConversationToolResult(block, toolCalls))
		}
	}
	if text := strings.TrimSpace(strings.Join(textParts, "\n\n")); text != "" {
		m.output.WriteString(m.renderConversationUserEntry("❯ ", text))
		m.output.WriteString("\n")
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
				m.renderConversationAssistantMarkdown(body)
				textParts = nil
			}
			renderedCall := m.renderConversationToolCall(block)
			m.output.WriteString(renderedCall)
			m.chatEntries.Append(ChatEntry{Role: "tool", RawText: renderedCall})
			if block.ToolID != "" {
				toolCalls[block.ToolID] = resumedToolCall{
					Presentation: describeTool(m.currentLanguage(), block.ToolName, string(block.Input)),
					ToolName:     block.ToolName,
					RawArgs:      string(block.Input),
				}
			}
		case "image":
			textParts = append(textParts, "_[image omitted]_")
		}
	}
	if body := strings.TrimSpace(strings.Join(textParts, "\n\n")); body != "" {
		m.renderConversationAssistantMarkdown(body)
	}
}

func (m *Model) renderConversationAssistantMarkdown(body string) {
	rendered := trimLeadingRenderedSpacing(RenderMarkdownWidth(body, max(20, m.conversationInnerWidth()-2)))
	m.output.WriteString(assistantBulletStyle.Render("● "))
	m.output.WriteString(rendered)
	m.output.WriteString("\n")
}

func (m *Model) renderConversationToolCall(block provider.ContentBlock) string {
	if isCommandTool(block.ToolName) {
		item, ok := buildCommandToolActivityItem(m.currentLanguage(), ToolStatusMsg{
			ToolName: block.ToolName,
			RawArgs:  string(block.Input),
			Running:  true,
		})
		if ok {
			return m.renderConversationCommandCall(item)
		}
	}
	present := describeTool(m.currentLanguage(), block.ToolName, string(block.Input))
	return FormatToolStart(ToolStatusMsg{
		ToolName:    block.ToolName,
		DisplayName: present.DisplayName,
		Detail:      present.Detail,
		Activity:    present.Activity,
		RawArgs:     string(block.Input),
		Args:        compactToolArgsPreview(string(block.Input)),
		Running:     true,
	})
}

func (m *Model) renderConversationToolResult(block provider.ContentBlock, toolCalls map[string]resumedToolCall) string {
	relOutput := relativizeResult(block.Output)
	state, ok := toolCalls[block.ToolID]
	if ok && isCommandTool(state.ToolName) {
		item, ok := buildCommandToolActivityItem(m.currentLanguage(), ToolStatusMsg{
			ToolName:    state.ToolName,
			DisplayName: state.Presentation.DisplayName,
			Detail:      state.Presentation.Detail,
			RawArgs:     state.RawArgs,
			Result:      relOutput,
			IsError:     block.IsError,
			Running:     false,
		})
		if ok {
			return m.renderConversationCommandResult(item, block.IsError)
		}
	}
	present := state.Presentation
	if !ok {
		present = describeTool(m.currentLanguage(), block.ToolName, "")
	}
	return FormatToolResult(m.currentLanguage(), ToolStatusMsg{
		ToolName:    block.ToolName,
		DisplayName: present.DisplayName,
		Detail:      present.Detail,
		Result:      relOutput,
		IsError:     block.IsError,
		Running:     false,
	})
}

func (m *Model) renderConversationCommandCall(item toolActivityItem) string {
	commandStyle := lipgloss.NewStyle().Foreground(lipgloss.Color("214"))

	var rows []string
	commandLines := append([]string(nil), item.CommandLines...)
	if item.CommandTitle == "" && len(commandLines) > 0 {
		first := appendHiddenLineSuffix(m.currentLanguage(), commandLines[0], item.CommandHiddenLineCount, "command")
		rows = append(rows, toolBulletStyle.Render("● ")+commandStyle.Render(first))
		commandLines = commandLines[1:]
	} else {
		header := item.CommandTitle
		if header == "" {
			header = item.Summary
		}
		rows = append(rows, toolBulletStyle.Render("● ")+header)
	}
	for i, line := range commandLines {
		if i == len(commandLines)-1 {
			line = appendHiddenLineSuffix(m.currentLanguage(), line, item.CommandHiddenLineCount, "command")
		}
		rows = append(rows, "  "+commandStyle.Render(line))
	}
	return strings.Join(rows, "\n") + "\n"
}

func (m *Model) renderConversationCommandResult(item toolActivityItem, isError bool) string {
	style := lipgloss.NewStyle().Foreground(lipgloss.Color("45"))
	if isError {
		style = lipgloss.NewStyle().Foreground(lipgloss.Color("9"))
	}
	bodyWidth := m.conversationInnerWidth() - 4
	if bodyWidth < 8 {
		bodyWidth = 8
	}
	if len(item.OutputLines) == 0 {
		return FormatToolResult(m.currentLanguage(), ToolStatusMsg{
			Result:  "",
			IsError: isError,
			Running: false,
		})
	}
	rows := make([]string, 0, len(item.OutputLines))
	for i, line := range item.OutputLines {
		if i == len(item.OutputLines)-1 {
			line = appendHiddenLineSuffix(m.currentLanguage(), line, item.OutputHiddenLineCount, "output")
		}
		prefix := "  "
		if i == 0 {
			prefix = "  └ "
		}
		rows = append(rows, prefix+style.Render(truncateString(line, bodyWidth)))
	}
	return strings.Join(rows, "\n") + "\n"
}
