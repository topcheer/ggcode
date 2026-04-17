package tui

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"slices"
	"strings"
	"sync"
	"time"

	"github.com/topcheer/ggcode/internal/debug"
	"github.com/topcheer/ggcode/internal/im"
	"github.com/topcheer/ggcode/internal/subagent"
	toolpkg "github.com/topcheer/ggcode/internal/tool"
)

type queuedIMEvent struct {
	mgr   *im.Manager
	event im.OutboundEvent
}

type imEmitterState struct {
	once sync.Once
	ch   chan queuedIMEvent
}

func newIMEmitterState() *imEmitterState {
	return &imEmitterState{ch: make(chan queuedIMEvent, 256)}
}

func (s *imEmitterState) enqueue(mgr *im.Manager, event im.OutboundEvent) {
	if s == nil || mgr == nil {
		return
	}
	s.once.Do(func() {
		go func() {
			for item := range s.ch {
				err := item.mgr.Emit(context.Background(), item.event)
				if err != nil && !errors.Is(err, im.ErrNoChannelBound) {
					debug.Log("tui", "emit im kind=%s failed: %v", item.event.Kind, err)
				}
			}
		}()
	})
	s.ch <- queuedIMEvent{mgr: mgr, event: event}
}

func (m *Model) emitIMEvent(event im.OutboundEvent) {
	if m.imManager == nil {
		return
	}
	if event.Kind == im.OutboundEventText {
		if strings.TrimSpace(event.Text) == "" {
			return
		}
	}
	if event.Kind == im.OutboundEventStatus {
		event.Status = strings.TrimSpace(event.Status)
		if event.Status == "" {
			return
		}
	}
	switch event.Kind {
	case im.OutboundEventText:
		debug.Log("tui", "emit im text len=%d preview=%q", len(event.Text), truncateStr(event.Text, 80))
	case im.OutboundEventStatus:
		debug.Log("tui", "emit im status=%q", truncateStr(event.Status, 80))
	default:
		debug.Log("tui", "emit im kind=%s", event.Kind)
	}
	if m.imEmitter == nil {
		m.imEmitter = newIMEmitterState()
	}
	m.imEmitter.enqueue(m.imManager, event)
	m.triggerIMTyping()
}

// imTypingKeeper tracks the last typing trigger time to implement keepalive.
// Typing indicators typically expire after a few seconds; this ensures periodic
// refresh during long-running tool executions.
type imTypingKeeper struct {
	lastTrigger time.Time
	interval    time.Duration
}

const imTypingKeepaliveInterval = 5 * time.Second

func (m *Model) triggerIMTyping() {
	if m.imManager == nil {
		return
	}
	now := time.Now()
	if m.imTypingLast == nil {
		m.imTypingLast = &imTypingKeeper{interval: imTypingKeepaliveInterval}
	}
	if now.Sub(m.imTypingLast.lastTrigger) < m.imTypingLast.interval {
		return
	}
	m.imTypingLast.lastTrigger = now
	go m.imManager.TriggerTyping(context.Background())
}

func (m *Model) emitIMText(text string) {
	if strings.TrimSpace(text) == "" {
		return
	}
	m.lastIMStatus = ""
	m.emitIMEvent(im.OutboundEvent{
		Kind: im.OutboundEventText,
		Text: text,
	})
}

func (m *Model) emitIMLocalUserText(text string) {
	if strings.TrimSpace(text) == "" {
		return
	}
	m.emitIMText("【用户】" + text + "\n")
}

func (m *Model) emitIMStatus(status string) {
	status = strings.TrimSpace(status)
	if status == "" {
		return
	}
	if status == m.lastIMStatus {
		return
	}
	m.lastIMStatus = status
	m.emitIMEvent(im.OutboundEvent{
		Kind:   im.OutboundEventStatus,
		Status: status,
	})
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

	switch {
	case toolSummary != "" && (activity == "" || activity == thinking || activity == writing):
		return localizeIMProgress(m.currentLanguage(), toolSummary)
	case activity != "":
		return localizeIMProgress(m.currentLanguage(), activity)
	case toolSummary != "":
		return localizeIMProgress(m.currentLanguage(), toolSummary)
	default:
		return ""
	}
}

func localizeIMProgress(lang Language, text string) string {
	text = strings.TrimSpace(text)
	if text == "" {
		return ""
	}
	switch lang {
	case LangZhCN:
		switch text {
		case "思考中...", "思考中…":
			return "我先想一下..."
		case "输出中...", "输出中…":
			return "我整理一下结果..."
		}
		base := strings.TrimSpace(strings.TrimSuffix(strings.TrimSuffix(text, "..."), "…"))
		if base == "" {
			return ""
		}
		if strings.HasPrefix(base, "我") || strings.HasPrefix(base, "正在") {
			return text
		}
		return "正在" + base + "..."
	default:
		switch text {
		case "Thinking...", "Thinking…":
			return "Let me think..."
		case "Writing...", "Writing…":
			return "I'm drafting the answer..."
		}
		base := strings.TrimSpace(strings.TrimSuffix(strings.TrimSuffix(text, "..."), "…"))
		if base == "" {
			return ""
		}
		if strings.HasPrefix(base, "I'm ") || strings.HasPrefix(base, "I am ") || strings.HasPrefix(base, "Let me ") {
			return text
		}
		return "Working on " + base + "..."
	}
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
	_, _, _ = toolCalls, toolSuccesses, toolFailures
	m.emitIMText(text)
}

func (m *Model) emitIMAskUser(text string) {
	text = strings.TrimSpace(text)
	if text == "" {
		return
	}
	m.emitIMText(text)
}

func (m *Model) formatIMAskUserPrompt(rawArgs string) string {
	rawArgs = strings.TrimSpace(rawArgs)
	if rawArgs == "" {
		return ""
	}
	var req toolpkg.AskUserRequest
	if err := json.Unmarshal([]byte(rawArgs), &req); err != nil {
		target := strings.TrimSpace(askUserToolTarget(parseToolArgs(rawArgs)))
		if target == "" {
			return ""
		}
		switch m.currentLanguage() {
		case LangZhCN:
			return "我需要你补充信息：\n" + target
		default:
			return "I need a bit more input:\n" + target
		}
	}

	lang := m.currentLanguage()
	multiQuestion := len(req.Questions) > 1
	lines := make([]string, 0, 16+len(req.Questions)*4)

	// Title
	title := strings.TrimSpace(req.Title)
	switch lang {
	case LangZhCN:
		if title != "" {
			lines = append(lines, "📋 **"+title+"**")
		} else {
			lines = append(lines, "📋 **需要补充信息**")
		}
	default:
		if title != "" {
			lines = append(lines, "📋 **"+title+"**")
		} else {
			lines = append(lines, "📋 **Input needed**")
		}
	}

	// Questions
	for idx, question := range req.Questions {
		qLines := formatIMQuestionBlock(lang, idx, question, multiQuestion)
		lines = append(lines, qLines...)
	}

	// Reply instructions
	lines = append(lines, "")
	lines = append(lines, formatIMReplyInstructions(lang, req.Questions, multiQuestion))

	return strings.TrimSpace(strings.Join(lines, "\n"))
}

// formatIMQuestionBlock formats a single question with its choices and
// question-type-specific guidance.
func formatIMQuestionBlock(lang Language, idx int, q toolpkg.AskUserQuestion, multiQuestion bool) []string {
	prompt := strings.TrimSpace(firstNonEmpty(q.Prompt, q.Title))
	if prompt == "" {
		return nil
	}

	var lines []string

	// Question header
	if multiQuestion {
		lines = append(lines, fmt.Sprintf("**%d. %s**", idx+1, prompt))
	} else {
		lines = append(lines, fmt.Sprintf("**%s**", prompt))
	}

	// Choices with numbered labels
	for ci, choice := range q.Choices {
		label := strings.TrimSpace(choice.Label)
		if label == "" {
			continue
		}
		if multiQuestion {
			// Multi-question: prefix with question number + choice number
			// e.g. "1a. QQ  1b. 飞书" so user can reply "1a"
			lines = append(lines, fmt.Sprintf("  %d%c. %s", idx+1, 'a'+ci, label))
		} else {
			lines = append(lines, fmt.Sprintf("  %d. %s", ci+1, label))
		}
	}

	// Question-type hint
	switch q.Kind {
	case toolpkg.AskUserKindText:
		placeholder := strings.TrimSpace(q.Placeholder)
		if placeholder != "" {
			switch lang {
			case LangZhCN:
				lines = append(lines, fmt.Sprintf("  _（输入文本，例如：%s）_", placeholder))
			default:
				lines = append(lines, fmt.Sprintf("  _(type text, e.g. %s)_", placeholder))
			}
		} else {
			switch lang {
			case LangZhCN:
				lines = append(lines, "  _（输入文本）_")
			default:
				lines = append(lines, "  _(type text)_")
			}
		}
	case toolpkg.AskUserKindSingle:
		hint := ""
		switch lang {
		case LangZhCN:
			if len(q.Choices) > 0 {
				hint = fmt.Sprintf("  _（回复编号 %d-%d 或选项文本", 1, len(q.Choices))
				if q.AllowFreeform {
					hint += "，也可以直接输入其他内容"
				}
				hint += "）_"
			}
		default:
			if len(q.Choices) > 0 {
				hint = fmt.Sprintf("  _(reply %d-%d or option text", 1, len(q.Choices))
				if q.AllowFreeform {
					hint += ", or type freely"
				}
				hint += ")_"
			}
		}
		if hint != "" {
			lines = append(lines, hint)
		}
	case toolpkg.AskUserKindMulti:
		hint := ""
		switch lang {
		case LangZhCN:
			if len(q.Choices) > 0 {
				hint = fmt.Sprintf("  _（可多选，回复编号如 \"%d,%d\" 或选项文本", 1, min(3, len(q.Choices)))
				if q.AllowFreeform {
					hint += "，也可以输入其他内容"
				}
				hint += "）_"
			}
		default:
			if len(q.Choices) > 0 {
				hint = fmt.Sprintf("  _(select multiple, e.g. \"%d,%d\" or option text", 1, min(3, len(q.Choices)))
				if q.AllowFreeform {
					hint += ", or type freely"
				}
				hint += ")_"
			}
		}
		if hint != "" {
			lines = append(lines, hint)
		}
	}

	return lines
}

// formatIMReplyInstructions generates the overall reply instructions based on
// the number and types of questions.
func formatIMReplyInstructions(lang Language, questions []toolpkg.AskUserQuestion, multiQuestion bool) string {
	if len(questions) == 0 {
		return ""
	}

	if !multiQuestion {
		// Single question: simple instruction
		q := questions[0]
		switch lang {
		case LangZhCN:
			switch q.Kind {
			case toolpkg.AskUserKindText:
				return "💬 直接回复文本即可。"
			case toolpkg.AskUserKindSingle:
				return "💬 回复编号或选项文本。"
			case toolpkg.AskUserKindMulti:
				return "💬 回复多个编号（用逗号或空格分隔）或选项文本。"
			}
		default:
			switch q.Kind {
			case toolpkg.AskUserKindText:
				return "💬 Just reply with your text."
			case toolpkg.AskUserKindSingle:
				return "💬 Reply with the number or option text."
			case toolpkg.AskUserKindMulti:
				return "💬 Reply with multiple numbers (comma or space separated) or option text."
			}
		}
		return ""
	}

	// Multi-question: provide structured reply guidance
	switch lang {
	case LangZhCN:
		lines := []string{"💬 **回复格式：**"}
		if len(questions) == 2 {
			lines = append(lines, "每行回答一个问题，或用空行分隔。例如：")
		} else {
			lines = append(lines, "按顺序逐行回答，每行对应一个问题。例如：")
		}
		examples := buildIMReplyExamples(lang, questions)
		for _, ex := range examples {
			lines = append(lines, "> "+ex)
		}
		return strings.Join(lines, "\n")
	default:
		lines := []string{"💬 **Reply format:**"}
		if len(questions) == 2 {
			lines = append(lines, "Answer one question per line, or separate with blank lines. Example:")
		} else {
			lines = append(lines, "Answer in order, one per line. Example:")
		}
		examples := buildIMReplyExamples(lang, questions)
		for _, ex := range examples {
			lines = append(lines, "> "+ex)
		}
		return strings.Join(lines, "\n")
	}
}

// buildIMReplyExamples generates concrete reply examples based on the actual
// question types, so the user sees exactly what format to use.
func buildIMReplyExamples(lang Language, questions []toolpkg.AskUserQuestion) []string {
	examples := make([]string, len(questions))
	for i, q := range questions {
		switch q.Kind {
		case toolpkg.AskUserKindSingle:
			if len(q.Choices) > 0 {
				examples[i] = fmt.Sprintf("%d", 1) // "1" — pick first choice
			}
		case toolpkg.AskUserKindMulti:
			if len(q.Choices) >= 2 {
				examples[i] = fmt.Sprintf("%d,%d", 1, 2) // "1,2"
			} else if len(q.Choices) == 1 {
				examples[i] = "1"
			}
		case toolpkg.AskUserKindText:
			switch lang {
			case LangZhCN:
				switch i {
				case 0:
					examples[i] = "我的答案"
				case 1:
					examples[i] = "另一个回答"
				default:
					examples[i] = fmt.Sprintf("第%d个回答", i+1)
				}
			default:
				switch i {
				case 0:
					examples[i] = "my answer"
				case 1:
					examples[i] = "another answer"
				default:
					examples[i] = fmt.Sprintf("answer %d", i+1)
				}
			}
		}
	}
	return examples
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
