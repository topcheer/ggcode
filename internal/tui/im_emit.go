package tui

import (
	"encoding/json"
	"fmt"
	"github.com/topcheer/ggcode/internal/util"
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
	task := strings.TrimSpace(util.FirstNonEmpty(sa.DisplayTask, sa.Task))
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

// emitIMAskUserInteractive sends ask_user to IM via the shared emitter,
// which handles interactive buttons + fallback logic in one place.
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

// emitIMQuestionnaireSummary sends a summary of all questions and their answers to IM
// after the TUI user submits the questionnaire. This gives IM users visibility into
// what was answered.
// emitIMApproval pushes a tool permission approval request to IM channels.
// The user can reply with y/a/n to approve or deny.
func (m *Model) emitIMApproval(toolName, input string) {
	if m.imEmitter == nil {
		return
	}
	lang := m.currentLanguage()
	detail := formatToolInline(toolName, input)
	var msg string
	if lang == LangZhCN {
		msg = fmt.Sprintf("🔒 需要审批: %s\n\n回复 y 允许 · a 总是允许 · n 拒绝", detail)
	} else {
		msg = fmt.Sprintf("🔒 Approval required: %s\n\nReply y allow · a always allow · n deny", detail)
	}
	m.imEmitter.EmitText(msg)
}

func (m *Model) emitIMApprovalResult(toolName string, decision string) {
	if m.imEmitter == nil {
		return
	}
	lang := m.currentLanguage()
	detail := formatToolInline(toolName, "")
	var msg string
	if lang == LangZhCN {
		switch decision {
		case "allow":
			msg = fmt.Sprintf("✅ 已允许: %s", detail)
		case "always":
			msg = fmt.Sprintf("✅ 已总是允许: %s", detail)
		case "deny":
			msg = fmt.Sprintf("❌ 已拒绝: %s", detail)
		}
	} else {
		switch decision {
		case "allow":
			msg = fmt.Sprintf("✅ Allowed: %s", detail)
		case "always":
			msg = fmt.Sprintf("✅ Always allowed: %s", detail)
		case "deny":
			msg = fmt.Sprintf("❌ Denied: %s", detail)
		}
	}
	if msg != "" {
		m.imEmitter.EmitText(msg)
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
			sb.WriteString("  → _未回答_\n")
		}
	}
	m.emitIMAskUser(strings.TrimSpace(sb.String()))
}
