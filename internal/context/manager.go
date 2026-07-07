package context

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"strings"
	"sync"
	"time"

	"github.com/topcheer/ggcode/internal/debug"
	"github.com/topcheer/ggcode/internal/provider"
)

// ContextManager manages conversation history, tracking tokens and auto-summarizing.
//
// ⚠️ Consuming packages must import this as "ctxpkg" to avoid
// collision with the standard library "context" package.
type ContextManager interface {
	Add(msg provider.Message)
	Messages() []provider.Message
	TokenCount() int
	// MessagesAndTokenCount returns both values under a single lock,
	// guaranteeing a consistent snapshot.
	MessagesAndTokenCount() ([]provider.Message, int)
	ContextWindow() int
	SetContextWindow(n int)
	SetOutputReserve(n int)
	RecordUsage(usage provider.TokenUsage)
	Summarize(ctx context.Context, prov provider.Provider) error
	CheckAndSummarize(ctx context.Context, prov provider.Provider) (bool, error)
	TruncateOldestGroupForRetry() bool
	RemoveLastAssistantGroup() string
	Clear()
	UsageRatio() float64
	AutoCompactThreshold() int
	ReconcileToolCalls() bool
}

// CompactSnapshot is an immutable point-in-time view used by background
// compaction. It lets callers summarize a stable copy without mutating the live
// conversation while an LLM turn may still be running.
type CompactSnapshot struct {
	Messages      []provider.Message
	OrigLen       int
	ContextWindow int
	OutputReserve int
	TodoPath      string
	Version       int64
}

// CompactResult is the output of compacting a CompactSnapshot.
type CompactResult struct {
	Messages   []provider.Message
	TokenCount int
	Changed    bool
}

const (
	// Compaction trigger: 99% of usable budget. Leaves a small margin so the
	// last LLM turn before compaction can still fit.
	summarizeThreshold = 0.99

	// Summary output cap: 5% of contextWindow, but capped at a fixed
	// absolute maximum. For 200K context → 10K; for 1M context → 12K (not 50K).
	maxSummaryOutputRatio  = 0.05
	maxSummaryOutputTokens = 12000

	// Post-compaction target: fixed absolute size, not proportional.
	// After compaction the context should be roughly system + summary ≈ 20K.
	// For small context windows, cap at 25% of the window.
	compactTargetFixed = 30000

	defaultOutputReserveRatio = 0.10
	maxOutputReserveRatio     = 0.25
	safetyMarginRatio         = 0.05
	minRecentGroups           = 0 // summarize all groups, keep none verbatim
	minSummaryReserve         = 64
	maxPTLRetries             = 3
	tokenCountTimeout         = 100 * time.Millisecond
)

func AutoCompactThresholdRatio() float64 {
	return summarizeThreshold
}

func AutoCompactThresholdTokens(contextWindow int) int {
	if contextWindow <= 0 {
		return 0
	}
	return int(float64(contextWindow) * summarizeThreshold)
}

// Manager implements ContextManager.
type Manager struct {
	mu                sync.Mutex
	messages          []provider.Message
	version           int64              // incremented on every mutation, enables cheap change detection
	runAdded          []provider.Message // messages added via Add() since last StartRunTracking()
	tokens            int
	contextWindow     int
	outputReserve     int
	baselineTokens    int
	baselineDelta     int
	baselineAvailable bool
	provider          provider.Provider
	todoPath          string
	onUsage           func(provider.TokenUsage)
}

// NewManager creates a ContextManager with the given context window limit.
func NewManager(contextWindow int) *Manager {
	return &Manager{contextWindow: contextWindow}
}

func (m *Manager) SetTodoFilePath(path string) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.todoPath = path
}

func (m *Manager) SetUsageHandler(fn func(provider.TokenUsage)) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.onUsage = fn
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
	m.runAdded = append(m.runAdded, msg)
	m.version++
	m.tokens += msgTokens
	if m.baselineAvailable {
		m.baselineDelta += msgTokens
	}
	ratio := 0.0
	if m.contextWindow > 0 {
		ratio = float64(m.tokenCountLocked()) / float64(m.contextWindow)
	}
	debug.Log("ctx", "Add: role=%s blocks=%d msg_tokens=%d total=%d max=%d ratio=%.3f baseline=%t",
		msg.Role, len(msg.Content), msgTokens, m.tokenCountLocked(), m.contextWindow, ratio, m.baselineAvailable)
}

// ReconcileToolCalls checks whether any assistant message in the conversation
// has unpaired tool_use blocks (i.e. tool_calls without matching tool_result
// blocks in subsequent messages). If so, it inserts user messages containing
// the actual tool results at the correct position (before the next assistant),
// preserving real execution output rather than dropping it.
//
// This handles two scenarios:
//  1. Session restoration from file: the process crashed while a tool was
//     still pending, so the session file contains an assistant message with
//     tool_use but no tool_result (or tool_results placed after another
//     assistant message).
//  2. Runtime interruption: the user interrupted (e.g. Ctrl+C) while the
//     agent was about to execute tools, and the next user message starts
//     a new agent run without the cancelled tool_results having been added.
//
// Returns true if any messages were inserted or moved.
// Returns true if any cancelled tool_result entries were added.
func (m *Manager) ReconcileToolCalls() bool {
	m.mu.Lock()
	defer m.mu.Unlock()

	// ── Phase 0: clean orphan tool_results ──
	// Tool_results whose tool_id has no matching tool_use in the preceding
	// assistant message (or any open tool_call) are orphaned. Remove them.
	m.removeOrphanToolResults()

	// ── Phase 1: collect information ──
	type lateResult struct {
		msgIdx int
		block  provider.ContentBlock
	}
	type needFix struct {
		insertBefore int
		lateBlocks   []lateResult
		missingIDs   []struct{ id, name string }
	}
	var fixes []needFix

	for idx := range m.messages {
		if m.messages[idx].Role != "assistant" {
			continue
		}
		toolIDs := make(map[string]string)
		for _, block := range m.messages[idx].Content {
			if block.Type == "tool_use" {
				toolIDs[block.ToolID] = block.ToolName
			}
		}
		if len(toolIDs) == 0 {
			continue
		}

		// Find the next assistant boundary.
		nextAssistantIdx := len(m.messages)
		for j := idx + 1; j < len(m.messages); j++ {
			if m.messages[j].Role == "assistant" {
				nextAssistantIdx = j
				break
			}
		}

		// Collect tool_results BEFORE the next assistant -> properly placed.
		for j := idx + 1; j < nextAssistantIdx; j++ {
			for _, block := range m.messages[j].Content {
				if block.Type == "tool_result" {
					delete(toolIDs, block.ToolID)
				}
			}
		}

		if len(toolIDs) == 0 {
			continue
		}

		// Check for LATE results (after next assistant).
		var late []lateResult
		var missingIDs []struct{ id, name string }
		for id, name := range toolIDs {
			foundLate := false
			for j := nextAssistantIdx; j < len(m.messages); j++ {
				for _, block := range m.messages[j].Content {
					if block.Type == "tool_result" && block.ToolID == id {
						late = append(late, lateResult{msgIdx: j, block: block})
						foundLate = true
						break
					}
				}
				if foundLate {
					break
				}
			}
			if !foundLate {
				missingIDs = append(missingIDs, struct{ id, name string }{id, name})
			}
		}

		if len(late) == 0 && len(missingIDs) == 0 {
			continue
		}

		fixes = append(fixes, needFix{
			insertBefore: nextAssistantIdx,
			lateBlocks:   late,
			missingIDs:   missingIDs,
		})
	}

	if len(fixes) == 0 {
		return false
	}

	// ── Phase 2: apply fixes ──
	oldMsgs := m.messages

	staleMsgIdxs := make(map[int]bool)
	var insertions []struct {
		insertBefore int
		msg          provider.Message
	}
	for _, fix := range fixes {
		for _, lr := range fix.lateBlocks {
			staleMsgIdxs[lr.msgIdx] = true
		}
		var content []provider.ContentBlock
		seen := make(map[string]bool)
		for _, lr := range fix.lateBlocks {
			if !seen[lr.block.ToolID] {
				content = append(content, lr.block)
				seen[lr.block.ToolID] = true
			}
		}
		for _, m := range fix.missingIDs {
			if !seen[m.id] {
				name := m.name
				if name == "" {
					name = "unknown"
				}
				content = append(content, provider.ToolResultNamedBlock(
					m.id, name,
					"operation cancelled - tool call was interrupted before it could complete",
					true,
				))
				seen[m.id] = true
			}
		}
		if len(content) > 0 {
			insertions = append(insertions, struct {
				insertBefore int
				msg          provider.Message
			}{insertBefore: fix.insertBefore, msg: provider.Message{Role: "user", Content: content}})
		}
	}

	newMsgs := make([]provider.Message, 0, len(oldMsgs)+len(insertions))
	for i, m := range oldMsgs {
		for _, ins := range insertions {
			if ins.insertBefore == i {
				newMsgs = append(newMsgs, ins.msg)
			}
		}
		if staleMsgIdxs[i] {
			continue
		}
		newMsgs = append(newMsgs, m)
	}
	for _, ins := range insertions {
		if ins.insertBefore == len(oldMsgs) {
			newMsgs = append(newMsgs, ins.msg)
		}
	}

	m.messages = newMsgs
	m.version++
	m.tokens = 0
	for _, msg := range m.messages {
		m.tokens += m.countTokens(msg)
	}
	if m.baselineAvailable {
		m.baselineDelta = 0
	}

	lateCount := 0
	cancelledCount := 0
	for _, fix := range fixes {
		lateCount += len(fix.lateBlocks)
		cancelledCount += len(fix.missingIDs)
	}
	debug.Log("ctx", "ReconcileToolCalls: relocated %d late tool_result(s), added %d cancelled, removed %d stale messages",
		lateCount, cancelledCount, len(staleMsgIdxs))
	return true
}

// removeOrphanToolResults removes tool_result blocks whose tool_id has no
// matching tool_use in any preceding assistant message. These are orphaned
// tool_results that trigger 'tool message without preceding tool_calls' errors.
func (m *Manager) removeOrphanToolResults() {
	openToolIDs := make(map[string]bool)
	changed := false

	for i := 0; i < len(m.messages); i++ {
		msg := m.messages[i]
		if msg.Role == "assistant" {
			for _, b := range msg.Content {
				if b.Type == "tool_use" {
					openToolIDs[b.ToolID] = true
				}
			}
		}

		// Check if this message contains orphan tool_results.
		hasToolResult := false
		allOrphan := true
		var kept []provider.ContentBlock
		for _, b := range msg.Content {
			if b.Type == "tool_result" {
				hasToolResult = true
				if openToolIDs[b.ToolID] {
					openToolIDs[b.ToolID] = false
					kept = append(kept, b)
					allOrphan = false
				} else {
					debug.Log("ctx", "removeOrphanToolResults: removing orphan tool_result id=%s name=%s at msg[%d]",
						b.ToolID, b.ToolName, i)
				}
			} else {
				kept = append(kept, b)
				if b.Type == "tool_use" {
					allOrphan = false
				}
			}
		}

		if hasToolResult {
			if allOrphan || len(kept) == 0 {
				m.messages = append(m.messages[:i], m.messages[i+1:]...)
				i--
				changed = true
			} else if len(kept) < len(msg.Content) {
				m.messages[i] = msg
				m.messages[i].Content = kept
				changed = true
			}
		}
	}
	if changed {
		m.version++
		m.tokens = 0
		for _, msg := range m.messages {
			m.tokens += m.countTokens(msg)
		}
		if m.baselineAvailable {
			m.baselineDelta = 0
		}
	}
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
	m.version++
	m.tokens += newTokens
}

// StartRunTracking clears the run-added message tracking. Call this at the
// start of each agent RunStreamWithContent. After the run, AddedSinceRunStart()
// returns all messages that were added via Add() during this run.
//
// Note: ApplyCompactResult replaces m.messages directly (bypassing Add),
// so compaction does NOT pollute runAdded. Messages added before compaction
// but during the same run are still tracked — this is correct because they
// are real conversation events that need to be persisted.
func (m *Manager) StartRunTracking() {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.runAdded = nil
}

// AddedSinceRunStart returns messages added via Add() since the last
// StartRunTracking(). This includes user messages, assistant responses,
// tool results, synthetic nudges, etc. — everything the agent added.
func (m *Manager) AddedSinceRunStart() []provider.Message {
	m.mu.Lock()
	defer m.mu.Unlock()
	out := make([]provider.Message, len(m.runAdded))
	copy(out, m.runAdded)
	return out
}

func (m *Manager) Messages() []provider.Message {
	m.mu.Lock()
	defer m.mu.Unlock()
	out := make([]provider.Message, len(m.messages))
	copy(out, m.messages)
	return out
}

func (m *Manager) MessagesAndTokenCount() ([]provider.Message, int) {
	m.mu.Lock()
	defer m.mu.Unlock()
	out := make([]provider.Message, len(m.messages))
	copy(out, m.messages)
	return out, m.tokenCountLocked()
}

func (m *Manager) CompactSnapshot() CompactSnapshot {
	m.mu.Lock()
	defer m.mu.Unlock()
	msgs := make([]provider.Message, len(m.messages))
	for i, msg := range m.messages {
		msgs[i] = msg
		msgs[i].Content = append([]provider.ContentBlock(nil), msg.Content...)
	}
	return CompactSnapshot{
		Messages:      msgs,
		OrigLen:       len(msgs),
		ContextWindow: m.contextWindow,
		OutputReserve: m.outputReserve,
		TodoPath:      m.todoPath,
		Version:       m.version,
	}
}

func (s CompactSnapshot) Compact(ctx context.Context, prov provider.Provider) (CompactResult, error) {
	scratch := NewManager(s.ContextWindow)
	scratch.SetOutputReserve(s.OutputReserve)
	scratch.SetProvider(prov)
	scratch.SetTodoFilePath(s.TodoPath)
	scratch.mu.Lock()
	scratch.messages = append([]provider.Message(nil), s.Messages...)
	scratch.recalcTokens()
	scratch.mu.Unlock()

	// Force summarization — the caller (precompact) already determined
	// that compaction is needed based on the LIVE token count (calibrated
	// from actual LLM API usage). The scratch manager's estimated token
	// count (via recalcTokens) may be significantly lower than the live
	// count, causing CheckAndSummarize to skip summarization and produce
	// a Changed=false result that ApplyCompactResult rejects.
	beforeVersion := scratch.version
	if err := scratch.Summarize(ctx, prov); err != nil {
		return CompactResult{}, err
	}
	after, tokens := scratch.MessagesAndTokenCount()
	changed := scratch.version != beforeVersion
	return CompactResult{
		Messages:   after,
		TokenCount: tokens,
		Changed:    changed,
	}, nil
}

// contentFingerprint returns a lightweight hash of message content for cheap
// change detection. Much faster than reflect.DeepEqual for large tool outputs.
func contentFingerprint(m provider.Message) uint64 {
	h := uint64(0x9e3779b9) // golden ratio
	for _, b := range m.Content {
		h ^= uint64(len(b.Text))
		h ^= uint64(len(b.Output))
		if b.ToolID != "" {
			h = h*31 ^ uint64(len(b.ToolID))
		}
	}
	return h
}

func (m *Manager) ApplyCompactResult(snapshot CompactSnapshot, result CompactResult) (bool, int) {
	if !result.Changed || len(result.Messages) == 0 {
		return false, m.TokenCount()
	}

	m.mu.Lock()
	defer m.mu.Unlock()
	if snapshot.OrigLen < 0 || len(m.messages) < snapshot.OrigLen {
		// Live has fewer messages than snapshot — messages were removed
		// (e.g. session cleared).  Stale, cannot apply.
		return false, m.tokenCountLocked()
	}

	// Detect (but do NOT reject) messages that changed within the snapshot
	// range.  The compaction summary is a lossy compression of the
	// conversation — it does not require byte-level accuracy of the source.
	// Using a slightly stale summary is always better than discarding the
	// result and never compacting (which leads to hitting the context limit).
	if m.version != snapshot.Version {
		mismatches := 0
		for i := range snapshot.Messages {
			if i >= len(m.messages) {
				mismatches++
				continue
			}
			if snapshot.Messages[i].Role == "system" && m.messages[i].Role == "system" {
				continue // system message is dynamically updated, always skip
			}
			live := m.messages[i]
			snap := snapshot.Messages[i]
			if live.Role != snap.Role || contentFingerprint(live) != contentFingerprint(snap) {
				mismatches++
			}
		}
		if mismatches > 0 {
			debug.Log("ctx", "ApplyCompactResult: %d/%d messages changed since snapshot — applying anyway (lossy summary is acceptable)",
				mismatches, len(snapshot.Messages))
		}
	}

	// Preserve the current system message. The snapshot's version may be stale
	// due to dynamic updates (lanchat peers, memory, autopilot state) during
	// the compaction window.
	var liveSystem provider.Message
	hasLiveSystem := false
	if len(m.messages) > 0 && m.messages[0].Role == "system" {
		liveSystem = m.messages[0]
		hasLiveSystem = true
	}

	// Messages appended after the snapshot are preserved as-is.
	origEnd := snapshot.OrigLen
	if origEnd > len(m.messages) {
		origEnd = len(m.messages)
	}
	extra := append([]provider.Message(nil), m.messages[origEnd:]...)
	newMsgs := append([]provider.Message(nil), result.Messages...)
	newMsgs = append(newMsgs, extra...)

	if hasLiveSystem && len(newMsgs) > 0 && newMsgs[0].Role == "system" {
		newMsgs[0] = liveSystem
	}

	m.messages = newMsgs
	m.version++
	m.recalcTokens()
	debug.Log("ctx", "ApplyCompactResult: applied snapshot msgs=%d compacted=%d extra=%d tokens=%d",
		snapshot.OrigLen, len(result.Messages), len(extra), m.tokenCountLocked())
	return true, m.tokenCountLocked()
}

func (m *Manager) TokenCount() int {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.tokenCountLocked()
}

func (m *Manager) ContextWindow() int {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.contextWindow
}

func (m *Manager) SetContextWindow(n int) {
	m.mu.Lock()
	defer m.mu.Unlock()
	debug.Log("ctx", "SetContextWindow: %d→%d", m.contextWindow, n)
	m.contextWindow = n
}

func (m *Manager) SetOutputReserve(n int) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if n < 0 {
		n = 0
	}
	debug.Log("ctx", "SetOutputReserve: raw=%d effective=%d (ceiling=%d)", n, m.effectiveOutputReserveLocked(), int(float64(m.contextWindow)*maxOutputReserveRatio))
	m.outputReserve = n
}

// SetCheckpointBaseline sets the initial token baseline from a session
// checkpoint. This avoids inflated token counts on session restore where
// the local estimator (len/4) diverges significantly from real token counts.
// The first real LLM call (RecordUsage) will override this with actual values.
func (m *Manager) SetCheckpointBaseline(tokens int) {
	if tokens <= 0 {
		return
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	m.baselineTokens = tokens
	m.baselineDelta = 0
	m.baselineAvailable = true
	debug.Log("ctx", "SetCheckpointBaseline: tokens=%d (replaces estimate %d)", tokens, m.tokens)
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
		m.version++
		m.tokens = m.countTokens(sys)
	} else {
		m.messages = nil
		m.version++
		m.tokens = 0
	}
	m.invalidateUsageBaselineLocked()
}

func (m *Manager) UsageRatio() float64 {
	if m.contextWindow <= 0 {
		return 0
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	return float64(m.tokenCountLocked()) / float64(m.contextWindow)
}

func (m *Manager) AutoCompactThreshold() int {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.autoCompactThresholdLocked()
}

func (m *Manager) PromptBudget() int {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.usablePromptBudgetLocked()
}

// Summarize compresses all messages (except system prompt) into a single
// summary. This implements rolling compaction: each invocation produces
// [system, summary, extra...] where extra = messages that arrived during
// the async compaction window. On the next trigger, the summary itself is
// included in the compression input, producing a fresh summary.
func (m *Manager) Summarize(ctx context.Context, prov provider.Provider) error {
	plan, ok := m.buildSummaryPlan()
	if !ok {
		debug.Log("ctx", "Summarize: no plan built, nothing to summarize")
		return nil
	}

	debug.Log("ctx", "Summarize: old_msgs=%d has_system=%t", len(plan.oldMsgs), plan.hasSystem)

	summaryText, err := summarizeMessages(ctx, prov, plan.oldMsgs, m.onUsage, m.summaryReserveTokens())
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

	newMsgs := make([]provider.Message, 0, len(extraMsgs)+2)
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
	newMsgs = append(newMsgs, extraMsgs...)

	oldLen := len(m.messages)
	m.messages = newMsgs
	m.version++
	m.recalcTokens()
	debug.Log("ctx", "Summarize: msgs=%d→%d tokens=%d", oldLen, len(newMsgs), m.tokenCountLocked())
	m.mu.Unlock()
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
	debug.Log("ctx", "CheckAndSummarize: tokens=%d threshold=%d budget=%d ratio=%.3f contextWindow=%d — %s",
		tokenCount, threshold, budget, ratio, m.ContextWindow(),
		func() string {
			if tokenCount < threshold {
				return "SKIP (below threshold)"
			}
			return "TRIGGERED"
		}())

	if tokenCount < threshold {
		return false, nil
	}

	debug.Log("ctx", "CheckAndSummarize: calling Summarize (tokens=%d)", tokenCount)
	beforeVersion := m.version
	err := m.Summarize(ctx, prov)
	if err != nil {
		debug.Log("ctx", "CheckAndSummarize: Summarize FAILED: %v", err)
		return false, err
	}
	summaryChanged := m.version != beforeVersion
	debug.Log("ctx", "CheckAndSummarize: done tokens=%d msgs=%d→%d changed=%t",
		m.TokenCount(), len(m.Messages()), summaryChanged)
	return summaryChanged, nil
}

func (m *Manager) TruncateOldestGroupForRetry() bool {
	m.mu.Lock()
	defer m.mu.Unlock()
	truncated, ok := truncateGroupsForPTLRetry(m.messages)
	if !ok {
		return false
	}
	m.messages = truncated
	m.version++
	m.recalcTokens()
	return true
}

// RemoveLastAssistantGroup removes the most recent assistant message and any
// trailing tool messages that follow it. This is used by /regenerate to
// discard the agent's last response so it can be re-generated. Returns the
// text of the last remaining user message, or "" if no regeneration is
// possible (no assistant message found or no preceding user message).
func (m *Manager) RemoveLastAssistantGroup() string {
	m.mu.Lock()
	defer m.mu.Unlock()
	if len(m.messages) == 0 {
		return ""
	}
	// Find the last assistant message.
	lastAsstIdx := -1
	for i := len(m.messages) - 1; i >= 0; i-- {
		if m.messages[i].Role == "assistant" {
			lastAsstIdx = i
			break
		}
	}
	if lastAsstIdx < 0 {
		return ""
	}
	// Find the last user message before the assistant message.
	lastUserIdx := -1
	for i := lastAsstIdx - 1; i >= 0; i-- {
		if m.messages[i].Role == "user" {
			lastUserIdx = i
			break
		}
	}
	if lastUserIdx < 0 {
		return ""
	}
	// Extract the user message text for re-submission.
	userText := ""
	for _, b := range m.messages[lastUserIdx].Content {
		if b.Type == "text" && b.Text != "" {
			userText = b.Text
			break
		}
	}
	// Truncate: keep everything up to and including the last user message,
	// discard the assistant response and any trailing tool messages.
	m.messages = m.messages[:lastUserIdx+1]
	m.version++
	m.recalcTokens()
	debug.Log("ctx", "RemoveLastAssistantGroup: removed %d messages from index %d, remaining=%d tokens=%d",
		len(m.messages)-lastUserIdx-1+1, lastAsstIdx, len(m.messages), m.tokenCountLocked())
	return userText
}

func (m *Manager) recalcTokens() {
	m.tokens = 0
	for _, msg := range m.messages {
		m.tokens += m.countTokens(msg)
	}
	m.invalidateUsageBaselineLocked()
}

// toolResultClearMinLen is the minimum Output length to bother clearing.
// Small results (e.g. "ok", "done") waste negligible tokens and may be
// more useful to keep inline for context.
const toolResultClearMinLen = 500

// toolUseInputClearMinLen is the minimum Input (arguments) length to bother
// clearing. Many tool calls have tiny arguments (e.g. {"path": "main.go"})
// that aren't worth truncating.
const toolUseInputClearMinLen = 200

// ClearOldToolResults replaces large tool_result outputs from older messages
// with short placeholders, keeping the most recent `keepN` tool results intact.
// This is a cheap, mechanical context-recovery technique that avoids the cost
// of LLM-based compaction. It is safe to call repeatedly (idempotent).
//
// Only clears:
//   - tool_result blocks with Output > toolResultClearMinLen
//   - that are not error results (IsError == false)
//   - that have not already been cleared (idempotency)
//
// Returns the estimated number of tokens freed.
func (m *Manager) ClearOldToolResults(keepN int) int {
	m.mu.Lock()
	defer m.mu.Unlock()

	// Pass 0: build tool info map from tool_use blocks for ACON-inspired
	// observation compression (ICML 2026). Instead of generic "[cleared: N chars]",
	// produce tool-specific summaries that preserve key context — file paths,
	// search patterns, command names — so the agent knows whether re-running
	// the tool is worthwhile.
	type toolInfo struct {
		name string
		args map[string]any
	}
	toolMap := make(map[string]toolInfo)
	for _, msg := range m.messages {
		for _, b := range msg.Content {
			if b.Type == "tool_use" && b.ToolID != "" {
				info := toolInfo{name: b.ToolName}
				if len(b.Input) > 0 {
					var args map[string]any
					if json.Unmarshal(b.Input, &args) == nil {
						info.args = args
					}
				}
				toolMap[b.ToolID] = info
			}
		}
	}

	// Pass 1: count clearable tool_results (large, non-error, not yet cleared).
	// Also skip results with semantic importance (error/debugging context) to
	// avoid "context collapse" — the phenomenon where iterative clearing erodes
	// critical debugging information (inspired by SWE-Pruner task-aware pruning
	// and ACE context collapse research, ICLR 2026).
	type clearTarget struct {
		msgIdx  int
		blkIdx  int
		origLen int
	}
	var targets []clearTarget
	skippedImportant := 0
	for i, msg := range m.messages {
		for j, b := range msg.Content {
			if b.Type != "tool_result" {
				continue
			}
			if b.IsError {
				continue
			}
			if len(b.Output) < toolResultClearMinLen {
				continue
			}
			if strings.HasPrefix(b.Output, "[cleared:") {
				continue
			}
			// Semantic importance: preserve results containing build/test
			// error output that the agent may still need for debugging.
			if hasSemanticImportance(b.Output) {
				skippedImportant++
				continue
			}
			targets = append(targets, clearTarget{msgIdx: i, blkIdx: j, origLen: len(b.Output)})
		}
	}

	// Determine which targets to clear (all except the last keepN)
	if len(targets) <= keepN {
		return 0
	}
	toClear := targets[:len(targets)-keepN]

	// Pass 2: replace outputs with tool-aware summaries
	freedChars := 0
	for _, t := range toClear {
		block := &m.messages[t.msgIdx].Content[t.blkIdx]
		origLen := t.origLen
		freedChars += origLen
		// Use tool name from the result block, or look up from tool_use.
		toolName := block.ToolName
		var args map[string]any
		if info, ok := toolMap[block.ToolID]; ok {
			if toolName == "" {
				toolName = info.name
			}
			args = info.args
		}
		block.Output = summarizeClearedResult(toolName, origLen, block.Output, args)
		block.Images = nil // clear images too — they're large and re-fetchable
	}

	if freedChars == 0 {
		return 0
	}

	before := m.tokens
	m.version++
	m.recalcTokens()
	freed := before - m.tokens
	debug.Log("ctx", "ClearOldToolResults: cleared %d tool results, freed ~%d tokens (keepN=%d, total_clearable=%d, skipped_important=%d)",
		len(toClear), freed, keepN, len(targets), skippedImportant)
	return freed
}

// hasSemanticImportance checks whether a tool result output contains content
// that is likely to be important for the agent's current debugging context.
// Such results are preserved during context clearing to avoid losing critical
// debugging information (SWE-Pruner task-aware pruning concept).
//
// This is a lightweight heuristic — no ML model needed. It checks for common
// error/build/test failure markers that indicate the output is error-relevant
// rather than just large file content.
func hasSemanticImportance(output string) bool {
	// Quick exit: only check outputs that might contain errors (> 50 chars).
	// Very short outputs are unlikely to contain meaningful error context.
	if len(output) < 50 {
		return false
	}

	// Check first 2000 chars — errors typically appear early in output.
	// This avoids scanning very large outputs fully (performance).
	check := output
	if len(check) > 2000 {
		check = check[:2000]
	}

	// Strong error markers: these substrings are almost certainly from
	// build/compiler/test/runtime errors. We check per-line and skip lines
	// that are code comments (// # /*) to reduce false positives.
	strongMarkers := []string{
		"error:", "fail:", "failed:", "panic:", "fatal:",
		"undefined:", "cannot find", "does not compile",
		"syntax error", "type error", "referenceerror", "typeerror:",
		"traceback (most recent call last)",
	}
	checkLower := strings.ToLower(check)
	for _, marker := range strongMarkers {
		if strings.Contains(checkLower, marker) {
			// Verify the marker is not solely inside a comment line.
			for _, line := range strings.Split(check, "\n") {
				trimmed := strings.TrimLeft(line, " \t")
				if strings.HasPrefix(trimmed, "//") || strings.HasPrefix(trimmed, "#") || strings.HasPrefix(trimmed, "/*") {
					continue // skip comment lines
				}
				if strings.Contains(strings.ToLower(trimmed), marker) {
					return true
				}
			}
		}
	}

	return false
}

// summarizeClearedResult produces a concise, tool-aware summary for a cleared
// tool result placeholder. Instead of the generic "[cleared: N chars]", it
// preserves key context — file paths, search patterns, command names — so the
// agent can decide whether re-running the tool is worthwhile.
//
// This implements ACON's "observation compression" concept (ICML 2026):
// "optimally compress both observations and history into concise, informative
// representations." Our approach uses deterministic heuristics per tool type
// rather than LLM-based compression, keeping it fast and cost-free.
//
// The output always starts with "[cleared:" for backward compatibility with
// idempotency checks and ClearOldToolUseInputs matching.
func summarizeClearedResult(toolName string, origLen int, output string, args map[string]any) string {
	summary := buildToolSummary(toolName, origLen, output, args)
	if summary == "" {
		summary = fmt.Sprintf("output was %d chars", origLen)
	}
	return fmt.Sprintf("[cleared: %s — re-run to see full result]", summary)
}

// buildToolSummary extracts the key contextual information from a tool result
// based on the tool type. Returns a short string (e.g., "read_file of
// main.go (~2KB)") or empty string if no specific info could be extracted.
func buildToolSummary(toolName string, origLen int, output string, args map[string]any) string {
	if args == nil {
		args = map[string]any{}
	}
	kbSize := origLen / 1024
	if kbSize == 0 {
		kbSize = 1 // show at least 1KB for readability
	}

	switch toolName {
	case "read_file":
		if path, ok := extractPath(args); ok {
			return fmt.Sprintf("read_file of %s (~%dKB)", shortPath(path), kbSize)
		}
		return fmt.Sprintf("read_file output (~%dKB)", kbSize)

	case "multi_file_read":
		if n := extractFileCount(args); n > 0 {
			return fmt.Sprintf("multi_file_read of %d files (~%dKB)", n, kbSize)
		}
		return fmt.Sprintf("multi_file_read output (~%dKB)", kbSize)

	case "grep":
		pattern, _ := args["pattern"].(string)
		resultLines := strings.Count(output, "\n")
		return fmt.Sprintf("grep for %q (%d lines, ~%dKB)", truncStr(pattern, 40), resultLines, kbSize)

	case "search_files":
		pattern, _ := args["pattern"].(string)
		return fmt.Sprintf("search_files for %q (~%dKB)", truncStr(pattern, 40), kbSize)

	case "list_directory":
		if path, ok := extractPath(args); ok {
			return fmt.Sprintf("list_directory of %s (~%dKB)", shortPath(path), kbSize)
		}
		return fmt.Sprintf("list_directory output (~%dKB)", kbSize)

	case "run_command":
		cmd, _ := args["command"].(string)
		firstLine := cmd
		if idx := strings.IndexByte(cmd, '\n'); idx > 0 {
			firstLine = cmd[:idx]
		}
		return fmt.Sprintf("run_command: %s (~%dKB)", truncStr(firstLine, 50), kbSize)

	case "glob":
		pattern, _ := args["pattern"].(string)
		return fmt.Sprintf("glob %q (~%dKB)", truncStr(pattern, 40), kbSize)

	case "git_diff", "git_status", "git_log", "git_show", "git_blame":
		return fmt.Sprintf("%s output (~%dKB)", toolName, kbSize)

	case "lsp_symbols", "lsp_definition", "lsp_references", "lsp_hover",
		"lsp_diagnostics", "lsp_implementation", "lsp_code_actions",
		"lsp_rename", "lsp_workspace_symbols":
		return fmt.Sprintf("%s output (~%dKB)", toolName, kbSize)

	case "web_fetch", "web_search":
		if url, _ := args["url"].(string); url != "" {
			return fmt.Sprintf("%s of %s (~%dKB)", toolName, truncStr(url, 50), kbSize)
		}
		if q, _ := args["query"].(string); q != "" {
			return fmt.Sprintf("%s for %q (~%dKB)", toolName, truncStr(q, 40), kbSize)
		}
		return fmt.Sprintf("%s output (~%dKB)", toolName, kbSize)

	default:
		if toolName != "" {
			return fmt.Sprintf("%s output (~%dKB)", toolName, kbSize)
		}
		return fmt.Sprintf("output was %d chars", origLen)
	}
}

// extractPath gets the "path" field from args, handling both string and
// nested "files" array structures.
func extractPath(args map[string]any) (string, bool) {
	if path, ok := args["path"].(string); ok && path != "" {
		return path, true
	}
	return "", false
}

// extractFileCount counts entries in a "files" array argument.
func extractFileCount(args map[string]any) int {
	if files, ok := args["files"].([]any); ok {
		return len(files)
	}
	return 0
}

// shortPath abbreviates long paths to keep summaries compact.
// Example: "/Volumes/new/ggai/ggcode/internal/agent/agent.go" → ".../agent/agent.go"
func shortPath(path string) string {
	// Keep last 3 path components for readability.
	parts := strings.Split(path, "/")
	if len(parts) <= 3 {
		return path
	}
	return ".../" + strings.Join(parts[len(parts)-3:], "/")
}

// truncStr truncates a string to maxLen, appending "..." if truncated.
func truncStr(s string, maxLen int) string {
	if len(s) <= maxLen {
		return s
	}
	return s[:maxLen-3] + "..."
}

// ClearOldToolUseInputs truncates the Input (arguments) of tool_use blocks
// whose corresponding tool_result has already been cleared by ClearOldToolResults.
// This recovers context from large tool arguments (e.g., full file content in
// edit_file/write_file Input) that are no longer needed once the result is gone.
//
// Only clears tool_use blocks where:
//   - Input length exceeds toolUseInputClearMinLen
//   - The matching tool_result Output starts with "[cleared:" (already cleared)
//   - Input has not already been truncated (idempotency)
//
// Returns the estimated number of tokens freed.
func (m *Manager) ClearOldToolUseInputs() int {
	m.mu.Lock()
	defer m.mu.Unlock()

	// Pass 1: collect ToolIDs of cleared tool_results
	clearedIDs := make(map[string]bool)
	for _, msg := range m.messages {
		for _, b := range msg.Content {
			if b.Type == "tool_result" && strings.HasPrefix(b.Output, "[cleared:") {
				clearedIDs[b.ToolID] = true
			}
		}
	}
	if len(clearedIDs) == 0 {
		return 0
	}

	// Pass 2: truncate matching tool_use Input blocks
	type inputTarget struct {
		msgIdx  int
		blkIdx  int
		origLen int
	}
	var targets []inputTarget
	for i, msg := range m.messages {
		for j, b := range msg.Content {
			if b.Type != "tool_use" {
				continue
			}
			if !clearedIDs[b.ToolID] {
				continue
			}
			if len(b.Input) < toolUseInputClearMinLen {
				continue
			}
			// Idempotency: check if already truncated
			var check map[string]any
			if json.Unmarshal(b.Input, &check) == nil {
				if v, ok := check["_cleared"].(bool); ok && v {
					continue
				}
			}
			targets = append(targets, inputTarget{msgIdx: i, blkIdx: j, origLen: len(b.Input)})
		}
	}

	if len(targets) == 0 {
		return 0
	}

	freedChars := 0
	for _, t := range targets {
		block := &m.messages[t.msgIdx].Content[t.blkIdx]
		origTool := block.ToolName
		freedChars += t.origLen
		// Replace with minimal placeholder that preserves tool name for context
		block.Input = json.RawMessage(fmt.Sprintf(`{"_cleared":true,"_tool":%q,"_note":"input was %d chars — already executed"}`, origTool, t.origLen))
	}

	before := m.tokens
	m.version++
	m.recalcTokens()
	freed := before - m.tokens
	debug.Log("ctx", "ClearOldToolUseInputs: cleared %d tool_use inputs, freed ~%d tokens", len(targets), freed)
	return freed
}

// CompactSupersededReads finds pairs of read_file/multi_file_read tool calls
// that target the same file path, and replaces the earlier (stale) result with
// a compact placeholder. When an agent reads a file, edits it, then re-reads
// it (or simply reads the same file twice), the earlier result holds outdated
// content that wastes context space. This method removes that redundancy
// proactively — before the general tool-result clearing tiers need to kick in.
//
// Inspired by Headroom's cross-agent context deduplication concept: if the
// same resource appears multiple times in context, only the latest copy needs
// to be retained. This is a purely mechanical operation (no LLM call needed)
// and is safe because the newer read always has the more current content.
//
// Returns the approximate number of tokens freed.
func (m *Manager) CompactSupersededReads() int {
	m.mu.Lock()
	defer m.mu.Unlock()

	// Phase 1: Scan tool_use blocks for read_file and multi_file_read.
	// Track file path → ordered list of ToolIDs that read it.
	pathToToolIDs := make(map[string][]string)
	for _, msg := range m.messages {
		for _, b := range msg.Content {
			if b.Type != "tool_use" {
				continue
			}
			paths := extractReadPaths(b.ToolName, b.Input)
			for _, p := range paths {
				norm := normalizeFilePath(p)
				pathToToolIDs[norm] = append(pathToToolIDs[norm], b.ToolID)
			}
		}
	}

	// Phase 2: For paths read more than once, all but the last read are superseded.
	supersededIDs := make(map[string]bool)
	for _, ids := range pathToToolIDs {
		if len(ids) > 1 {
			for _, id := range ids[:len(ids)-1] {
				supersededIDs[id] = true
			}
		}
	}

	if len(supersededIDs) == 0 {
		return 0
	}

	// Phase 3: Compact tool_results for superseded ToolIDs.
	freedChars := 0
	compacted := 0
	for i := range m.messages {
		for j := range m.messages[i].Content {
			b := &m.messages[i].Content[j]
			if b.Type != "tool_result" {
				continue
			}
			if !supersededIDs[b.ToolID] {
				continue
			}
			// Skip already-cleared or superseded results (idempotent).
			if strings.HasPrefix(b.Output, "[superseded:") || strings.HasPrefix(b.Output, "[cleared:") {
				continue
			}
			origLen := len(b.Output)
			if origLen < 200 {
				continue // skip small results — not worth compacting
			}
			b.Output = fmt.Sprintf("[superseded: file was re-read later in the conversation, output was %d chars]", origLen)
			b.Images = nil
			freedChars += origLen
			compacted++
		}
	}

	if freedChars == 0 {
		return 0
	}

	before := m.tokens
	m.version++
	m.recalcTokens()
	freed := before - m.tokens
	debug.Log("ctx", "CompactSupersededReads: compacted %d superseded file reads, freed ~%d tokens", compacted, freed)
	return freed
}

// extractReadPaths extracts file paths from the Input JSON of read tools.
// Supports read_file ({"path": "..."}) and multi_file_read ({"files": [{"path": "..."}]}).
func extractReadPaths(toolName string, input json.RawMessage) []string {
	if len(input) == 0 {
		return nil
	}
	switch toolName {
	case "read_file":
		var args struct {
			Path string `json:"path"`
		}
		if json.Unmarshal(input, &args) == nil && args.Path != "" {
			return []string{args.Path}
		}
	case "multi_file_read":
		var args struct {
			Files []struct {
				Path string `json:"path"`
			} `json:"files"`
		}
		if json.Unmarshal(input, &args) == nil {
			paths := make([]string, 0, len(args.Files))
			for _, f := range args.Files {
				if f.Path != "" {
					paths = append(paths, f.Path)
				}
			}
			return paths
		}
	}
	return nil
}

// normalizeFilePath normalizes a file path for comparison purposes.
// Strips "./" prefix, converts backslashes to forward slashes, and lowercases
// for case-insensitive filesystems (macOS, Windows).
func normalizeFilePath(p string) string {
	p = strings.TrimSpace(p)
	p = strings.ReplaceAll(p, "\\", "/")
	// Strip leading "./" repeatedly
	for strings.HasPrefix(p, "./") {
		p = p[2:]
	}
	// Collapse duplicate slashes
	for strings.Contains(p, "//") {
		p = strings.ReplaceAll(p, "//", "/")
	}
	return strings.ToLower(p)
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
	var sb strings.Builder
	var hasImage bool
	var toolCallCount int
	for _, b := range msg.Content {
		sb.WriteString(b.Text)
		sb.WriteString(b.ToolName)
		sb.WriteString(b.Output)
		sb.Write(b.Input)
		if b.Type == "image" || b.ImageData != "" || len(b.Images) > 0 {
			hasImage = true
		}
		if b.Type == "tool_use" {
			toolCallCount++
		}
	}
	n := EstimateTokens(sb.String())
	// Each message has ~4 tokens of structural overhead (role, separators).
	n += 4
	// Tool calls carry JSON structure overhead beyond their input text:
	// tool name, id, type field, opening/closing braces, etc.
	n += toolCallCount * 6
	// Images are roughly 85-170 tokens for standard thumbnails,
	// and up to 1100 tokens for large images. Use a conservative average.
	if hasImage {
		n += 170
	}
	return n
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
	if len(m.messages) > 0 && m.messages[0].Role == "system" {
		plan.hasSystem = true
		plan.systemMsg = m.messages[0]
		start = 1
	}

	plan.allMsgs = append([]provider.Message(nil), m.messages...)
	groups := buildMessageGroups(m.messages, start)
	if len(groups) == 0 {
		return summaryPlan{}, false
	}

	// Rolling compaction: summarize ALL messages (except system prompt).
	// No "recent groups" are kept verbatim. Messages produced during the
	// compaction window are preserved later by ApplyCompactResult as "extra".
	plan.oldMsgs = append([]provider.Message(nil), m.messages[start:]...)
	plan.recentMsgs = nil
	debug.Log("ctx", "buildSummaryPlan: summarizing all %d messages (groups=%d)", len(plan.oldMsgs), len(groups))
	return plan, len(plan.oldMsgs) > 0
}

func summarizeMessages(ctx context.Context, prov provider.Provider, msgs []provider.Message, onUsage func(provider.TokenUsage), summaryTokenLimit int) (string, error) {
	current := append([]provider.Message(nil), msgs...)
	for attempt := 0; attempt <= maxPTLRetries; attempt++ {
		payload := buildSummaryPayload(current)
		summaryMsgs := []provider.Message{
			{
				Role: "system",
				Content: []provider.ContentBlock{{
					Type: "text",
					Text: fmt.Sprintf(`You are summarizing a conversation between a user and an AI coding assistant. Produce a concise, structured summary that preserves the information most critical for continuing work.

Your summary must be under %d tokens. Be extremely concise.

Focus on preserving:
- Key decisions made and WHY (rationale, trade-offs considered)
- File paths that were read, created, or modified
- Code structure: function/class names, signatures, key variables, configuration values
- Errors encountered and how they were resolved
- Incomplete or pending work the assistant was about to do
- User preferences or constraints mentioned

You may omit:
- Full source code contents (reference file paths and key changes instead)
- Verbose command output (summarize the outcome)
- Repeated status checks or confirmations
- Tool call mechanics (focus on what was done, not which tool was used)

Format: Use clear sections with bullet points. Be specific with names, paths, and values.`, summaryTokenLimit),
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
		if onUsage != nil {
			onUsage(resp.Usage)
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
	target := compactTargetFixed
	if cw := m.contextWindow; cw > 0 && target > cw/4 {
		target = cw / 4 // cap at 25% for small context windows
	}
	if target < minSummaryReserve {
		return minSummaryReserve
	}
	return target
}

// summaryReserveTokens returns the token budget reserved for the summary
// LLM call's output. Capped at maxSummaryOutputRatio (5%) of contextWindow
// AND a fixed absolute maximum (maxSummaryOutputTokens).
func (m *Manager) summaryReserveTokens() int {
	if m.contextWindow <= 0 {
		return minSummaryReserve
	}
	reserve := int(float64(m.contextWindow) * maxSummaryOutputRatio)
	if reserve < minSummaryReserve {
		return minSummaryReserve
	}
	if reserve > maxSummaryOutputTokens {
		return maxSummaryOutputTokens
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
	return int(float64(budget) * summarizeThreshold)
}

func (m *Manager) usablePromptBudgetLocked() int {
	if m.contextWindow <= 0 {

		return 0
	}
	reserve := m.effectiveOutputReserveLocked()
	safety := m.effectiveSafetyMarginLocked()
	budget := m.contextWindow - reserve - safety
	if budget < minSummaryReserve {
		return minSummaryReserve
	}
	return budget
}

func (m *Manager) effectiveOutputReserveLocked() int {
	if m.contextWindow <= 0 {
		return 0
	}
	floor := minInt(8192, maxInt(512, m.contextWindow/10))
	ceiling := maxInt(floor, int(float64(m.contextWindow)*maxOutputReserveRatio))
	reserve := m.outputReserve
	if reserve <= 0 {
		reserve = int(float64(m.contextWindow) * defaultOutputReserveRatio)
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
	if m.contextWindow <= 0 {
		return minSummaryReserve
	}
	safety := int(float64(m.contextWindow) * safetyMarginRatio)
	safetyFloor := minInt(4096, maxInt(minSummaryReserve, m.contextWindow/20))
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

func buildSummaryPayload(msgs []provider.Message) string {
	const (
		payloadToolResultMaxLen = 500 // max chars for tool result in summary payload
		payloadToolResultHead   = 200 // keep first N chars
		payloadToolInputMaxLen  = 300 // max chars for tool input in summary payload
	)

	var sb strings.Builder
	for _, msg := range msgs {
		sb.WriteString(fmt.Sprintf("[%s]\n", msg.Role))
		for _, block := range msg.Content {
			switch block.Type {
			case "text":
				sb.WriteString(block.Text)
				sb.WriteByte('\n')
			case "tool_use":
				input := formatToolInputForSummary(block.Input, payloadToolInputMaxLen)
				if input != "" {
					sb.WriteString(fmt.Sprintf("Tool call: %s(%s)\n", block.ToolName, input))
				} else {
					sb.WriteString(fmt.Sprintf("Tool call: %s\n", block.ToolName))
				}
			case "tool_result":
				output := block.Output
				if len(output) > payloadToolResultMaxLen {
					output = output[:payloadToolResultHead] + fmt.Sprintf("\n... (truncated, original %d chars)", len(output))
				}
				sb.WriteString(fmt.Sprintf("Tool result: %s\n", output))
			}
		}
		sb.WriteByte('\n')
	}
	return sb.String()
}

// formatToolInputForSummary extracts the most informative fields from a tool
// call's JSON input and returns a concise human-readable string. This lets the
// summarization LLM know WHICH file was read, WHAT command was run, etc.
// Without this, the summarizer only sees "Tool call: read_file" with no path.
func formatToolInputForSummary(input []byte, maxLen int) string {
	if len(input) == 0 || maxLen <= 0 {
		return ""
	}
	var m map[string]any
	if err := json.Unmarshal(input, &m); err != nil {
		return ""
	}
	// Priority fields by importance for summarization
	priorityKeys := []string{
		"path", "file_path", "command", "pattern", "query", "url",
		"directory", "task", "prompt", "message", "revision",
	}
	var parts []string
	for _, key := range priorityKeys {
		val, ok := m[key]
		if !ok {
			continue
		}
		s := fmt.Sprintf("%v", val)
		s = strings.ReplaceAll(s, "\n", " ")
		if len(s) > 80 {
			s = s[:77] + "..."
		}
		parts = append(parts, fmt.Sprintf("%s=%q", key, s))
		delete(m, key) // remove so we don't double-report
	}
	// Include remaining short scalar fields (e.g., old_text/new_text snippets)
	for k, v := range m {
		if len(parts) >= 4 {
			break // limit total fields
		}
		s, ok := v.(string)
		if !ok {
			continue
		}
		s = strings.ReplaceAll(s, "\n", " ")
		if len(s) > 60 {
			s = s[:57] + "..."
		}
		if s == "" {
			continue
		}
		parts = append(parts, fmt.Sprintf("%s=%q", k, s))
	}
	result := strings.Join(parts, ", ")
	if len(result) > maxLen {
		result = result[:maxLen-3] + "..."
	}
	return result
}

func truncateGroupsForPTLRetry(msgs []provider.Message) ([]provider.Message, bool) {
	if len(msgs) < 2 {
		return nil, false
	}
	start := 0
	var prefix []provider.Message
	if msgs[0].Role == "system" {
		start = 1
		prefix = append(prefix, msgs[0])
	}
	groups := buildMessageGroups(msgs, start)
	if len(groups) < 2 {
		return nil, false
	}
	truncated := append([]provider.Message(nil), prefix...)
	truncated = append(truncated, msgs[groups[1].start:]...)
	return truncated, true
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
		return "" // no session bound — no todo summary
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
