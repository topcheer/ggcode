package tui

import (
	"strings"

	"github.com/topcheer/ggcode/internal/provider"
)

func (m *Model) rebuildConversationFromMessages(messages []provider.Message) {
	m.output.Reset()
	m.streamBuffer = nil
	m.streamPrefixWritten = false
	for _, msg := range messages {
		m.renderConversationMessage(msg)
	}
	m.syncConversationViewport()
	m.viewport.GotoBottom()
}

func (m *Model) renderConversationMessage(msg provider.Message) {
	switch msg.Role {
	case "user":
		text := strings.TrimSpace(renderConversationText(msg.Content))
		if text == "" {
			return
		}
		m.output.WriteString(m.renderConversationUserEntry("❯ ", text))
		m.output.WriteString("\n")
	default:
		body := strings.TrimSpace(renderConversationMarkdown(msg.Content))
		if body == "" {
			return
		}
		rendered := body
		if m.mdRenderer != nil {
			if output, err := m.mdRenderer.Render(body); err == nil {
				rendered = trimLeadingRenderedSpacing(output)
			}
		}
		m.output.WriteString(bulletStyle.Render("● "))
		m.output.WriteString(rendered)
		m.output.WriteString("\n")
	}
}

func renderConversationText(blocks []provider.ContentBlock) string {
	var parts []string
	for _, block := range blocks {
		if block.Type == "text" && strings.TrimSpace(block.Text) != "" {
			parts = append(parts, strings.TrimSpace(block.Text))
		}
	}
	return strings.Join(parts, "\n\n")
}

func renderConversationMarkdown(blocks []provider.ContentBlock) string {
	var parts []string
	for _, block := range blocks {
		switch block.Type {
		case "text":
			if strings.TrimSpace(block.Text) != "" {
				parts = append(parts, strings.TrimSpace(block.Text))
			}
		case "tool_use":
			text := "**Tool Call:** `" + strings.TrimSpace(block.ToolName) + "`"
			if len(block.Input) > 0 {
				text += "\n```json\n" + strings.TrimSpace(string(block.Input)) + "\n```"
			}
			parts = append(parts, text)
		case "tool_result":
			label := "**Tool Result:**"
			if block.IsError {
				label = "**Tool Result (error):**"
			}
			parts = append(parts, label+"\n```\n"+strings.TrimSpace(block.Output)+"\n```")
		case "image":
			parts = append(parts, "_[image omitted]_")
		}
	}
	return strings.Join(parts, "\n\n")
}
