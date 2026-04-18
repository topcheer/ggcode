package tui

import (
	"encoding/json"
	"slices"
	"strings"
	"time"

	"github.com/topcheer/ggcode/internal/im"
	"github.com/topcheer/ggcode/internal/subagent"
	toolpkg "github.com/topcheer/ggcode/internal/tool"
)

// IM emission methods delegate to the shared im.IMEmitter.

func (m *Model) emitIMEvent(event im.OutboundEvent) {
	if m.imEmitter == nil {
		return
	}
	m.imEmitter.EmitEvent(event)
}

func (m *Model) triggerIMTyping() {
	if m.imEmitter == nil {
		return
	}
	m.imEmitter.TriggerTyping()
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

// emitIMLocalUserTextExcept sends user echo to all channels except the originating adapter.
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

func (m *Model) emitIMStatusMsg(msg statusMsg) {
	status := m.formatIMStatus(msg)
	if status == "" {
		return
	}
	m.emitIMStatus(status)
}

func (m *Model) emitIMSubAgentStatus() {
	status := m.currentSubAgentIMStatus()
	if status == "" {
		return
	}
	m.emitIMStatus(status)
}

func (m *Model) formatIMStatus(msg statusMsg) string {
	activity := strings.TrimSpace(msg.Activity)
	toolSummary := strings.TrimSpace(formatToolInline(msg.ToolName, msg.ToolArg))
	thinking := strings.TrimSpace(m.t("status.thinking"))
	writing := strings.TrimSpace(m.t("status.writing"))
	if toolSummary == "" && (activity == thinking || activity == writing) {
		return ""
	}

	lang := m.currentLanguage()
	switch {
	case toolSummary != "" && (activity == "" || activity == thinking || activity == writing):
		return localizeIMProgress(lang, toolSummary)
	case activity != "":
		return localizeIMProgress(lang, activity)
	case toolSummary != "":
		return localizeIMProgress(lang, toolSummary)
	default:
		return ""
	}
}

// localizeIMProgress applies language-specific localization to an IM progress string.
// This wraps the shared im.LocalizeIMProgress for TUI's Language type.
func localizeIMProgress(lang Language, text string) string {
	var tl im.ToolLanguage
	switch lang {
	case LangZhCN:
		tl = im.ToolLangZhCN
	default:
		tl = im.ToolLangEn
	}
	return im.LocalizeIMProgress(tl, text)
}

func (m *Model) currentSubAgentIMStatus() string {
	if m.subAgentMgr == nil {
		return ""
	}
	live := make([]*subagent.SubAgent, 0)
	for _, sa := range m.subAgentMgr.List() {
		if sa == nil || !isLiveSubAgentStatus(sa.Status) {
			continue
		}
		live = append(live, sa)
	}
	if len(live) == 0 {
		return ""
	}
	slices.SortFunc(live, func(a, b *subagent.SubAgent) int {
		aTime := firstNonZeroTime(a.StartedAt, a.CreatedAt)
		bTime := firstNonZeroTime(b.StartedAt, b.CreatedAt)
		switch {
		case aTime.After(bTime):
			return -1
		case aTime.Before(bTime):
			return 1
		default:
			return strings.Compare(a.ID, b.ID)
		}
	})
	sa := live[0]
	summary := strings.TrimSpace(m.subAgentActivitySummary(sa))
	if summary == "" {
		return ""
	}
	task := strings.TrimSpace(firstNonEmpty(sa.DisplayTask, sa.Task))
	switch m.currentLanguage() {
	case LangZhCN:
		if task == "" {
			return "子任务：" + summary
		}
		return "子任务「" + task + "」：" + summary
	default:
		if task == "" {
			return "Sub-agent: " + summary
		}
		return "Sub-agent \"" + task + "\": " + summary
	}
}

func firstNonZeroTime(values ...time.Time) time.Time {
	for _, value := range values {
		if !value.IsZero() {
			return value
		}
	}
	return time.Time{}
}

func (m *Model) pendingIMStreamText() string {
	if m.streamBuffer == nil {
		return ""
	}
	return strings.TrimSpace(m.streamBuffer.String())
}

func (m *Model) emitIMRoundProgress(text string) {
	_ = text
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
		return strings.TrimSpace(firstNonEmpty(question.Prompt, question.Title))
	}
	return m.formatIMAskUserPrompt(string(data))
}
