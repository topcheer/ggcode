package context

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"runtime"
	"strings"
	"sync"
	"time"
	"unicode/utf8"

	"github.com/google/uuid"
	"github.com/topcheer/ggcode/internal/debug"
	"github.com/topcheer/ggcode/internal/provider"
)

func newMessageID() string {
	return "msg_" + uuid.New().String()
}

type ContextManager interface {
	Add(msg provider.Message)
	Messages() []provider.Message
	TokenCount() int
	MessagesAndTokenCount() ([]provider.Message, int)
	SummaryMsgID() string
	ContextWindow() int
	OutputReserve() int
	SetContextWindow(n int)
	SetOutputReserve(n int)
	RecordUsage(usage provider.TokenUsage)
	Summarize(ctx context.Context, prov provider.Provider) error
	CheckAndSummarize(ctx context.Context, prov provider.Provider) (bool, error)
	TruncateOldestGroupForRetry() bool
	RemoveLastAssistantGroup() string
	RemoveLastUserMessage() bool
	Clear()
	UsageRatio() float64
	AutoCompactThreshold() int
	SetToolDefinitionOverhead(tokens int)
	ReconcileToolCalls() bool
	Pinned() *PinnedContext
}

type CompactSnapshot struct {
	Messages      []provider.Message
	OrigLen       int
	LastMsgID     string
	ContextWindow int
	OutputReserve int
	TodoPath      string
	Version       int64
}

type CompactResult struct {
	Messages   []provider.Message
	TokenCount int
	Changed    bool
}

type CompactRejectReason int

const (
	CompactRejectNone CompactRejectReason = iota
	CompactRejectNoChange
	CompactRejectEmpty
	CompactRejectUserReset
	CompactRejectBenignTrim
)

func (r CompactRejectReason) String() string {
	switch r {
	case CompactRejectNone:
		return "none"
	case CompactRejectNoChange:
		return "no-change"
	case CompactRejectEmpty:
		return "empty"
	case CompactRejectUserReset:
		return "user-reset"
	case CompactRejectBenignTrim:
		return "benign-trim"
	default:
		return "unknown"
	}
}

const (
	maxSummaryOutputRatio  = 0.05
	maxSummaryOutputTokens = 12000

	defaultOutputReserveRatio  = 0.10
	maxOutputReserveRatio      = 0.25
	safetyMarginRatio          = 0.05
	minRecentGroups            = 1
	maxRecentGroupTokenRatio   = 0.15
	minSummaryReserve          = 64
	uncoveredScriptFreezeShare = 0.20
	maxPTLRetries              = 3
	tokenCountTimeout          = 100 * time.Millisecond
)

type Manager struct {
	mu                       sync.Mutex
	messages                 []provider.Message
	version                  int64
	nonTailMutSeq            int64
	runAdded                 []provider.Message
	runAddedIDs              map[string]bool
	tokens                   int
	contextWindow            int
	outputReserve            int
	baselineTokens           int
	baselineDelta            int
	baselineAvailable        bool
	provider                 provider.Provider
	providerCountChecked     bool
	providerCountSupportsRPC bool
	todoPath                 string
	compactedArchive         []CompactedEntry
	onUsage                  func(provider.TokenUsage)
	calibrator               *TokenCalibrator
	onPersist                func(msg provider.Message)
	toolDefinitionOverhead   int
	pinned                   *PinnedContext
	postCompactNoteFn        func() string
	lastLoggedReserve        int
	lastLoggedThreshold      int
	benignRemoval            bool
	userReset                bool
	lastReject               CompactRejectReason
	lastSummarizeApplied     bool
	earlyCompactRatio        float64
}

func NewManager(contextWindow int) *Manager {
	return &Manager{
		contextWindow: contextWindow,
		calibrator:    NewTokenCalibrator(),
		pinned:        newPinnedContext(),
	}
}

func (m *Manager) markBenignRemoval() {
	m.benignRemoval = true
}

func (m *Manager) LastCompactRejectReason() CompactRejectReason {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.lastReject
}

func (m *Manager) Pinned() *PinnedContext {
	return m.pinned
}

func (m *Manager) injectPinnedAfterCompaction() {
	m.removeSystemMessageByMarker(pinnedMarker)

	if m.pinned == nil || m.pinned.IsEmpty() {
		return
	}

	rendered := m.pinned.Render()
	if rendered == "" {
		return
	}

	pinnedMsg := provider.Message{
		Role: "system",
		Content: []provider.ContentBlock{
			{Type: "text", Text: rendered},
		},
	}
	pinnedMsg.ID = newMessageID()

	insertIdx := m.findSystemMessageIdx("[Previous conversation summary]")
	if insertIdx >= 0 {
		insertIdx++
	} else if len(m.messages) > 0 && m.messages[0].Role == "system" {
		insertIdx = 1
	} else {
		insertIdx = 0
	}

	m.messages = append(m.messages, provider.Message{})
	copy(m.messages[insertIdx+1:], m.messages[insertIdx:])
	m.messages[insertIdx] = pinnedMsg

	debug.Log("ctx", "injectPinnedAfterCompaction: injected %d pinned items at position %d", len(m.pinned.List()), insertIdx)
}

func (m *Manager) SetPostCompactNoteProvider(fn func() string) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.postCompactNoteFn = fn
}

const postCompactNoteMarker = "[Session State Note - refreshed after compaction]"

func (m *Manager) injectPostCompactNoteAfterCompaction() {
	m.removeSystemMessageByMarker(postCompactNoteMarker)

	if m.postCompactNoteFn == nil {
		return
	}
	note := strings.TrimSpace(m.postCompactNoteFn())
	if note == "" {
		return
	}

	noteMsg := provider.Message{
		Role: "system",
		Content: []provider.ContentBlock{
			{Type: "text", Text: postCompactNoteMarker + "\n" + note},
		},
	}
	noteMsg.ID = newMessageID()

	insertIdx := m.findSystemMessageIdx("[Previous conversation summary]")
	if insertIdx >= 0 {
		insertIdx++
	} else if len(m.messages) > 0 && m.messages[0].Role == "system" {
		insertIdx = 1
	} else {
		insertIdx = 0
	}

	m.messages = append(m.messages, provider.Message{})
	copy(m.messages[insertIdx+1:], m.messages[insertIdx:])
	m.messages[insertIdx] = noteMsg

	debug.Log("ctx", "injectPostCompactNoteAfterCompaction: injected %d-char note at position %d", len(note), insertIdx)
}

func (m *Manager) findSystemMessageIdx(marker string) int {
	for i, msg := range m.messages {
		if msg.Role == "system" && len(msg.Content) > 0 &&
			msg.Content[0].Type == "text" &&
			strings.Contains(msg.Content[0].Text, marker) {
			return i
		}
	}
	return -1
}

func (m *Manager) removeSystemMessageByMarker(marker string) {
	idx := m.findSystemMessageIdx(marker)
	if idx >= 0 {
		m.messages = append(m.messages[:idx], m.messages[idx+1:]...)
		m.markBenignRemoval()
	}
}

func (m *Manager) SetPersistHandler(fn func(msg provider.Message)) {
	m.mu.Lock()
	m.onPersist = fn
	m.mu.Unlock()
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

func (m *Manager) SetProvider(p provider.Provider) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.provider = p
	m.providerCountChecked = false
	m.providerCountSupportsRPC = false
}

func (m *Manager) Add(msg provider.Message) {
	m.mu.Lock()
	if msg.ID == "" {
		msg.ID = newMessageID()
	}
	msgTokens := m.countTokens(msg)
	m.messages = append(m.messages, msg)
	if m.runAddedIDs == nil {
		m.runAddedIDs = make(map[string]bool)
	}
	if !m.runAddedIDs[msg.ID] {
		m.runAddedIDs[msg.ID] = true
		m.runAdded = append(m.runAdded, msg)
	}
	m.version++
	m.tokens += msgTokens
	if m.baselineAvailable {
		m.baselineDelta += msgTokens
	}
	ratio := 0.0
	if m.contextWindow > 0 {
		ratio = float64(m.tokenCountLocked()) / float64(m.contextWindow)
	}
	persistFn := m.onPersist
	debug.Log("ctx", "Add: role=%s blocks=%d msg_tokens=%d total=%d max=%d ratio=%.3f baseline=%t",
		msg.Role, len(msg.Content), msgTokens, m.tokenCountLocked(), m.contextWindow, ratio, m.baselineAvailable)
	m.mu.Unlock()
	if persistFn != nil {
		persistFn(msg)
	}
}

func (m *Manager) ReconcileToolCalls() bool {
	m.mu.Lock()
	defer m.mu.Unlock()

	m.removeOrphanToolResults()

	fixes := m.collectReconcileFixes()
	if len(fixes) == 0 {
		return false
	}
	return m.applyReconcileFixes(fixes)
}

type reconcileLateResult struct {
	msgIdx   int
	blockIdx int
	block    provider.ContentBlock
}

type reconcileID struct{ id, name string }

type reconcileNeedFix struct {
	insertBefore int
	lateBlocks   []reconcileLateResult
	missingIDs   []reconcileID
}

func (m *Manager) collectReconcileFixes() []reconcileNeedFix {
	var fixes []reconcileNeedFix

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

		nextAssistantIdx := m.nextAssistantIndex(idx)

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

		late, missingIDs := m.findLateOrMissingResults(toolIDs, nextAssistantIdx)
		if len(late) == 0 && len(missingIDs) == 0 {
			continue
		}

		fixes = append(fixes, reconcileNeedFix{
			insertBefore: nextAssistantIdx,
			lateBlocks:   late,
			missingIDs:   missingIDs,
		})
	}
	return fixes
}

func (m *Manager) nextAssistantIndex(start int) int {
	for j := start + 1; j < len(m.messages); j++ {
		if m.messages[j].Role == "assistant" {
			return j
		}
	}
	return len(m.messages)
}

func (m *Manager) findLateOrMissingResults(toolIDs map[string]string, boundaryIdx int) (late []reconcileLateResult, missingIDs []reconcileID) {
	for id, name := range toolIDs {
		foundLate := false
		for j := boundaryIdx; j < len(m.messages); j++ {
			for bi, block := range m.messages[j].Content {
				if block.Type == "tool_result" && block.ToolID == id {
					late = append(late, reconcileLateResult{msgIdx: j, blockIdx: bi, block: block})
					foundLate = true
					break
				}
			}
			if foundLate {
				break
			}
		}
		if !foundLate {
			missingIDs = append(missingIDs, reconcileID{id: id, name: name})
		}
	}
	return late, missingIDs
}

func (m *Manager) applyReconcileFixes(fixes []reconcileNeedFix) bool {
	oldMsgs := m.messages

	staleBlockIdxs := make(map[int]map[int]bool)
	var insertions []reconcileInsertion
	for _, fix := range fixes {
		for _, lr := range fix.lateBlocks {
			if staleBlockIdxs[lr.msgIdx] == nil {
				staleBlockIdxs[lr.msgIdx] = make(map[int]bool)
			}
			staleBlockIdxs[lr.msgIdx][lr.blockIdx] = true
		}
		var content []provider.ContentBlock
		seen := make(map[string]bool)
		for _, lr := range fix.lateBlocks {
			if !seen[lr.block.ToolID] {
				content = append(content, lr.block)
				seen[lr.block.ToolID] = true
			}
		}
		for _, mid := range fix.missingIDs {
			if !seen[mid.id] {
				name := mid.name
				if name == "" {
					name = "unknown"
				}
				content = append(content, provider.ToolResultNamedBlock(
					mid.id, name,
					"operation cancelled - tool call was interrupted before it could complete",
					true,
				))
				seen[mid.id] = true
			}
		}
		if len(content) > 0 {
			insertions = append(insertions, reconcileInsertion{
				insertBefore: fix.insertBefore,
				msg:          provider.Message{Role: "user", Content: content},
			})
		}
	}

	newMsgs, droppedMsgs, removedBlocks := rebuildReconciledMessages(oldMsgs, insertions, staleBlockIdxs)

	m.messages = newMsgs
	m.markBenignRemoval()
	m.version++
	m.nonTailMutSeq++
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
	debug.Log("ctx", "ReconcileToolCalls: relocated %d late tool_result(s), added %d cancelled, removed %d stale block(s) from %d message(s) (%d emptied, user content kept)",
		lateCount, cancelledCount, removedBlocks, len(staleBlockIdxs), droppedMsgs)
	return true
}

type reconcileInsertion struct {
	insertBefore int
	msg          provider.Message
}

func rebuildReconciledMessages(oldMsgs []provider.Message, insertions []reconcileInsertion, staleBlockIdxs map[int]map[int]bool) (newMsgs []provider.Message, droppedMsgs, removedBlocks int) {
	newMsgs = make([]provider.Message, 0, len(oldMsgs)+len(insertions))
	for i, m := range oldMsgs {
		for _, ins := range insertions {
			if ins.insertBefore == i {
				newMsgs = append(newMsgs, ins.msg)
			}
		}
		kept, emptied := stripStaleBlocks(m, staleBlockIdxs[i])
		if emptied {
			droppedMsgs++
			continue
		}
		m.Content = kept
		newMsgs = append(newMsgs, m)
	}
	for _, ins := range insertions {
		if ins.insertBefore == len(oldMsgs) {
			newMsgs = append(newMsgs, ins.msg)
		}
	}
	for _, set := range staleBlockIdxs {
		removedBlocks += len(set)
	}
	return newMsgs, droppedMsgs, removedBlocks
}

func stripStaleBlocks(msg provider.Message, stale map[int]bool) (kept []provider.ContentBlock, emptied bool) {
	if len(stale) == 0 {
		return msg.Content, false
	}
	kept = make([]provider.ContentBlock, 0, len(msg.Content))
	for bi, b := range msg.Content {
		if stale[bi] {
			continue
		}
		kept = append(kept, b)
	}
	return kept, len(kept) == 0
}

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
				allOrphan = false
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
		m.markBenignRemoval()
		m.version++
		m.nonTailMutSeq++
		m.tokens = 0
		for _, msg := range m.messages {
			m.tokens += m.countTokens(msg)
		}
		if m.baselineAvailable {
			m.baselineDelta = 0
		}
	}
}

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
	newTokens := m.countTokens(msg)
	m.messages = append([]provider.Message{msg}, m.messages...)
	m.version++
	m.nonTailMutSeq++
	m.tokens += newTokens
}

func (m *Manager) StartRunTracking() {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.runAdded = nil
	m.runAddedIDs = nil
}

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

func (m *Manager) SummaryMsgID() string {
	m.mu.Lock()
	defer m.mu.Unlock()
	for _, msg := range m.messages {
		if msg.Role == "system" && len(msg.Content) > 0 && msg.Content[0].Type == "text" {
			if strings.Contains(msg.Content[0].Text, "[Previous conversation summary]") {
				return msg.ID
			}
		}
	}
	return ""
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
	m.benignRemoval = false
	m.userReset = false
	msgs := make([]provider.Message, len(m.messages))
	for i, msg := range m.messages {
		msgs[i] = msg
		msgs[i].Content = append([]provider.ContentBlock(nil), msg.Content...)
	}
	lastID := ""
	if len(msgs) > 0 {
		lastID = msgs[len(msgs)-1].ID
	}
	return CompactSnapshot{
		Messages:      msgs,
		OrigLen:       len(msgs),
		LastMsgID:     lastID,
		ContextWindow: m.contextWindow,
		OutputReserve: m.outputReserve,
		TodoPath:      m.todoPath,
		Version:       m.version,
	}
}

func (s CompactSnapshot) Compact(ctx context.Context, prov provider.Provider) (CompactResult, error) {
	debug.Log("ctx", "CompactSnapshot.Compact: START snapshotMsgs=%d contextWindow=%d outputReserve=%d",
		len(s.Messages), s.ContextWindow, s.OutputReserve)
	scratch := NewManager(s.ContextWindow)
	scratch.SetOutputReserve(s.OutputReserve)
	scratch.SetProvider(prov)
	scratch.SetTodoFilePath(s.TodoPath)
	scratch.mu.Lock()
	scratch.messages = append([]provider.Message(nil), s.Messages...)
	scratch.recalcTokens()
	scratchTokensBefore := scratch.tokens
	scratch.mu.Unlock()
	debug.Log("ctx", "CompactSnapshot.Compact: scratch tokens before summarize=%d", scratchTokensBefore)

	beforeVersion := scratch.version
	if err := scratch.Summarize(ctx, prov); err != nil {
		debug.Log("ctx", "CompactSnapshot.Compact: Summarize FAILED: %v", err)
		return CompactResult{}, err
	}
	after, tokens := scratch.MessagesAndTokenCount()
	changed := scratch.version != beforeVersion
	summaryChars, summaryToks, summaryMsgCount := 0, 0, 0
	for _, msg := range after {
		if msg.Role == "system" && len(msg.Content) > 0 && strings.Contains(msg.Content[0].Text, "[Previous conversation summary]") {
			text := msg.Content[0].Text
			summaryChars = len(text)
			summaryToks = EstimateTokens(text)
			summaryMsgCount++
		}
	}
	debug.Log("ctx", "CompactSnapshot.Compact: DONE scratchTokens=%d→%d resultMsgs=%d changed=%t summaryMsgCount=%d summaryChars=%d summaryEstimatedTokens=%d",
		scratchTokensBefore, tokens, len(after), changed, summaryMsgCount, summaryChars, summaryToks)
	return CompactResult{
		Messages:   after,
		TokenCount: tokens,
		Changed:    changed,
	}, nil
}

func contentFingerprint(m provider.Message) uint64 {
	h := uint64(0x9e3779b9)
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
		debug.Log("ctx", "ApplyCompactResult: REJECT result.changed=%t result.msgs=%d", result.Changed, len(result.Messages))
		m.mu.Lock()
		if !result.Changed {
			m.lastReject = CompactRejectNoChange
		} else {
			m.lastReject = CompactRejectEmpty
		}
		m.mu.Unlock()
		return false, m.TokenCount()
	}

	m.mu.Lock()
	liveTokensBefore := m.tokenCountLocked()

	extraStart := 0
	anchorMissing := false
	if snapshot.LastMsgID != "" {
		foundIdx := -1
		for i, msg := range m.messages {
			if msg.ID == snapshot.LastMsgID {
				foundIdx = i
			}
		}
		if foundIdx >= 0 {
			extraStart = foundIdx + 1
		} else {
			anchorMissing = true
		}
	} else {
		if snapshot.OrigLen >= 0 && snapshot.OrigLen <= len(m.messages) {
			extraStart = snapshot.OrigLen
		} else {
			extraStart = len(m.messages)
		}
	}
	if extraStart > len(m.messages) {
		extraStart = len(m.messages)
	}
	debug.Log("ctx", "ApplyCompactResult: liveMsgs=%d snapshot.OrigLen=%d snapshot.LastMsgID=%s extraStart=%d result.msgs=%d anchorMissing=%t",
		len(m.messages), snapshot.OrigLen, snapshot.LastMsgID, extraStart, len(result.Messages), anchorMissing)

	if anchorMissing {
		if m.userReset {
			m.lastReject = CompactRejectUserReset
		} else if m.benignRemoval {
			m.lastReject = CompactRejectBenignTrim
		} else {
			m.lastReject = CompactRejectUserReset
		}
		debug.Log("ctx", "ApplyCompactResult: REJECT anchor-missing (%s): LastMsgID %q not in live msgs (liveMsgs=%d, snapshotMsgs=%d) — removed during compaction window; refusing to apply",
			m.lastReject, snapshot.LastMsgID, len(m.messages), len(snapshot.Messages))
		n := m.tokenCountLocked()
		m.mu.Unlock()
		return false, n
	}

	if len(m.messages) < len(snapshot.Messages) {
		if m.userReset {
			m.lastReject = CompactRejectUserReset
		} else if m.benignRemoval {
			m.lastReject = CompactRejectBenignTrim
		} else {
			m.lastReject = CompactRejectUserReset
		}
		debug.Log("ctx", "ApplyCompactResult: REJECT live-shrunk (%s): liveMsgs=%d < snapshotMsgs=%d (messages removed during compaction window)",
			m.lastReject, len(m.messages), len(snapshot.Messages))
		n := m.tokenCountLocked()
		m.mu.Unlock()
		return false, n
	}

	if m.version != snapshot.Version {
		mismatches := 0
		for i := range snapshot.Messages {
			if i >= len(m.messages) {
				mismatches++
				continue
			}
			if snapshot.Messages[i].Role == "system" && m.messages[i].Role == "system" {
				continue
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

	var liveSystem provider.Message
	hasLiveSystem := false
	if len(m.messages) > 0 && m.messages[0].Role == "system" {
		liveSystem = m.messages[0]
		hasLiveSystem = true
	}

	m.archiveCompactedMessages(m.messages[:extraStart])
	extra := append([]provider.Message(nil), m.messages[extraStart:]...)
	newMsgs := append([]provider.Message(nil), result.Messages...)
	newMsgs = append(newMsgs, extra...)

	if hasLiveSystem && len(newMsgs) > 0 {
		for i := range newMsgs {
			if newMsgs[i].Role == "system" && len(newMsgs[i].Content) > 0 &&
				newMsgs[i].Content[0].Type == "text" &&
				strings.Contains(newMsgs[i].Content[0].Text, "[Previous conversation summary]") {
				continue
			}
			if newMsgs[i].Role == "system" {
				newMsgs[i] = liveSystem
				break
			}
		}
	}

	m.messages = newMsgs

	m.injectPinnedAfterCompaction()
	m.injectPostCompactNoteAfterCompaction()

	m.version++
	m.nonTailMutSeq++
	m.recalcTokens()
	liveTokensAfter := m.tokenCountLocked()

	var persistedSummary *provider.Message
	for i := range result.Messages {
		sm := result.Messages[i]
		if sm.Role == "system" && len(sm.Content) > 0 && sm.Content[0].Type == "text" &&
			strings.Contains(sm.Content[0].Text, "[Previous conversation summary]") {
			if sm.ID == "" {
				sm.ID = newMessageID()
			}
			if m.runAddedIDs == nil {
				m.runAddedIDs = make(map[string]bool)
			}
			if !m.runAddedIDs[sm.ID] {
				m.runAddedIDs[sm.ID] = true
				m.runAdded = append(m.runAdded, sm)
			}
			persistedSummary = &sm
			break
		}
	}

	debug.Log("ctx", "ApplyCompactResult: APPLIED liveTokensBefore=%d liveTokensAfter=%d snapshotMsgs=%d compacted=%d extra=%d resultTokenCount=%d",
		liveTokensBefore, liveTokensAfter, snapshot.OrigLen, len(result.Messages), len(extra), result.TokenCount)
	m.lastReject = CompactRejectNone

	persistFn := m.onPersist
	m.mu.Unlock()
	if persistFn != nil && persistedSummary != nil {
		persistFn(*persistedSummary)
	}
	return true, liveTokensAfter
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

func (m *Manager) OutputReserve() int {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.outputReserve
}

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
	if usage.InputTokens <= 0 && usage.OutputTokens <= 0 {
		return
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	if usage.InputTokens <= 0 {
		debug.Log("ctx", "RecordUsage: skipping baseline update — InputTokens=%d OutputTokens=%d (missing input tokens)",
			usage.InputTokens, usage.OutputTokens)
		return
	}
	oldBaseline := m.baselineTokens
	totalInput := usage.InputTokens
	if usage.CacheRead > 0 && usage.PromptTokensTotal > usage.InputTokens {
		totalInput = usage.PromptTokensTotal
	}
	m.baselineTokens = totalInput + usage.OutputTokens
	m.baselineDelta = 0
	m.baselineAvailable = true
	asciiChars, cjkChars, latinExtChars := m.compositionLocked()
	totalRunes := m.totalContentRunes()
	if asciiChars+cjkChars+latinExtChars == 0 {
		if totalRunes > 0 {
			debug.Log("context-calibrator", "sample-frozen: uncovered scripts only (see #598/#605)")
			return
		}
	} else if totalRunes > 0 {
		uncovered := totalRunes - asciiChars - cjkChars - latinExtChars
		if uncovered > 0 && float64(uncovered)/float64(totalRunes) > uncoveredScriptFreezeShare {
			debug.Log("context-calibrator", "sample-frozen: uncovered scripts are %d/%d runes (see #623)",
				uncovered, totalRunes)
			return
		}
	}
	for _, msg := range m.messages {
		for _, b := range msg.Content {
			if b.Type == "image" || b.Type == "thinking" || b.Type == "redacted_thinking" || len(b.Images) > 0 {
				debug.Log("context-calibrator", "sample-frozen: image/thinking blocks present (see #1618-A)")
				return
			}
		}
	}
	m.calibrator.RecordSample(m.tokens+m.toolDefinitionOverhead, totalInput, asciiChars, cjkChars, latinExtChars)
	debug.Log("ctx", "RecordUsage: input=%d cache_read=%d output=%d old_baseline=%d→new_baseline=%d estimated=%d delta=%d",
		usage.InputTokens, usage.CacheRead, usage.OutputTokens, oldBaseline, m.baselineTokens, m.tokens, m.baselineTokens-m.tokens)
}

func (m *Manager) totalContentRunes() int {
	total := 0
	for _, msg := range m.messages {
		for _, b := range msg.Content {
			total += utf8.RuneCountInString(b.Text + b.ReasoningContent + b.Output)
		}
	}
	return total
}

func (m *Manager) compositionLocked() (asciiChars, cjkChars, latinExtChars int) {
	for _, msg := range m.messages {
		for _, b := range msg.Content {
			text := b.Text + b.ReasoningContent + b.Output
			a, c, le, _, _, _ := scriptTokenClasses(text)
			asciiChars += a
			cjkChars += c
			latinExtChars += le
		}
	}
	return asciiChars, cjkChars, latinExtChars
}

func (m *Manager) Clear() {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.userReset = true
	m.lastSummarizeApplied = false
	oldTokens := m.tokenCountLocked()
	oldMsgCount := len(m.messages)
	if len(m.messages) > 0 && m.messages[0].Role == "system" {
		sys := m.messages[0]
		m.messages = []provider.Message{sys}
		m.version++
		m.nonTailMutSeq++
		m.tokens = m.countTokens(sys)
	} else {
		m.messages = nil
		m.version++
		m.nonTailMutSeq++
		m.tokens = 0
	}
	m.invalidateUsageBaselineLocked()
	debug.Log("ctx", "Clear: msgs %d→%d, tokens %d→%d (estimated), baseline invalidated",
		oldMsgCount, len(m.messages), oldTokens, m.tokenCountLocked())
}

func (m *Manager) UsageRatio() float64 {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.contextWindow <= 0 {
		return 0
	}
	return float64(m.tokenCountLocked()) / float64(m.contextWindow)
}

func (m *Manager) AutoCompactThreshold() int {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.autoCompactThresholdLocked()
}

func (m *Manager) SetToolDefinitionOverhead(tokens int) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.toolDefinitionOverhead = tokens
}

func (m *Manager) PromptBudget() int {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.usablePromptBudgetLocked()
}

func (m *Manager) Summarize(ctx context.Context, prov provider.Provider) error {
	plan, ok := m.buildSummaryPlan()
	if !ok {
		debug.Log("ctx", "Summarize: no plan built, nothing to summarize")
		m.mu.Lock()
		m.lastSummarizeApplied = false
		m.mu.Unlock()
		return nil
	}

	debug.Log("ctx", "Summarize: old_msgs=%d has_system=%t", len(plan.oldMsgs), plan.hasSystem)

	trigger := ""
	if len(plan.recentMsgs) == 0 {
		trigger = lastUserMessageText(plan.oldMsgs)
	}
	summaryText, err := summarizeMessages(ctx, prov, plan.oldMsgs, m.onUsage, m.summaryReserveTokens(), trigger)
	if err != nil {
		debug.Log("ctx", "Summarize: summarizeMessages FAILED: %v", err)
		return err
	}
	debug.Log("ctx", "Summarize: summary generated, len=%d chars limit=%d", len(summaryText), m.summaryReserveTokens())

	stateText := m.buildPostCompactState(plan.allMsgs)

	m.mu.Lock()
	if m.nonTailMutSeq != plan.origVersion {
		m.lastSummarizeApplied = false
		m.mu.Unlock()
		debug.Log("ctx", "Summarize: non-tail mutation during LLM window (seq %d→%d), discarding stale snapshot",
			plan.origVersion, m.nonTailMutSeq)
		return nil
	}
	oldTokens := m.tokenCountLocked()
	var extraMsgs []provider.Message
	if len(m.messages) > plan.origLen {
		extraMsgs = make([]provider.Message, len(m.messages)-plan.origLen)
		copy(extraMsgs, m.messages[plan.origLen:])
	}

	newMsgs := make([]provider.Message, 0, len(extraMsgs)+2)
	if plan.hasSystem {
		if len(m.messages) > 0 && m.messages[0].Role == "system" {
			newMsgs = append(newMsgs, m.messages[0])
		} else {
			newMsgs = append(newMsgs, plan.systemMsg)
		}
	}
	summaryMsg := provider.Message{
		ID:   newMessageID(),
		Role: "system",
		Content: []provider.ContentBlock{{
			Type: "text",
			Text: fmt.Sprintf("[Previous conversation summary]\n%s", summaryText),
		}},
	}
	newMsgs = append(newMsgs, summaryMsg)
	if stateText != "" {
		stateMsg := provider.Message{
			ID:   newMessageID(),
			Role: "system",
			Content: []provider.ContentBlock{{
				Type: "text",
				Text: stateText,
			}},
		}
		newMsgs = append(newMsgs, stateMsg)
	}
	newMsgs = append(newMsgs, plan.recentMsgs...)
	newMsgs = append(newMsgs, extraMsgs...)

	oldLen := len(m.messages)
	m.messages = newMsgs
	m.lastSummarizeApplied = true
	m.injectPinnedAfterCompaction()
	m.injectPostCompactNoteAfterCompaction()
	m.version++
	m.nonTailMutSeq++
	m.recalcTokens()
	debug.Log("ctx", "Summarize: msgs=%d→%d oldTokens=%d newTokens=%d summaryChars=%d summaryEstimatedTokens=%d extraMsgs=%d stateTextChars=%d",
		oldLen, len(newMsgs), oldTokens, m.tokenCountLocked(), len(summaryText), EstimateTokens(summaryText), len(extraMsgs), len(stateText))
	m.mu.Unlock()
	return nil
}

func (m *Manager) CheckAndSummarize(ctx context.Context, prov provider.Provider) (bool, error) {
	debug.Log("ctx", "CheckAndSummarize: tokens=%d contextWindow=%d threshold=%d ratio=%.2f", m.TokenCount(), m.ContextWindow(), m.AutoCompactThreshold(), m.UsageRatio())

	err := m.Summarize(ctx, prov)
	if err != nil {
		debug.Log("ctx", "CheckAndSummarize: Summarize FAILED: %v", err)
		return false, err
	}
	m.mu.Lock()
	summaryChanged := m.lastSummarizeApplied
	m.mu.Unlock()
	debug.Log("ctx", "CheckAndSummarize: done tokens=%d msgs=%d changed=%t",
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
	m.markBenignRemoval()
	m.version++
	m.nonTailMutSeq++
	m.recalcTokens()
	return true
}

func isToolResultCarrier(msg provider.Message) bool {
	hasToolResult := false
	for _, b := range msg.Content {
		if b.Type == "tool_result" {
			hasToolResult = true
			continue
		}
		if b.Type == "text" && strings.TrimSpace(b.Text) != "" {
			return false
		}
	}
	return hasToolResult
}

func (m *Manager) RemoveLastUserMessage() bool {
	m.mu.Lock()
	defer m.mu.Unlock()
	for i := len(m.messages) - 1; i >= 0; i-- {
		if m.messages[i].Role != "user" {
			continue
		}
		if isToolResultCarrier(m.messages[i]) {
			continue
		}
		m.messages = append(m.messages[:i], m.messages[i+1:]...)
		return true
	}
	return false
}

func (m *Manager) RemoveLastAssistantGroup() string {
	m.mu.Lock()
	defer m.mu.Unlock()
	if len(m.messages) == 0 {
		return ""
	}
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
	lastUserIdx := -1
	for i := lastAsstIdx - 1; i >= 0; i-- {
		if m.messages[i].Role != "user" {
			continue
		}
		if isToolResultCarrier(m.messages[i]) {
			continue
		}
		lastUserIdx = i
		break
	}
	if lastUserIdx < 0 {
		return ""
	}
	userText := ""
	for _, b := range m.messages[lastUserIdx].Content {
		if b.Type == "text" && b.Text != "" {
			userText = b.Text
			break
		}
	}
	if userText == "" {
		return ""
	}
	removed := len(m.messages) - lastUserIdx - 1
	m.messages = m.messages[:lastUserIdx+1]
	m.markBenignRemoval()
	m.version++
	m.nonTailMutSeq++
	m.recalcTokens()
	debug.Log("ctx", "RemoveLastAssistantGroup: removed %d messages from index %d, remaining=%d tokens=%d",
		removed, lastAsstIdx, len(m.messages), m.tokenCountLocked())
	return userText
}

func (m *Manager) recalcTokens() {
	m.tokens = 0
	for _, msg := range m.messages {
		m.tokens += m.countTokens(msg)
	}
	m.invalidateUsageBaselineLocked()
}

const reasoningCompactMinLen = 200

func headRunesPlain(s string, maxBytes int) string {
	if maxBytes <= 0 {
		return ""
	}
	if len(s) <= maxBytes {
		return s
	}
	cut := maxBytes
	for cut > 0 && !utf8.RuneStart(s[cut]) {
		cut--
	}
	return s[:cut]
}

func (m *Manager) CompactOldReasoningBlocks() int {
	m.mu.Lock()
	defer m.mu.Unlock()

	if len(m.messages) == 0 {
		return 0
	}

	lastAssistantIdx := -1
	for i := len(m.messages) - 1; i >= 0; i-- {
		if m.messages[i].Role == "assistant" {
			lastAssistantIdx = i
			break
		}
	}
	if lastAssistantIdx < 0 {
		return 0
	}

	type target struct {
		msgIdx  int
		blkIdx  int
		origLen int
	}
	var targets []target

	for i := 0; i < lastAssistantIdx; i++ {
		if m.messages[i].Role != "assistant" {
			continue
		}
		for j, b := range m.messages[i].Content {
			if b.ReasoningContent == "" {
				continue
			}
			if len(b.ReasoningContent) < reasoningCompactMinLen {
				continue
			}
			if strings.HasPrefix(b.ReasoningContent, "[compacted:") {
				continue
			}
			targets = append(targets, target{msgIdx: i, blkIdx: j, origLen: len(b.ReasoningContent)})
		}
	}

	if len(targets) == 0 {
		return 0
	}

	for _, t := range targets {
		block := &m.messages[t.msgIdx].Content[t.blkIdx]
		block.ReasoningContent = fmt.Sprintf("[compacted: original %d chars]", t.origLen)
		if block.ThinkingSignature != "" {
			block.ThinkingSignature = ""
		}
	}

	freed := m.commitMechanicalCompaction()
	debug.Log("ctx", "CompactOldReasoningBlocks: compacted %d reasoning blocks, freed ~%d tokens", len(targets), freed)
	return freed
}

func (m *Manager) CompactSupersededReads() int {
	m.mu.Lock()
	defer m.mu.Unlock()

	pathToToolIDs := make(map[string][]string)
	toolIDToPaths := make(map[string]map[string]bool)
	toolIDToRange := make(map[string]readRange)
	for _, msg := range m.messages {
		for _, b := range msg.Content {
			if b.Type != "tool_use" {
				continue
			}
			read := extractReadCall(b.ToolName, b.Input)
			for _, p := range read.paths {
				norm := normalizeFilePath(p)
				pathToToolIDs[norm] = append(pathToToolIDs[norm], b.ToolID)
				if toolIDToPaths[b.ToolID] == nil {
					toolIDToPaths[b.ToolID] = make(map[string]bool)
				}
				toolIDToPaths[b.ToolID][norm] = true
			}
			if len(read.paths) > 0 {
				toolIDToRange[b.ToolID] = read.rng
			}
		}
	}

	partiallySuperseded := make(map[string]bool)
	for _, ids := range pathToToolIDs {
		if len(ids) < 2 {
			continue
		}
		for i, id := range ids[:len(ids)-1] {
			covered := false
			for _, laterID := range ids[i+1:] {
				if readCovers(toolIDToRange[laterID], toolIDToRange[id]) {
					covered = true
					break
				}
			}
			if covered {
				partiallySuperseded[id] = true
			}
		}
	}

	supersededIDs := make(map[string]bool)
	for id := range partiallySuperseded {
		allFilesSuperseded := true
		for p := range toolIDToPaths[id] {
			ids := pathToToolIDs[p]
			covered := false
			for i, x := range ids {
				if x != id {
					continue
				}
				for _, laterID := range ids[i+1:] {
					if readCovers(toolIDToRange[laterID], toolIDToRange[id]) {
						covered = true
						break
					}
				}
				break
			}
			if !covered {
				allFilesSuperseded = false
				break
			}
		}
		if allFilesSuperseded {
			supersededIDs[id] = true
		}
	}

	if len(supersededIDs) == 0 {
		return 0
	}

	freedChars, compacted := m.compactSupersededToolResults(supersededIDs,
		"file was re-read later in the conversation")
	if freedChars == 0 {
		return 0
	}

	freed := m.commitMechanicalCompaction()
	debug.Log("ctx", "CompactSupersededReads: compacted %d superseded file reads, freed ~%d tokens", compacted, freed)
	return freed
}

func (m *Manager) CompactSupersededCommands() int {
	m.mu.Lock()
	defer m.mu.Unlock()

	cmdToToolIDs := make(map[string][]string)
	for _, msg := range m.messages {
		for _, b := range msg.Content {
			if b.Type != "tool_use" {
				continue
			}
			cmd := extractCommand(b.ToolName, b.Input)
			if cmd == "" {
				continue
			}
			cmdToToolIDs[cmd] = append(cmdToToolIDs[cmd], b.ToolID)
		}
	}

	supersededIDs := make(map[string]bool)
	for _, ids := range cmdToToolIDs {
		for _, id := range ids[:len(ids)-1] {
			supersededIDs[id] = true
		}
	}
	if len(supersededIDs) == 0 {
		return 0
	}

	freedChars, compacted := m.compactSupersededToolResults(supersededIDs,
		"command was re-run later in the conversation")
	if freedChars == 0 {
		return 0
	}

	freed := m.commitMechanicalCompaction()
	debug.Log("ctx", "CompactSupersededCommands: compacted %d superseded command runs, freed ~%d tokens", compacted, freed)
	return freed
}

func (m *Manager) compactSupersededToolResults(supersededIDs map[string]bool, reason string) (freedChars, compacted int) {
	for i := range m.messages {
		for j := range m.messages[i].Content {
			b := &m.messages[i].Content[j]
			if b.Type != "tool_result" || !supersededIDs[b.ToolID] {
				continue
			}
			if strings.HasPrefix(b.Output, "[superseded:") || strings.HasPrefix(b.Output, "[cleared:") {
				continue
			}
			origLen := len(b.Output)
			if origLen < 200 {
				continue
			}
			m.archiveSupersededToolOutput(m.messages[i].Role, b.ToolID, reason, b.Output)
			b.Output = fmt.Sprintf("[superseded: %s, output was %d chars]", reason, origLen)
			b.Images = nil
			freedChars += origLen
			compacted++
		}
	}
	return freedChars, compacted
}

func (m *Manager) commitMechanicalCompaction() int {
	before := m.tokens
	m.markBenignRemoval()
	m.version++
	m.nonTailMutSeq++
	m.recalcTokens()
	return before - m.tokens
}

func extractCommand(toolName string, input json.RawMessage) string {
	if toolName != "run_command" || len(input) == 0 {
		return ""
	}
	var args struct {
		Command    string `json:"command"`
		WorkingDir string `json:"working_dir"`
	}
	if json.Unmarshal(input, &args) != nil {
		return ""
	}
	cmd := strings.TrimSpace(args.Command)
	if cmd == "" {
		return ""
	}
	if wd := strings.TrimSpace(args.WorkingDir); wd != "" {
		return wd + "\x00" + cmd
	}
	return cmd
}

type readRange struct {
	offset int64
	limit  int64
	full   bool
}

func readCovers(later, earlier readRange) bool {
	if later.full {
		return true
	}
	if earlier.full {
		return false
	}
	if earlier == (readRange{}) {
		return true
	}
	if earlier.limit <= 0 {
		return later.offset <= earlier.offset && later.limit <= 0
	}
	if later.limit <= 0 {
		return later.offset <= earlier.offset
	}
	return later.offset <= earlier.offset && later.offset+later.limit >= earlier.offset+earlier.limit
}

type readCall struct {
	paths []string
	rng   readRange
}

func extractReadCall(toolName string, input json.RawMessage) readCall {
	if len(input) == 0 {
		return readCall{}
	}
	switch toolName {
	case "read_file":
		var args struct {
			Path   string `json:"path"`
			Offset int64  `json:"offset"`
			Limit  int64  `json:"limit"`
		}
		if json.Unmarshal(input, &args) == nil && args.Path != "" {
			return readCall{
				paths: []string{args.Path},
				rng:   readRange{offset: args.Offset, limit: args.Limit, full: args.Offset == 0 && args.Limit <= 0},
			}
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
			return readCall{paths: paths, rng: readRange{full: true}}
		}
	}
	return readCall{}
}

func extractReadPaths(toolName string, input json.RawMessage) []string {
	return extractReadCall(toolName, input).paths
}

func normalizeFilePath(p string) string {
	p = strings.TrimSpace(p)
	p = strings.ReplaceAll(p, "\\", "/")
	for strings.HasPrefix(p, "./") {
		p = p[2:]
	}
	for strings.Contains(p, "//") {
		p = strings.ReplaceAll(p, "//", "/")
	}
	if runtime.GOOS != "linux" {
		return strings.ToLower(p)
	}
	return p
}

func (m *Manager) countTokens(msg provider.Message) int {
	if m.provider != nil {
		if m.providerCountChecked && !m.providerCountSupportsRPC {
			return m.estimateTokens(msg)
		}
		ctx, cancel := context.WithTimeout(context.Background(), tokenCountTimeout)
		defer cancel()
		start := time.Now()
		n, err := m.provider.CountTokens(ctx, []provider.Message{msg})
		elapsed := time.Since(start)
		if err == nil && n > 0 {
			if !m.providerCountChecked && elapsed < time.Millisecond {
				m.providerCountChecked = true
				m.providerCountSupportsRPC = false
				debug.Log("ctx", "countTokens: provider uses local estimation (elapsed=%v), caching fast path", elapsed)
			}
			return n
		} else if err != nil {
			debug.Log("ctx", "countTokens: provider.CountTokens failed (%v), falling back to heuristic (will retry)", err)
		}
	}
	return m.estimateTokens(msg)
}

func estimateTokensStandalone(msg provider.Message) int {
	m := &Manager{calibrator: NewTokenCalibrator()}
	return m.estimateTokens(msg)
}

func (m *Manager) estimateTokens(msg provider.Message) int {
	return m.estimateTokensCalibrated(msg)
}

func (m *Manager) estimateTokensCalibrated(msg provider.Message) int {
	var sb strings.Builder
	var imageCount int
	var toolCallCount int
	for _, b := range msg.Content {
		sb.WriteString(b.Text)
		sb.WriteString(b.ReasoningContent)
		sb.WriteString(b.ToolName)
		sb.WriteString(b.Output)
		sb.Write(b.Input)
		if b.Type == "image" || b.ImageData != "" {
			imageCount++
		}
		imageCount += len(b.Images)
		if b.Type == "tool_use" {
			toolCallCount++
		}
	}
	n := EstimateTokensCalibrated(sb.String(), m.calibrator)
	extra := imageCount - 1
	if extra < 0 {
		extra = 0
	}
	return n + messageStructuralTokens(toolCallCount, imageCount > 0) + 170*extra
}

func messageStructuralTokens(toolCallCount int, hasImage bool) int {
	n := 4 + toolCallCount*6
	if hasImage {
		n += 170
	}
	return n
}

type summaryPlan struct {
	hasSystem   bool
	systemMsg   provider.Message
	allMsgs     []provider.Message
	oldMsgs     []provider.Message
	recentMsgs  []provider.Message
	origLen     int
	origVersion int64
}

type messageGroup struct {
	start int
	end   int
}

func (m *Manager) buildSummaryPlan() (summaryPlan, bool) {
	m.mu.Lock()
	defer m.mu.Unlock()

	plan := summaryPlan{origLen: len(m.messages), origVersion: m.nonTailMutSeq}
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

	recentCount := 0
	maxRecentTokens := int(float64(m.contextWindow) * maxRecentGroupTokenRatio)
	if len(groups) > minRecentGroups {
		accumulatedTokens := 0
		for i := 0; i < minRecentGroups && i < len(groups); i++ {
			g := groups[len(groups)-1-i]
			groupTokens := estimateMessagesTokens(m.messages[g.start:g.end])
			if accumulatedTokens+groupTokens > maxRecentTokens {
				break
			}
			accumulatedTokens += groupTokens
			recentCount++
		}
		if recentCount == 0 {
			recentCount = 1
		}
	}

	if recentCount > 0 {
		splitIdx := groups[len(groups)-recentCount].start
		plan.oldMsgs = append([]provider.Message(nil), m.messages[start:splitIdx]...)
		plan.recentMsgs = append([]provider.Message(nil), m.messages[splitIdx:]...)
		debug.Log("ctx", "buildSummaryPlan: summarizing %d msgs, keeping %d recent msgs (%d groups, ~%d tokens, budget=%d)",
			len(plan.oldMsgs), len(plan.recentMsgs), recentCount, estimateMessagesTokens(plan.recentMsgs), maxRecentTokens)
	} else {
		plan.oldMsgs = append([]provider.Message(nil), m.messages[start:]...)
		plan.recentMsgs = nil
		debug.Log("ctx", "buildSummaryPlan: summarizing all %d messages (groups=%d, recent=0 budget_exceeded)", len(plan.oldMsgs), len(groups))
	}
	return plan, len(plan.oldMsgs) > 0
}

func estimateMessagesTokens(msgs []provider.Message) int {
	m := &Manager{calibrator: NewTokenCalibrator()}
	total := 0
	for _, msg := range msgs {
		total += m.estimateTokensCalibrated(msg)
	}
	return total
}

const triggerVerbatimMaxLen = 8000

func headRunes(s string, n int) string {
	runes := []rune(s)
	if len(runes) <= n {
		return s
	}
	return string(runes[:n]) + fmt.Sprintf("\n... (truncated, original %d runes)", len(runes))
}

func summarizeMessages(ctx context.Context, prov provider.Provider, msgs []provider.Message, onUsage func(provider.TokenUsage), summaryTokenLimit int, trigger string) (string, error) {
	current := append([]provider.Message(nil), msgs...)
	for attempt := 0; attempt <= maxPTLRetries; attempt++ {
		payload := buildSummaryPayload(current)
		if trigger != "" {
			payload = "=== TRIGGER MESSAGE VERBATIM (the live user request that triggered this compaction — reproduce it under ## User Requests, condensed only for the token budget) ===\n" + trigger + "\n\n" + payload
		}
		summaryMsgs := []provider.Message{
			{
				Role: "system",
				Content: []provider.ContentBlock{{
					Type: "text",
					Text: fmt.Sprintf(`You are summarizing a conversation between a user and an AI coding assistant (agentic coding tool). Produce a concise, structured summary that preserves the information most critical for continuing work autonomously.

Your summary must be under %d tokens. Be extremely concise — every token wasted on filler is a token lost to the agent's working memory.

Your summary MUST use these sections (skip a section only if empty):

## Task
What the user asked for and the current goal. One sentence.

## Done
What has been completed and VERIFIED (tests passed, builds succeeded, code committed). Mark each item with ✅. This prevents the agent from redoing finished work.

## In Progress
What is actively being worked on. Include the specific file, function, or component. If the agent was mid-edit when compaction triggered, state exactly what change was in flight.

## Next Steps
Concrete next actions the agent should take, in priority order.

## Key Files
File paths that were read, created, or modified, with a 1-3 word note on what each contains or why it matters. Example: "internal/context/manager.go — compaction logic, summarizeMessages()". Do NOT include full file contents.

## Decisions & Constraints
Architecture decisions, trade-offs, and USER CONSTRAINTS (e.g. "don't modify tests", "use library X", "follow pattern Y"). These MUST be preserved — losing a user constraint causes wrong behavior.

## Dead Ends
Approaches that were TRIED AND FAILED or ruled out, with the reason. Example: "tried using sync.Pool for buffers - caused data races on concurrent map access". This is critical negative knowledge: without it, the agent will repeat the same failed attempts after compaction, wasting iterations.

## Errors Resolved
Bugs found and fixed, with root cause (1 line each). Example: "nil pointer in swarm ReplyTo — added nil check".

## Background Tasks
Active background commands or spawned sub-agents with their IDs and purpose. These survive compaction but their context must be preserved here.

## User Requests
The verbatim user requests (provided at the top of the payload) — preserve near-verbatim.
When a TRIGGER MESSAGE VERBATIM section is present, it is the LIVE user request the agent must answer right after compaction: reproduce its full content in this section (condensed only if it exceeds the token budget), never reduce it to one sentence.

Omit entirely:
- Full source code (reference paths + key signatures only)
- Verbose command output (state the outcome in one phrase)
- Repeated status checks, confirmations, or idle chatter
- Tool call mechanics (focus on what was done, not which tool)`, summaryTokenLimit),
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

		if blocks := splitPayloadBlocks(payload, pccMinPayloadTokens); len(blocks) > 1 {
			if summaryText, ok := summarizeParallel(ctx, prov, blocks, summaryTokenLimit, onUsage); ok {
				return applyFactRetention(summaryText, payload, userConstraintSource(msgs)), nil
			}
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
		summaryText := ""
		for _, block := range resp.Message.Content {
			if block.Type == "text" && block.Text != "" {
				summaryText = block.Text
				break
			}
		}
		if summaryText == "" {
			return "", fmt.Errorf("summarization returned empty text")
		}
		debug.Log("ctx", "summarizeMessages: summary len=%d chars estimated=%d tokens limit=%d usage=%+v",
			len(summaryText), EstimateTokens(summaryText), summaryTokenLimit, resp.Usage)
		return applyFactRetention(summaryText, payload, userConstraintSource(msgs)), nil
	}
	return "", fmt.Errorf("summarization returned empty text")
}

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
	return m.tokens + m.toolDefinitionOverhead
}

// SetEarlyCompactRatio enables an earlier auto-compact trigger (#5002 gap 2):
// when r is in (0, 1), the effective auto-compact threshold becomes
// min(window-reserve, r*window). Single long-task sessions (sub-agents) grow
// monotonically with no user-turn pacing, so firing the pre-compact earlier
// keeps them off the reactive overflow path. r <= 0 disables the early
// trigger (default).
func (m *Manager) SetEarlyCompactRatio(r float64) {
	if r < 0 {
		r = 0
	}
	if r >= 1 {
		r = 0
	}
	m.mu.Lock()
	m.earlyCompactRatio = r
	m.lastLoggedThreshold = 0
	m.mu.Unlock()
}

// EarlyCompactRatio reports the configured early auto-compact trigger ratio
// (0 = disabled).
func (m *Manager) EarlyCompactRatio() float64 {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.earlyCompactRatio
}

func (m *Manager) autoCompactThresholdLocked() int {
	if m.contextWindow <= 0 {
		debug.Log("ctx", "autoCompactThreshold: contextWindow=0 → threshold=0")
		return 0
	}
	reserve := m.effectiveOutputReserveLocked()
	threshold := m.contextWindow - reserve
	if r := m.earlyCompactRatio; r > 0 && r < 1 {
		if early := int(float64(m.contextWindow) * r); early < threshold {
			threshold = early
		}
	}
	result := threshold
	if threshold < minSummaryReserve {
		result = minSummaryReserve
	}
	if m.lastLoggedThreshold != result {
		debug.Log("ctx", "autoCompactThreshold: contextWindow=%d outputReserve=%d earlyRatio=%.2f rawThreshold=%d finalThreshold=%d",
			m.contextWindow, reserve, m.earlyCompactRatio, threshold, result)
		m.lastLoggedThreshold = result
	}
	return result
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
		debug.Log("ctx", "effectiveOutputReserve: contextWindow=0 → reserve=0")
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
	if m.lastLoggedReserve != reserve {
		debug.Log("ctx", "effectiveOutputReserve: contextWindow=%d userReserve=%d floor=%d ceiling=%d effective=%d",
			m.contextWindow, m.outputReserve, floor, ceiling, reserve)
		m.lastLoggedReserve = reserve
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
		payloadToolResultMaxLen = 500
		payloadToolResultHead   = 200
		payloadToolInputMaxLen  = 300
		payloadUserMsgMaxLen    = 500
	)

	var sb strings.Builder

	userRequests := extractUserRequests(msgs, payloadUserMsgMaxLen)
	if len(userRequests) > 0 {
		sb.WriteString("=== VERBATIM USER REQUESTS (preserve these near-verbatim in your summary) ===\n")
		for i, req := range userRequests {
			sb.WriteString(fmt.Sprintf("%d. %s\n", i+1, req))
		}
		sb.WriteString("\n=== CONVERSATION LOG ===\n")
	}

	for _, msg := range msgs {
		sb.WriteString(fmt.Sprintf("[%s]\n", msg.Role))
		for _, block := range msg.Content {
			switch block.Type {
			case "text":
				sb.WriteString(block.Text)
				sb.WriteByte('\n')
			case "image":
				sb.WriteString(formatImageBlockForSummary(block))
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
					output = headRunesPlain(output, payloadToolResultHead) + fmt.Sprintf("\n... (truncated, original %d chars)", len(output))
				}
				sb.WriteString(fmt.Sprintf("Tool result: %s\n", output))
				if n := len(block.Images); n > 0 {
					sb.WriteString(fmt.Sprintf("(tool result included %d image(s); visual content is NOT preserved in this summary)\n", n))
				}
			}
		}
		sb.WriteByte('\n')
	}
	return sb.String()
}

func lastUserMessageText(msgs []provider.Message) string {
	for i := len(msgs) - 1; i >= 0; i-- {
		if msgs[i].Role != "user" {
			continue
		}
		for _, block := range msgs[i].Content {
			if block.Type == "text" && strings.TrimSpace(block.Text) != "" {
				return headRunes(block.Text, triggerVerbatimMaxLen)
			}
		}
	}
	return ""
}

func extractUserRequests(msgs []provider.Message, maxLen int) []string {
	var requests []string
	for _, msg := range msgs {
		if msg.Role != "user" {
			continue
		}
		for _, block := range msg.Content {
			if block.Type != "text" {
				continue
			}
			text := strings.TrimSpace(block.Text)
			if text == "" {
				continue
			}
			if len(text) > maxLen {
				text = headRunesPlain(text, maxLen-3) + "..."
			}
			requests = append(requests, text)
		}
	}
	return requests
}

func formatImageBlockForSummary(block provider.ContentBlock) string {
	mime := block.ImageMIME
	if mime == "" {
		mime = "unknown"
	}
	note := fmt.Sprintf("[IMAGE attached (%s", mime)
	if sizeKB := approxImageKB(block.ImageData); sizeKB > 0 {
		note += fmt.Sprintf(", ~%dKB", sizeKB)
	}
	note += ") — visual content is NOT preserved in this summary]"
	return note
}

func approxImageKB(b64 string) int {
	if len(b64) < 8 {
		return 0
	}
	decoded := len(b64) * 3 / 4
	if decoded <= 0 {
		return 0
	}
	return decoded / 1024
}

func formatToolInputForSummary(input []byte, maxLen int) string {
	if len(input) == 0 || maxLen <= 0 {
		return ""
	}
	var m map[string]any
	if err := json.Unmarshal(input, &m); err != nil {
		return ""
	}
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
		if len([]rune(s)) > 80 {
			s = string([]rune(s)[:77]) + "..."
		}
		parts = append(parts, fmt.Sprintf("%s=%q", key, s))
		delete(m, key)
	}
	for k, v := range m {
		if len(parts) >= 4 {
			break
		}
		s, ok := v.(string)
		if !ok {
			continue
		}
		s = strings.ReplaceAll(s, "\n", " ")
		if len([]rune(s)) > 60 {
			s = string([]rune(s)[:57]) + "..."
		}
		if s == "" {
			continue
		}
		parts = append(parts, fmt.Sprintf("%s=%q", k, s))
	}
	result := strings.Join(parts, ", ")
	if len(result) > maxLen {
		result = headRunesPlain(result, maxLen-3) + "..."
	}
	return result
}

func truncateGroupsForPTLRetry(msgs []provider.Message) ([]provider.Message, bool) {
	if len(msgs) < 2 {
		return nil, false
	}
	start := 0
	if msgs[0].Role == "system" {
		start = 1
	}
	groups := buildMessageGroups(msgs, start)
	for _, g := range groups {
		head := g.start
		if startsNewInteractionGroup(msgs[g.start]) {
			head++
		}
		if head < g.end {
			out := append([]provider.Message(nil), msgs[:head]...)
			return append(out, msgs[head+1:]...), true
		}
	}
	return nil, false
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
		return ""
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
