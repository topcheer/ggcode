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

	lines := make([]string, 0, 8)
	title := strings.TrimSpace(req.Title)
	switch m.currentLanguage() {
	case LangZhCN:
		if title != "" {
			lines = append(lines, "我需要你补充一些信息："+title)
		} else {
			lines = append(lines, "我需要你补充一些信息：")
		}
	default:
		if title != "" {
			lines = append(lines, "I need a bit more input: "+title)
		} else {
			lines = append(lines, "I need a bit more input:")
		}
	}

	multiQuestion := len(req.Questions) > 1
	for idx, question := range req.Questions {
		prompt := strings.TrimSpace(firstNonEmpty(question.Prompt, question.Title))
		if prompt == "" {
			continue
		}
		if multiQuestion {
			lines = append(lines, fmt.Sprintf("%d. %s", idx+1, prompt))
		} else {
			lines = append(lines, prompt)
		}
		for _, choice := range question.Choices {
			label := strings.TrimSpace(choice.Label)
			if label == "" {
				continue
			}
			lines = append(lines, "- "+label)
		}
	}

	switch m.currentLanguage() {
	case LangZhCN:
		lines = append(lines, "请直接回复你的选择或答案。")
	default:
		lines = append(lines, "Reply directly with your choice or answer.")
	}
	return strings.TrimSpace(strings.Join(lines, "\n"))
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
