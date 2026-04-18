package context

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"reflect"
	"strings"
	"sync"
	"time"

	"github.com/topcheer/ggcode/internal/debug"
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
	SetOutputReserve(n int)
	RecordUsage(usage provider.TokenUsage)
	Summarize(ctx context.Context, prov provider.Provider) error
	CheckAndSummarize(ctx context.Context, prov provider.Provider) (bool, error)
	TruncateOldestGroupForRetry() bool
	Clear()
	UsageRatio() float64
	AutoCompactThreshold() int
}

const (
	summarizeThresholdWithUsage = 0.85
	summarizeThresholdFallback  = 0.72
	compactTargetRatio          = 0.55
	summaryReserveRatio         = 0.10
	defaultOutputReserveRatio   = 0.10
	maxOutputReserveRatio       = 0.25
	safetyMarginRatio           = 0.05
	minRecentGroups             = 1
	maxSummarizePasses          = 2
	minSummaryReserve           = 64
	microcompactMinGain         = 32
	toolResultMinTokens         = 96
	maxPTLRetries               = 3
	tokenCountTimeout           = 100 * time.Millisecond
)

func AutoCompactThresholdRatio() float64 {
	return summarizeThresholdWithUsage
}

func AutoCompactThresholdTokens(maxTokens int) int {
	if maxTokens <= 0 {
		return 0
	}
	return int(float64(maxTokens) * summarizeThresholdFallback)
}

// Manager implements ContextManager.
type Manager struct {
	mu                sync.Mutex
	messages          []provider.Message
	tokens            int
	maxTokens         int
	outputReserve     int
	baselineTokens    int
	baselineDelta     int
	baselineAvailable bool
	provider          provider.Provider
	todoPath          string
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
	msgTokens := m.countTokens(msg)
	m.messages = append(m.messages, msg)
	m.tokens += msgTokens
	if m.baselineAvailable {
		m.baselineDelta += msgTokens
	}
	ratio := 0.0
	if m.maxTokens > 0 {
		ratio = float64(m.tokenCountLocked()) / float64(m.maxTokens)
	}
	debug.Log("ctx", "Add: role=%s blocks=%d msg_tokens=%d total=%d max=%d ratio=%.3f baseline=%t",
		msg.Role, len(msg.Content), msgTokens, m.tokenCountLocked(), m.maxTokens, ratio, m.baselineAvailable)
}

// UpdateFirstSystemMessage replaces the first system message in the context.
// If no system message exists, it prepends one.
func (m *Manager) UpdateFirstSystemMessage(msg provider.Message) {
	m.mu.Lock()
	defer m.mu.Unlock()
	for i, existing := range m.messages {
		if existing.Role == "system" {
			oldTokens := m.countTokens(existing)
			newTokens := m.countTokens(msg)
			m.messages[i] = msg
			m.tokens += newTokens - oldTokens
			return
		}
	}
	// No system message found, prepend
	newTokens := m.countTokens(msg)
	m.messages = append([]provider.Message{msg}, m.messages...)
	m.tokens += newTokens
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
	return m.tokenCountLocked()
}

func (m *Manager) MaxTokens() int {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.maxTokens
}

func (m *Manager) SetMaxTokens(n int) {
	m.mu.Lock()
	defer m.mu.Unlock()
	debug.Log("ctx", "SetMaxTokens: %d→%d", m.maxTokens, n)
	m.maxTokens = n
}

func (m *Manager) SetOutputReserve(n int) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if n < 0 {
		n = 0
	}
	debug.Log("ctx", "SetOutputReserve: raw=%d effective=%d (ceiling=%d)", n, m.effectiveOutputReserveLocked(), int(float64(m.maxTokens)*maxOutputReserveRatio))
	m.outputReserve = n
}

func (m *Manager) RecordUsage(usage provider.TokenUsage) {
	if usage.InputTokens <= 0 {
		return
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	oldBaseline := m.baselineTokens
	m.baselineTokens = usage.InputTokens
	m.baselineDelta = 0
	m.baselineAvailable = true
	debug.Log("ctx", "RecordUsage: input=%d output=%d old_baseline=%d→new_baseline=%d estimated=%d delta=%d",
		usage.InputTokens, usage.OutputTokens, oldBaseline, usage.InputTokens, m.tokens, usage.InputTokens-m.tokens)
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
	m.invalidateUsageBaselineLocked()
}

func (m *Manager) UsageRatio() float64 {
	if m.maxTokens <= 0 {
		return 0
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	return float64(m.tokenCountLocked()) / float64(m.maxTokens)
}

func (m *Manager) AutoCompactThreshold() int {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.autoCompactThresholdLocked()
}

// Summarize compresses old messages into a summary while retaining an adaptive
// recent suffix sized to fit within the target token budget.
func (m *Manager) Summarize(ctx context.Context, prov provider.Provider) error {
	for pass := 0; pass < maxSummarizePasses; pass++ {
		plan, ok := m.buildSummaryPlan()
		if !ok {
			debug.Log("ctx", "Summarize: no plan built, nothing to summarize")
			return nil
		}

		debug.Log("ctx", "Summarize: pass=%d old_msgs=%d recent_msgs=%d has_system=%t",
			pass, len(plan.oldMsgs), len(plan.recentMsgs), plan.hasSystem)

		summaryText, err := summarizeMessages(ctx, prov, plan.oldMsgs)
		if err != nil {
			debug.Log("ctx", "Summarize: summarizeMessages FAILED: %v", err)
			return err
		}
		debug.Log("ctx", "Summarize: summary generated, len=%d chars", len(summaryText))

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

		oldLen := len(m.messages)
		m.messages = newMsgs
		m.recalcTokens()
		done := m.tokens <= m.compactTargetTokens()
		debug.Log("ctx", "Summarize: pass=%d msgs=%d→%d tokens=%d target=%d done=%t",
			pass, oldLen, len(newMsgs), m.tokens, m.compactTargetTokens(), done)
		m.mu.Unlock()

		if done {
			return nil
		}
	}

	debug.Log("ctx", "Summarize: exhausted %d passes", maxSummarizePasses)
	return nil
}

// CheckAndSummarize triggers summarization if usage ratio >= threshold.
func (m *Manager) CheckAndSummarize(ctx context.Context, prov provider.Provider) (bool, error) {
	tokenCount := m.TokenCount()
	threshold := m.AutoCompactThreshold()
	budget := func() int {
		m.mu.Lock()
		defer m.mu.Unlock()
		return m.usablePromptBudgetLocked()
	}()
	ratio := m.UsageRatio()
	debug.Log("ctx", "CheckAndSummarize: tokens=%d threshold=%d budget=%d ratio=%.3f maxTokens=%d — %s",
		tokenCount, threshold, budget, ratio, m.MaxTokens(),
		func() string {
			if tokenCount < threshold {
				return "SKIP (below threshold)"
			}
			return "TRIGGERED"
		}())

	if tokenCount < threshold {
		return false, nil
	}

	changed := m.Microcompact()
	if m.TokenCount() < m.AutoCompactThreshold() {
		debug.Log("ctx", "CheckAndSummarize: Microcompact resolved, tokens now=%d", m.TokenCount())
		return changed, nil
	}

	debug.Log("ctx", "CheckAndSummarize: Microcompact not enough, calling Summarize (tokens=%d)", m.TokenCount())
	before := m.Messages()
	err := m.Summarize(ctx, prov)
	if err != nil {
		debug.Log("ctx", "CheckAndSummarize: Summarize FAILED: %v", err)
		return changed, err
	}
	after := m.Messages()
	summaryChanged := !reflect.DeepEqual(before, after)
	debug.Log("ctx", "CheckAndSummarize: done tokens=%d msgs=%d→%d changed=%t",
		m.TokenCount(), len(before), len(after), summaryChanged)
	return changed || summaryChanged, nil
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
	m.invalidateUsageBaselineLocked()
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
	n := estimateTokens(msg)
	return n
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

	if m.tokenCountLocked() <= m.compactTargetTokens() {
		debug.Log("ctx", "Microcompact: SKIP tokens=%d <= target=%d", m.tokenCountLocked(), m.compactTargetTokens())
		return false
	}

	start := 0
	if len(m.messages) > 0 && m.messages[0].Role == "system" {
		start = 1
	}

	groups := buildMessageGroups(m.messages, start)
	if len(groups) <= minRecentGroups {
		debug.Log("ctx", "Microcompact: SKIP groups=%d <= min=%d", len(groups), minRecentGroups)
		return false
	}
	protectedStart := groups[len(groups)-minRecentGroups].start

	changed := false
	currentTokens := m.tokenCountLocked()
	targetTokens := m.compactTargetTokens()
	compactedCount := 0

	debug.Log("ctx", "Microcompact: START tokens=%d target=%d msgs=%d groups=%d protected_from=%d",
		currentTokens, targetTokens, len(m.messages), len(groups), protectedStart)

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
			compactedCount++

			if currentTokens <= targetTokens {
				break
			}
		}

		if msgChanged {
			msg.Content = blocks
			m.messages[i] = msg
		}
	}

	// Extension: also compact large tool_results in recent groups if still over budget.
	// This prevents infinite compression loops when recent rounds contain huge tool output.
	if currentTokens > targetTokens {
		for i := protectedStart; i < len(m.messages) && currentTokens > targetTokens; i++ {
			msg := m.messages[i]
			blocks := append([]provider.ContentBlock(nil), msg.Content...)
			msgChanged := false

			for j, block := range blocks {
				if block.Type != "tool_result" || block.Output == "" {
					continue
				}
				// Use a higher threshold for recent groups to avoid over-truncation.
				originalTokens := EstimateTokens(block.Output)
				if originalTokens < toolResultMinTokens*3 {
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
				compactedCount++

				if currentTokens <= targetTokens {
					break
				}
			}

			if msgChanged {
				msg.Content = blocks
				m.messages[i] = msg
			}
		}
	}

	if changed {
		m.recalcTokens()
		debug.Log("ctx", "Microcompact: DONE compacted=%d blocks tokens=%d→%d", compactedCount, currentTokens, m.tokenCountLocked())
	} else {
		debug.Log("ctx", "Microcompact: no eligible tool results found to compact")
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

	// If recent side still far exceeds budget, force-compress oldest recent group.
	// This handles the case where a single huge round causes infinite compression loops.
	if recentTokens > recentBudget*2 && keptGroups > 1 {
		// Move the oldest kept group to oldMsgs by advancing keepStart past it
		firstKeptIdx := len(groups) - keptGroups
		keepStart = groups[firstKeptIdx+1].start
		keptGroups--
		debug.Log("ctx", "buildSummaryPlan: forcing single-round compression, keptGroups=%d->%d recentTokens=%d budget=%d",
			keptGroups+1, keptGroups, recentTokens, recentBudget)
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
	budget := m.usablePromptBudgetLocked()
	if budget <= 0 {
		return 0
	}
	target := int(float64(budget) * compactTargetRatio)
	if target < minSummaryReserve {
		return minSummaryReserve
	}
	return target
}

func (m *Manager) summaryReserveTokens() int {
	budget := m.usablePromptBudgetLocked()
	if budget <= 0 {
		return minSummaryReserve
	}
	reserve := int(float64(budget) * summaryReserveRatio)
	if reserve < minSummaryReserve {
		return minSummaryReserve
	}
	return reserve
}

func (m *Manager) tokenCountLocked() int {
	if m.baselineAvailable {
		total := m.baselineTokens + m.baselineDelta
		if total > 0 {
			return total
		}
	}
	return m.tokens
}

func (m *Manager) autoCompactThresholdLocked() int {
	budget := m.usablePromptBudgetLocked()
	if budget <= 0 {
		return 0
	}
	ratio := summarizeThresholdFallback
	if m.baselineAvailable {
		ratio = summarizeThresholdWithUsage
	}
	return int(float64(budget) * ratio)
}

func (m *Manager) usablePromptBudgetLocked() int {
	if m.maxTokens <= 0 {
		return 0
	}
	reserve := m.effectiveOutputReserveLocked()
	safety := m.effectiveSafetyMarginLocked()
	budget := m.maxTokens - reserve - safety
	if budget < minSummaryReserve {
		return minSummaryReserve
	}
	debug.Log("ctx", "usableBudget: max=%d - reserve=%d - safety=%d = %d", m.maxTokens, reserve, safety, budget)
	return budget
}

func (m *Manager) effectiveOutputReserveLocked() int {
	if m.maxTokens <= 0 {
		return 0
	}
	floor := minInt(8192, maxInt(512, m.maxTokens/10))
	ceiling := maxInt(floor, int(float64(m.maxTokens)*maxOutputReserveRatio))
	reserve := m.outputReserve
	if reserve <= 0 {
		reserve = int(float64(m.maxTokens) * defaultOutputReserveRatio)
	}
	if reserve < floor {
		reserve = floor
	}
	if reserve > ceiling {
		reserve = ceiling
	}
	return reserve
}

func (m *Manager) effectiveSafetyMarginLocked() int {
	if m.maxTokens <= 0 {
		return minSummaryReserve
	}
	safety := int(float64(m.maxTokens) * safetyMarginRatio)
	safetyFloor := minInt(4096, maxInt(minSummaryReserve, m.maxTokens/20))
	if safety < safetyFloor {
		safety = safetyFloor
	}
	return safety
}

func (m *Manager) invalidateUsageBaselineLocked() {
	m.baselineTokens = 0
	m.baselineDelta = 0
	m.baselineAvailable = false
}

func minInt(a, b int) int {
	if a < b {
		return a
	}
	return b
}

func maxInt(a, b int) int {
	if a > b {
		return a
	}
	return b
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
			if block.Type == "text" && block.Text != "" {
				for _, path := range extractPostCompactStateFilePaths(block.Text) {
					if len(paths) >= limit {
						break
					}
					if _, exists := seen[path]; exists {
						continue
					}
					seen[path] = struct{}{}
					paths = append(paths, path)
				}
			}
			if block.Type != "tool_use" || len(block.Input) == 0 || len(paths) >= limit {
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

func extractPostCompactStateFilePaths(text string) []string {
	if !strings.Contains(text, "[Post-compact state]") || !strings.Contains(text, "Recent files:") {
		return nil
	}
	lines := strings.Split(text, "\n")
	paths := make([]string, 0, 4)
	inFiles := false
	for _, line := range lines {
		trimmed := strings.TrimSpace(line)
		switch {
		case trimmed == "Recent files:":
			inFiles = true
		case !inFiles:
			continue
		case strings.HasPrefix(trimmed, "- "):
			path := strings.TrimSpace(strings.TrimPrefix(trimmed, "- "))
			if path != "" {
				paths = append(paths, path)
			}
		case trimmed == "":
			if inFiles {
				return paths
			}
		default:
			if inFiles {
				return paths
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
