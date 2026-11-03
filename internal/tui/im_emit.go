package tui

import (
	"encoding/json"
	"fmt"
	"strings"

	"github.com/topcheer/ggcode/internal/im"
	toolpkg "github.com/topcheer/ggcode/internal/tool"
	"github.com/topcheer/ggcode/internal/util"
)

func (m *Model) emitIMEvent(event im.OutboundEvent) {
	if m.imEmitter == nil {
		return
	}
	m.imEmitter.EmitEvent(event)
}

func (m *Model) emitIMText(text string) {
	if m.imEmitter == nil {
		return
	}
	m.imEmitter.EmitText(text)
}

func (m *Model) emitIMLocalUserText(text string) {
	if m.imEmitter == nil {
		return
	}
	m.imEmitter.EmitUserText(text)
}

func (m *Model) emitIMLocalUserTextExcept(text, excludeAdapter string) {
	if m.imEmitter == nil {
		return
	}
	if excludeAdapter == "" {
		m.imEmitter.EmitUserText(text)
		return
	}
	m.imEmitter.EmitUserTextExcept(text, excludeAdapter)
}

func (m *Model) emitIMStatus(status string) {
	if m.imEmitter == nil {
		return
	}
	m.imEmitter.EmitStatus(status)
}

func (m *Model) pendingIMStreamText() string {
	if m.streamBuffer == nil {
		return ""
	}
	return strings.TrimSpace(m.streamBuffer.String())
}

func (m *Model) emitIMRoundSummary(text string, toolCalls, toolSuccesses, toolFailures int) {
	if m.imEmitter == nil {
		return
	}
	m.imEmitter.EmitRoundSummary(text, toolCalls, toolSuccesses, toolFailures)
}

func (m *Model) emitIMAskUser(text string) {
	if m.imEmitter == nil {
		return
	}
	m.imEmitter.EmitAskUser(text)
}

func (m *Model) emitIMAskUserInteractive(title string, question toolpkg.AskUserQuestion, fallbackText string) {
	if m.imEmitter == nil {
		if fallbackText != "" {
			m.emitIMAskUser(fallbackText)
		}
		return
	}
	m.imEmitter.EmitAskUserInteractive(title, question, fallbackText)
}

func (m *Model) formatIMAskUserPrompt(rawArgs string) string {
	if m.imEmitter == nil {
		return ""
	}
	return m.imEmitter.FormatAskUserPrompt(rawArgs)
}

func (m *Model) formatIMAskUserQuestion(title string, question toolpkg.AskUserQuestion) string {
	req := toolpkg.AskUserRequest{
		Title:     strings.TrimSpace(title),
		Questions: []toolpkg.AskUserQuestion{question},
	}
	data, err := json.Marshal(req)
	if err != nil {
		return strings.TrimSpace(util.FirstNonEmpty(question.Prompt, question.Title))
	}
	return m.formatIMAskUserPrompt(string(data))
}

func (m *Model) emitIMApproval(toolName, input string) {
	if m.imEmitter == nil {
		return
	}
	m.imEmitter.EmitText(im.FormatApprovalRequest(m.imToolLanguage(), toolName, input))
}

func (m *Model) emitIMApprovalResult(toolName string, decision string) {
	if m.imEmitter == nil {
		return
	}
	if msg := im.FormatApprovalResult(m.imToolLanguage(), toolName, decision); msg != "" {
		m.imEmitter.EmitText(msg)
	}
}

func (m *Model) imToolLanguage() im.ToolLanguage {
	switch m.currentLanguage() {
	case LangZhCN:
		return im.ToolLangZhCN
	default:
		return im.ToolLangEn
	}
}

func (m *Model) emitIMQuestionnaireSummary(req toolpkg.AskUserRequest, resp toolpkg.AskUserResponse) {
	if m.imEmitter == nil {
		return
	}
	var sb strings.Builder
	title := strings.TrimSpace(req.Title)
	if title != "" {
		sb.WriteString("📋 **")
		sb.WriteString(title)
		sb.WriteString("**\n\n")
	}
	for i, q := range req.Questions {
		answer := ""
		if i < len(resp.Answers) && resp.Answers[i].Answered {
			a := resp.Answers[i]
			switch {
			case len(a.SelectedChoices) > 0 && a.FreeformText != "":
				answer = strings.Join(a.SelectedChoices, ", ") + " + " + a.FreeformText
			case len(a.SelectedChoices) > 0:
				answer = strings.Join(a.SelectedChoices, ", ")
			case a.FreeformText != "":
				answer = a.FreeformText
			}
		}
		sb.WriteString(fmt.Sprintf("**%d. %s**\n", i+1, util.FirstNonEmpty(q.Prompt, q.Title)))
		if answer != "" {
			sb.WriteString(fmt.Sprintf("  → %s\n", answer))
		} else {

			unanswered := "_Unanswered_"
			if m.imToolLanguage() == im.ToolLangZhCN {
				unanswered = "_未回答_"
			}
			sb.WriteString("  → " + unanswered + "\n")
		}
	}
	m.emitIMAskUser(strings.TrimSpace(sb.String()))
}
