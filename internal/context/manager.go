package context

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"strings"
	"sync"
	"time"

	"github.com/topcheer/ggcode/internal/provider"
	toolpkg "github.com/topcheer/ggcode/internal/tool"
)

// ContextManager manages conversation history, tracking tokens and auto-summarizing.
//
// ⚠️ Consuming packages must import this as "ctxpkg" to avoid
// collision with the standard library "context" package.
type ContextManager interface {
	Add(msg provider.Message)
	Messages() []provider.Message
	TokenCount() int
	MaxTokens() int
	SetMaxTokens(n int)
	Summarize(ctx context.Context, prov provider.Provider) error
	CheckAndSummarize(ctx context.Context, prov provider.Provider) (bool, error)
	TruncateOldestGroupForRetry() bool
	Clear()
	UsageRatio() float64
}

const (
	summarizeThreshold  = 0.8
	compactTargetRatio  = 0.55
	summaryReserveRatio = 0.10
	minRecentGroups     = 1
	maxSummarizePasses  = 2
	minSummaryReserve   = 64
	microcompactMinGain = 32
	toolResultMinTokens = 96
	maxPTLRetries       = 3
	tokenCountTimeout   = 100 * time.Millisecond
)

func AutoCompactThresholdRatio() float64 {
	return summarizeThreshold
}

func AutoCompactThresholdTokens(maxTokens int) int {
	if maxTokens <= 0 {
		return 0
	}
	return int(float64(maxTokens) * summarizeThreshold)
}

// Manager implements ContextManager.
type Manager struct {
	mu        sync.Mutex
	messages  []provider.Message
	tokens    int
	maxTokens int
	provider  provider.Provider
	todoPath  string
}

// NewManager creates a ContextManager with the given context window limit.
func NewManager(maxTokens int) *Manager {
	return &Manager{maxTokens: maxTokens, todoPath: toolpkg.TodoFilePath("")}
}

func (m *Manager) SetTodoFilePath(path string) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if strings.TrimSpace(path) == "" {
		m.todoPath = toolpkg.TodoFilePath("")
		return
	}
	m.todoPath = path
}

// SetProvider sets the provider for provider-aware token counting.
func (m *Manager) SetProvider(p provider.Provider) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.provider = p
}

func (m *Manager) Add(msg provider.Message) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.messages = append(m.messages, msg)
	m.tokens += m.countTokens(msg)
}

func (m *Manager) Messages() []provider.Message {
	m.mu.Lock()
	defer m.mu.Unlock()
	out := make([]provider.Message, len(m.messages))
	copy(out, m.messages)
	return out
}

func (m *Manager) TokenCount() int {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.tokens
}

func (m *Manager) MaxTokens() int {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.maxTokens
}

func (m *Manager) SetMaxTokens(n int) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.maxTokens = n
}

func (m *Manager) Clear() {
	m.mu.Lock()
	defer m.mu.Unlock()
	if len(m.messages) > 0 && m.messages[0].Role == "system" {
		sys := m.messages[0]
		m.messages = []provider.Message{sys}
		m.tokens = m.countTokens(sys)
	} else {
		m.messages = nil
		m.tokens = 0
	}
}

func (m *Manager) UsageRatio() float64 {
	if m.maxTokens <= 0 {
		return 0
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	return float64(m.tokens) / float64(m.maxTokens)
}

// Summarize compresses old messages into a summary while retaining an adaptive
// recent suffix sized to fit within the target token budget.
func (m *Manager) Summarize(ctx context.Context, prov provider.Provider) error {
	for pass := 0; pass < maxSummarizePasses; pass++ {
		plan, ok := m.buildSummaryPlan()
		if !ok {
			return nil
		}

		summaryText, err := summarizeMessages(ctx, prov, plan.oldMsgs)
		if err != nil {
			return err
		}

		stateText := m.buildPostCompactState(plan.allMsgs)

		m.mu.Lock()

		// Collect any messages that arrived during summarization (TOCTOU fix)
		var extraMsgs []provider.Message
		if len(m.messages) > plan.origLen {
			extraMsgs = make([]provider.Message, len(m.messages)-plan.origLen)
			copy(extraMsgs, m.messages[plan.origLen:])
		}

		newMsgs := make([]provider.Message, 0, len(plan.recentMsgs)+len(extraMsgs)+2)
		if plan.hasSystem {
			newMsgs = append(newMsgs, plan.systemMsg)
		}
		newMsgs = append(newMsgs, provider.Message{
			Role: "system",
			Content: []provider.ContentBlock{{
				Type: "text",
				Text: fmt.Sprintf("[Previous conversation summary]\n%s", summaryText),
			}},
		})
		if stateText != "" {
			newMsgs = append(newMsgs, provider.Message{
				Role: "system",
				Content: []provider.ContentBlock{{
					Type: "text",
					Text: stateText,
				}},
			})
		}
		newMsgs = append(newMsgs, plan.recentMsgs...)
		newMsgs = append(newMsgs, extraMsgs...)

		m.messages = newMsgs
		m.recalcTokens()
		done := m.tokens <= m.compactTargetTokens()
		m.mu.Unlock()

		if done {
			return nil
		}
	}

	return nil
}

// CheckAndSummarize triggers summarization if usage ratio >= threshold.
func (m *Manager) CheckAndSummarize(ctx context.Context, prov provider.Provider) (bool, error) {
	if m.UsageRatio() < summarizeThreshold {
		return false, nil
	}

	changed := m.Microcompact()
	if m.UsageRatio() < summarizeThreshold {
		return changed, nil
	}

	err := m.Summarize(ctx, prov)
	return changed || err == nil, err
}

func (m *Manager) TruncateOldestGroupForRetry() bool {
	m.mu.Lock()
	defer m.mu.Unlock()
	truncated, ok := truncateGroupsForPTLRetry(m.messages)
	if !ok {
		return false
	}
	m.messages = truncated
	m.recalcTokens()
	return true
}

func (m *Manager) recalcTokens() {
	m.tokens = 0
	for _, msg := range m.messages {
		m.tokens += m.countTokens(msg)
	}
}

// countTokens uses the provider's token counting API when available,
// falling back to heuristic estimation.
func (m *Manager) countTokens(msg provider.Message) int {
	if m.provider != nil {
		ctx, cancel := context.WithTimeout(context.Background(), tokenCountTimeout)
		defer cancel()
		if n, err := m.provider.CountTokens(ctx, []provider.Message{msg}); err == nil && n > 0 {
			return n
		}
	}
	return estimateTokens(msg)
}

func estimateTokens(msg provider.Message) int {
	var text string
	for _, b := range msg.Content {
		text += b.Text + b.ToolName + b.Output + string(b.Input)
	}
	return EstimateTokens(text)
}

// Microcompact reduces old bulky tool results in-place before falling back to
// full summarization.
func (m *Manager) Microcompact() bool {
	m.mu.Lock()
	defer m.mu.Unlock()

	if m.tokens <= m.compactTargetTokens() {
		return false
	}

	start := 0
	if len(m.messages) > 0 && m.messages[0].Role == "system" {
		start = 1
	}

	groups := buildMessageGroups(m.messages, start)
	if len(groups) <= minRecentGroups {
		return false
	}
	protectedStart := groups[len(groups)-minRecentGroups].start

	changed := false
	currentTokens := m.tokens
	targetTokens := m.compactTargetTokens()

	for i := start; i < protectedStart && currentTokens > targetTokens; i++ {
		msg := m.messages[i]
		blocks := append([]provider.ContentBlock(nil), msg.Content...)
		msgChanged := false

		for j, block := range blocks {
			if block.Type != "tool_result" || block.Output == "" {
				continue
			}

			originalTokens := EstimateTokens(block.Output)
			if originalTokens < toolResultMinTokens {
				continue
			}

			placeholder := compactedToolResultPlaceholder(block, originalTokens)
			newTokens := EstimateTokens(placeholder)
			if originalTokens-newTokens < microcompactMinGain {
				continue
			}

			blocks[j].Output = placeholder
			currentTokens -= originalTokens - newTokens
			msgChanged = true
			changed = true

			if currentTokens <= targetTokens {
				break
			}
		}

		if msgChanged {
			msg.Content = blocks
			m.messages[i] = msg
		}
	}

	if changed {
		m.recalcTokens()
	}
	return changed
}

type summaryPlan struct {
	hasSystem  bool
	systemMsg  provider.Message
	allMsgs    []provider.Message
	oldMsgs    []provider.Message
	recentMsgs []provider.Message
	origLen    int
}

type messageGroup struct {
	start int
	end   int
}

func (m *Manager) buildSummaryPlan() (summaryPlan, bool) {
	m.mu.Lock()
	defer m.mu.Unlock()

	plan := summaryPlan{origLen: len(m.messages)}
	start := 0
	systemTokens := 0
	if len(m.messages) > 0 && m.messages[0].Role == "system" {
		plan.hasSystem = true
		plan.systemMsg = m.messages[0]
		start = 1
		systemTokens = m.countTokens(plan.systemMsg)
	}

	plan.allMsgs = append([]provider.Message(nil), m.messages...)
	groups := buildMessageGroups(m.messages, start)
	if len(groups) <= minRecentGroups {
		return summaryPlan{}, false
	}

	recentBudget := m.compactTargetTokens() - systemTokens - m.summaryReserveTokens()
	if recentBudget < 0 {
		recentBudget = 0
	}

	keepStart := len(m.messages)
	recentTokens := 0
	keptGroups := 0
	for i := len(groups) - 1; i >= 0; i-- {
		groupTokens := 0
		for j := groups[i].start; j < groups[i].end; j++ {
			groupTokens += m.countTokens(m.messages[j])
		}
		if keptGroups < minRecentGroups || recentTokens+groupTokens <= recentBudget {
			recentTokens += groupTokens
			keptGroups++
			keepStart = groups[i].start
			continue
		}
		break
	}

	if keptGroups >= len(groups) {
		keepStart = groups[1].start
	}
	if keepStart <= start {
		keepStart = groups[1].start
	}
	if keepStart >= len(m.messages) {
		return summaryPlan{}, false
	}

	plan.oldMsgs = append([]provider.Message(nil), m.messages[start:keepStart]...)
	plan.recentMsgs = append([]provider.Message(nil), m.messages[keepStart:]...)
	return plan, len(plan.oldMsgs) > 0
}

func summarizeMessages(ctx context.Context, prov provider.Provider, msgs []provider.Message) (string, error) {
	current := append([]provider.Message(nil), msgs...)
	for attempt := 0; attempt <= maxPTLRetries; attempt++ {
		payload := buildSummaryPayload(current)
		summaryMsgs := []provider.Message{
			{
				Role: "system",
				Content: []provider.ContentBlock{{
					Type: "text",
					Text: "Summarize the following conversation concisely, preserving key decisions and context. Output only the summary.",
				}},
			},
			{
				Role: "user",
				Content: []provider.ContentBlock{{
					Type: "text",
					Text: fmt.Sprintf("Summarize:\n\n%s", payload),
				}},
			},
		}

		resp, err := prov.Chat(ctx, summaryMsgs, nil)
		if err != nil {
			if !isPromptTooLongError(err) || attempt == maxPTLRetries {
				return "", fmt.Errorf("summarization call failed: %w", err)
			}
			truncated, ok := truncateGroupsForPTLRetry(current)
			if !ok {
				return "", fmt.Errorf("summarization call failed: %w", err)
			}
			current = truncated
			continue
		}

		for _, block := range resp.Message.Content {
			if block.Type == "text" && block.Text != "" {
				return block.Text, nil
			}
		}
		return "", fmt.Errorf("summarization returned empty text")
	}
	return "", fmt.Errorf("summarization returned empty text")
}

func (m *Manager) compactTargetTokens() int {
	if m.maxTokens <= 0 {
		return 0
	}
	target := int(float64(m.maxTokens) * compactTargetRatio)
	if target < minSummaryReserve {
		return minSummaryReserve
	}
	return target
}

func (m *Manager) summaryReserveTokens() int {
	if m.maxTokens <= 0 {
		return minSummaryReserve
	}
	reserve := int(float64(m.maxTokens) * summaryReserveRatio)
	if reserve < minSummaryReserve {
		return minSummaryReserve
	}
	return reserve
}

func compactedToolResultPlaceholder(block provider.ContentBlock, originalTokens int) string {
	status := "ok"
	if block.IsError {
		status = "error"
	}
	if block.ToolID != "" {
		return fmt.Sprintf("[tool result compacted: id=%s status=%s original_tokens=%d]", block.ToolID, status, originalTokens)
	}
	return fmt.Sprintf("[tool result compacted: status=%s original_tokens=%d]", status, originalTokens)
}

func buildSummaryPayload(msgs []provider.Message) string {
	var sb strings.Builder
	for _, msg := range msgs {
		sb.WriteString(fmt.Sprintf("[%s]\n", msg.Role))
		for _, block := range msg.Content {
			switch block.Type {
			case "text":
				sb.WriteString(block.Text)
				sb.WriteByte('\n')
			case "tool_use":
				sb.WriteString(fmt.Sprintf("Tool call: %s\n", block.ToolName))
			case "tool_result":
				sb.WriteString(fmt.Sprintf("Tool result: %s\n", block.Output))
			}
		}
		sb.WriteByte('\n')
	}
	return sb.String()
}

func truncateGroupsForPTLRetry(msgs []provider.Message) ([]provider.Message, bool) {
	if len(msgs) < 2 {
		return nil, false
	}
	groups := buildMessageGroups(msgs, 0)
	if len(groups) < 2 {
		return nil, false
	}
	return append([]provider.Message(nil), msgs[groups[1].start:]...), true
}

func isPromptTooLongError(err error) bool {
	if err == nil {
		return false
	}
	s := strings.ToLower(err.Error())
	keywords := []string{
		"prompt too long",
		"context length",
		"context window",
		"maximum context",
		"too many tokens",
		"input is too long",
		"exceeds the model's context",
		"maximum input tokens",
	}
	for _, keyword := range keywords {
		if strings.Contains(s, keyword) {
			return true
		}
	}
	return false
}

func (m *Manager) buildPostCompactState(msgs []provider.Message) string {
	var sections []string

	if files := collectRecentFilePaths(msgs, 5); len(files) > 0 {
		sections = append(sections, fmt.Sprintf("Recent files:\n- %s", strings.Join(files, "\n- ")))
	}
	if todoSummary := m.readTodoSummary(); todoSummary != "" {
		sections = append(sections, todoSummary)
	}

	if len(sections) == 0 {
		return ""
	}
	return "[Post-compact state]\n" + strings.Join(sections, "\n\n")
}

func collectRecentFilePaths(msgs []provider.Message, limit int) []string {
	if limit <= 0 {
		return nil
	}
	seen := make(map[string]struct{})
	paths := make([]string, 0, limit)
	for i := len(msgs) - 1; i >= 0 && len(paths) < limit; i-- {
		for _, block := range msgs[i].Content {
			if block.Type != "tool_use" || len(block.Input) == 0 {
				continue
			}
			var input map[string]any
			if err := json.Unmarshal(block.Input, &input); err != nil {
				continue
			}
			for _, key := range []string{"path", "file_path"} {
				raw, ok := input[key]
				if !ok {
					continue
				}
				path, ok := raw.(string)
				if !ok || path == "" {
					continue
				}
				if _, exists := seen[path]; exists {
					break
				}
				seen[path] = struct{}{}
				paths = append(paths, path)
				break
			}
		}
	}
	return paths
}

func (m *Manager) readTodoSummary() string {
	path := strings.TrimSpace(m.todoPath)
	if path == "" {
		path = toolpkg.TodoFilePath("")
	}
	data, err := os.ReadFile(path)
	if err != nil {
		return ""
	}
	var todos []struct {
		ID      string `json:"id"`
		Content string `json:"content"`
		Status  string `json:"status"`
	}
	if err := json.Unmarshal(data, &todos); err != nil || len(todos) == 0 {
		return ""
	}
	pending, inProgress, done := 0, 0, 0
	active := make([]string, 0, 3)
	for _, td := range todos {
		switch td.Status {
		case "pending":
			pending++
		case "in_progress":
			inProgress++
		case "done":
			done++
		}
		if td.Status != "done" && len(active) < 3 {
			active = append(active, fmt.Sprintf("- %s (%s): %s", td.ID, td.Status, td.Content))
		}
	}
	var sb strings.Builder
	fmt.Fprintf(&sb, "Todo state: %d total (%d pending, %d in_progress, %d done)", len(todos), pending, inProgress, done)
	if len(active) > 0 {
		sb.WriteString("\nActive todos:\n")
		sb.WriteString(strings.Join(active, "\n"))
	}
	return sb.String()
}

func buildMessageGroups(messages []provider.Message, start int) []messageGroup {
	if start >= len(messages) {
		return nil
	}

	var groups []messageGroup
	currentStart := start
	for i := start + 1; i < len(messages); i++ {
		if startsNewInteractionGroup(messages[i]) {
			groups = append(groups, messageGroup{start: currentStart, end: i})
			currentStart = i
		}
	}
	groups = append(groups, messageGroup{start: currentStart, end: len(messages)})
	return groups
}

func startsNewInteractionGroup(msg provider.Message) bool {
	if msg.Role != "user" {
		return false
	}
	for _, block := range msg.Content {
		if block.Type != "tool_result" {
			return true
		}
	}
	return false
}
