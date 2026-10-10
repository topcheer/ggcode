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

// newMessageID generates a unique message identifier: "msg_" + UUID.
func newMessageID() string {
	return "msg_" + uuid.New().String()
}

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
	// SummaryMsgID returns the ID of the most recent summary message, or "".
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
	Clear()
	UsageRatio() float64
	AutoCompactThreshold() int
	// SetToolDefinitionOverhead tells the manager how many tokens the tool
	// definitions (schemas + names + descriptions) consume. This is added to
	// the dynamic prompt overhead when calculating AutoCompactThreshold.
	SetToolDefinitionOverhead(tokens int)
	ReconcileToolCalls() bool
	// Pinned returns the pinned-context store for user-pinned items that
	// survive context compaction. Returns nil if pinning is not supported.
	Pinned() *PinnedContext
}

// CompactSnapshot is an immutable point-in-time view used by background
// compaction. It lets callers summarize a stable copy without mutating the live
// conversation while an LLM turn may still be running.
type CompactSnapshot struct {
	Messages      []provider.Message
	OrigLen       int
	LastMsgID     string // ID of the last message in Messages, for ApplyCompactResult positioning
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

// CompactRejectReason categorizes why ApplyCompactResult refused a result.
// The agent uses it to attribute refunds correctly (#663): only user-driven
// context resets (clear/rewind/another compaction replacing the context)
// justify refunding the precompact cooldown — the live tokens that triggered
// the summarization are GONE, so a fresh schedule would summarize a context
// that no longer needs it. Benign internal cleanup (retry truncation via
// RemoveLastAssistantGroup, orphan tool-result removal) removes a few tail
// messages with no semantic loss; the live context is still essentially the
// one that warranted compaction, so the cooldown must STAY to prevent
// rescheduling a redundant full-context summarization every turn.
type CompactRejectReason int

const (
	// CompactRejectNone: the result was applied (no rejection).
	CompactRejectNone CompactRejectReason = iota
	// CompactRejectNoChange: summarization produced no change (real failure).
	CompactRejectNoChange
	// CompactRejectEmpty: summarization produced an empty result (real failure).
	CompactRejectEmpty
	// CompactRejectUserReset: live context was user-driven reset (clear,
	// rewind, another compaction) — messages removed with semantic loss.
	CompactRejectUserReset
	// CompactRejectBenignTrim: live context lost a few tail messages to benign
	// internal cleanup (retry truncation, orphan tool-result removal).
	CompactRejectBenignTrim
)

// String implements fmt.Stringer for debug logs.
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
	// Summary output cap: 5% of contextWindow, but capped at a fixed
	// absolute maximum. For 200K context → 10K; for 1M context → 12K (not 50K).
	maxSummaryOutputRatio  = 0.05
	maxSummaryOutputTokens = 12000

	defaultOutputReserveRatio = 0.10
	maxOutputReserveRatio     = 0.25
	safetyMarginRatio         = 0.05
	minRecentGroups           = 1    // keep last interaction group verbatim (budget permitting)
	maxRecentGroupTokenRatio  = 0.15 // recent groups may occupy at most 15% of context window
	minSummaryReserve         = 64
	// uncoveredScriptFreezeShare is the uncovered-script rune share (Latin-
	// Extended/Cyrillic/Greek/other, priced by fixed tokenizer tiers) above
	// which a calibration sample is frozen (#623): such text's tokens are
	// estimated by tiers invisible to the ascii/cjk ratio loop, so a sample
	// dominated by them must not drive those ratios.
	uncoveredScriptFreezeShare = 0.20
	maxPTLRetries              = 3
	tokenCountTimeout          = 100 * time.Millisecond
)

// Manager implements ContextManager.
type Manager struct {
	mu                       sync.Mutex
	messages                 []provider.Message
	version                  int64              // incremented on every mutation, enables cheap change detection
	nonTailMutSeq            int64              // #479: bumped ONLY by non-tail mutations (compaction/clear/truncate/mid-insert) — NOT by Add; Summarize's TOCTOU guard
	runAdded                 []provider.Message // messages added via Add() since last StartRunTracking()
	runAddedIDs              map[string]bool    // IDs of messages in runAdded, for dedup
	tokens                   int
	contextWindow            int
	outputReserve            int
	baselineTokens           int
	baselineDelta            int
	baselineAvailable        bool
	provider                 provider.Provider
	providerCountChecked     bool // whether providerCountSupportsRPC has been determined
	providerCountSupportsRPC bool // cached: does provider.CountTokens do real RPC?
	todoPath                 string
	onUsage                  func(provider.TokenUsage)
	calibrator               *TokenCalibrator
	onPersist                func(msg provider.Message) // called on every Add() for real-time JSONL persistence
	toolDefinitionOverhead   int                        // tokens reserved for tool definitions (set by Agent)
	pinned                   *PinnedContext             // user-pinned context that survives compaction
	postCompactNoteFn        func() string              // optional: non-empty return is re-injected as a system note after every compaction
	lastLoggedReserve        int                        // last logged effectiveOutputReserve value (suppress duplicate logs)
	lastLoggedThreshold      int                        // last logged autoCompactThreshold value (suppress duplicate logs)
	// #663: attribution for message removals. When ApplyCompactResult rejects
	// a live-shrunk result, the agent must distinguish a USER-DRIVEN reset
	// (Clear, rewind, another compaction — semantic loss, cooldown refund OK)
	// from BENIGN internal cleanup (retry truncation, orphan tool-result
	// removal — no semantic loss, refund would reschedule a redundant
	// full-context summarization).
	benignRemoval bool                // a benign (non-semantic) removal happened since last snapshot
	userReset     bool                // a user-driven reset (Clear/rewind) happened since last snapshot
	lastReject    CompactRejectReason // reason of the most recent ApplyCompactResult call
	// lastSummarizeApplied records whether the most recent Summarize call
	// actually replaced the conversation (#702b: m.version also bumps on Add,
	// so CheckAndSummarize must not infer "applied" from a version delta).
	lastSummarizeApplied bool
}

// NewManager creates a ContextManager with the given context window limit.
func NewManager(contextWindow int) *Manager {
	return &Manager{
		contextWindow: contextWindow,
		calibrator:    NewTokenCalibrator(),
		pinned:        newPinnedContext(),
	}
}

// markBenignRemoval records that a message removal happened via a benign
// internal-cleanup path (no semantic content loss). Caller must hold m.mu.
func (m *Manager) markBenignRemoval() {
	m.benignRemoval = true
}

// LastCompactRejectReason returns the rejection reason recorded by the most
// recent ApplyCompactResult call (#663). CompactRejectNone means the last
// result was applied (or no call happened yet).
func (m *Manager) LastCompactRejectReason() CompactRejectReason {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.lastReject
}

// Pinned returns the PinnedContext store for user-pinned context items.
// Pinned items survive compaction and are re-injected after each summary.
func (m *Manager) Pinned() *PinnedContext {
	return m.pinned
}

// injectPinnedAfterCompaction inserts a system message containing all pinned
// context items right after the compaction summary message. This is called
// after ApplyCompactResult sets the new message list. If a pinned message
// already exists (from a previous compaction cycle), it is replaced.
//
// Must be called with m.mu held.
func (m *Manager) injectPinnedAfterCompaction() {
	// Always remove any stale pinned message from a previous compaction cycle.
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

	// Insert right after the compaction summary, or after the first system
	// message as a fallback.
	insertIdx := m.findSystemMessageIdx("[Previous conversation summary]")
	if insertIdx >= 0 {
		insertIdx++ // after the summary
	} else if len(m.messages) > 0 && m.messages[0].Role == "system" {
		insertIdx = 1
	} else {
		insertIdx = 0
	}

	// Insert the pinned message at the insertion point.
	m.messages = append(m.messages, provider.Message{})
	copy(m.messages[insertIdx+1:], m.messages[insertIdx:])
	m.messages[insertIdx] = pinnedMsg

	debug.Log("ctx", "injectPinnedAfterCompaction: injected %d pinned items at position %d", len(m.pinned.List()), insertIdx)
}

// SetPostCompactNoteProvider registers an optional callback invoked after
// every successful compaction (both the ApplyCompactResult and the direct
// Summarize paths). Its non-empty return value is injected as a durable
// system note right after the compaction summary, mirroring the
// pinned-context contract: state the model must see after compaction is
// re-materialized rather than entrusted to the summary. A nil provider
// (the default) or an empty return disables the note. Used by the agent to
// rehydrate the live task board after compaction.
func (m *Manager) SetPostCompactNoteProvider(fn func() string) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.postCompactNoteFn = fn
}

// postCompactNoteMarker identifies the injected state note so a stale copy
// from a previous compaction cycle can be replaced.
const postCompactNoteMarker = "[Session State Note - refreshed after compaction]"

// injectPostCompactNoteAfterCompaction re-materializes the registered
// post-compaction note (e.g. the live task board) right after the summary.
// Without it, structured task state lives only in tool_results that the
// summary compresses away, leaving the model with no signal that pending
// work exists or that task IDs remain usable.
//
// Must be called with m.mu held.
func (m *Manager) injectPostCompactNoteAfterCompaction() {
	// Replace any stale note from a previous compaction cycle.
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

	// Insert right after the compaction summary (same positioning as the
	// pinned-context message), or after the first system message as fallback.
	insertIdx := m.findSystemMessageIdx("[Previous conversation summary]")
	if insertIdx >= 0 {
		insertIdx++ // after the summary
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

// findSystemMessageIdx returns the index of the first system message whose
// text content contains the given marker, or -1 if not found.
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

// removeSystemMessageByMarker removes the first system message containing the
// given marker string. No-op if not found.
func (m *Manager) removeSystemMessageByMarker(marker string) {
	idx := m.findSystemMessageIdx(marker)
	if idx >= 0 {
		m.messages = append(m.messages[:idx], m.messages[idx+1:]...)
		m.markBenignRemoval() // #663: marker housekeeping — no semantic loss
	}
}

// SetPersistHandler sets a callback invoked on every Add() for real-time
// JSONL persistence. The callback receives the message that was just added.
// This enables per-message append writes instead of batch writes at run end.
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

// SetProvider sets the provider for provider-aware token counting.
func (m *Manager) SetProvider(p provider.Provider) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.provider = p
	// Reset countTokens cache — new provider may have different RPC capability.
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
	// Track for run persistence — dedup by ID.
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
	// Trigger real-time persistence callback outside the lock.
	if persistFn != nil {
		persistFn(msg)
	}
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

	fixes := m.collectReconcileFixes()
	if len(fixes) == 0 {
		return false
	}
	return m.applyReconcileFixes(fixes)
}

// reconcileLateResult locates a tool_result block that arrived after the
// next assistant message (#535 granularity is block-level).
type reconcileLateResult struct {
	msgIdx   int
	blockIdx int // position of the block inside m.messages[msgIdx].Content (#535)
	block    provider.ContentBlock
}

// reconcileID identifies a tool_use that has neither a properly-placed nor a
// late tool_result and therefore needs a cancelled placeholder.
type reconcileID struct{ id, name string }

// reconcileNeedFix describes one assistant message whose tool_results need to
// be relocated (late) or synthesized (missing) before the next assistant msg.
type reconcileNeedFix struct {
	insertBefore int
	lateBlocks   []reconcileLateResult
	missingIDs   []reconcileID
}

// collectReconcileFixes scans the message history for assistant tool_use
// messages whose tool_results are missing or placed after the next assistant
// message. Callers must hold m.mu.
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

// nextAssistantIndex returns the index of the first assistant message after
// start, or len(m.messages) if there is none.
func (m *Manager) nextAssistantIndex(start int) int {
	for j := start + 1; j < len(m.messages); j++ {
		if m.messages[j].Role == "assistant" {
			return j
		}
	}
	return len(m.messages)
}

// findLateOrMissingResults partitions unresolved tool IDs into those whose
// tool_result appears after boundaryIdx (late) and those with no result at
// all (missing).
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

// applyReconcileFixes rebuilds the message history, relocating late
// tool_results before their assistant boundary, dropping the stale blocks in
// place (preserving sibling user content, #535), and inserting cancelled
// placeholders for missing results. Callers must hold m.mu. Returns true.
func (m *Manager) applyReconcileFixes(fixes []reconcileNeedFix) bool {
	oldMsgs := m.messages

	// Stale granularity is BLOCK-level, not message-level (#535): a late
	// tool_result usually shares its user message with genuine user content
	// (text typed while the tool was running, a pasted screenshot). Dropping
	// the whole message silently destroyed that content — text/image blocks
	// carry no ToolID, so they never entered the reinsertion whitelist below.
	staleBlockIdxs := make(map[int]map[int]bool) // msgIdx → blockIdx set
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
	m.markBenignRemoval() // #663: reconcile rebuild — bookkeeping, no semantic loss
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

// reconcileInsertion is a user message (relocated tool_results plus
// cancelled-tool placeholders) to be inserted before a given message index.
type reconcileInsertion struct {
	insertBefore int
	msg          provider.Message
}

// rebuildReconciledMessages applies the phase-2 rebuild of ReconcileToolCalls:
// insertions are placed before their target index, and stale tool_result
// blocks (staleBlockIdxs: msgIdx → blockIdx set) are stripped from their
// messages — keeping every other block of those messages (user text/images,
// #535). Returns the rebuilt slice, the number of messages dropped because
// they contained ONLY stale blocks, and the total number of stale blocks
// removed.
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
			droppedMsgs++ // message was nothing but late tool_results
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

// stripStaleBlocks removes the given stale block indices from msg.Content.
// An empty/nil stale set is a no-op. emptied reports that nothing survived
// (the message consisted solely of relocated tool_result blocks).
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
				allOrphan = false // any non-tool_result block has content
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
		m.markBenignRemoval() // #663: orphan cleanup — no semantic loss
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
	m.nonTailMutSeq++
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
	m.runAddedIDs = nil
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

// SummaryMsgID returns the ID of the most recent summary message in context,
// or empty string if no summary exists. Used by checkpoint to record which
// message to start restoring from.
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
	// #663: a new snapshot opens a fresh attribution window — removal markers
	// now answer "what kind of removal happened during THIS compaction window?".
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

	// Force summarization — the caller (precompact) already determined
	// that compaction is needed based on the LIVE token count (calibrated
	// from actual LLM API usage). The scratch manager's estimated token
	// count (via recalcTokens) may be significantly lower than the live
	// count, causing CheckAndSummarize to skip summarization and produce
	// a Changed=false result that ApplyCompactResult rejects.
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

	// Find the last message from the snapshot in live messages using its ID.
	// Everything after that message is "extra" (arrived during compaction)
	// and must be preserved. Everything before gets replaced by the summary.
	extraStart := 0
	anchorMissing := false
	if snapshot.LastMsgID != "" {
		// Scan for the last snapshot message by ID.
		foundIdx := -1
		for i, msg := range m.messages {
			if msg.ID == snapshot.LastMsgID {
				foundIdx = i
			}
		}
		if foundIdx >= 0 {
			extraStart = foundIdx + 1
		} else {
			// #718: LastMsgID not found — messages were removed during the
			// compaction window (rewind/clear/another compaction). The old
			// OrigLen fallback silently replaced every live message at index
			// >= OrigLen whenever removals were balanced by appends (equal
			// length passes the shrink guard below), so messages that arrived
			// during the window were swallowed by the lossy summary. The
			// anchor ID is the ONLY reliable way to tell snapshot messages
			// apart from window arrivals; when it is gone, refuse to apply.
			// Both old fallbacks (OrigLen / len(messages)) fed the same
			// silent-loss path, so they are removed entirely.
			anchorMissing = true
		}
	} else {
		// No LastMsgID (old snapshot) — fall back to OrigLen.
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
		// #718: refusing to apply is mandatory (we cannot tell snapshot
		// messages from window arrivals), but the CLASSIFICATION must reuse
		// the #663 classifier so the agent-side cooldown semantics stay
		// intact: benignRemoval (orphan cleanup, retry truncation) keeps the
		// cooldown (BenignTrim) — the live context is still essentially the
		// one that warranted compaction; userReset (Clear/rewind/another
		// compaction) refunds it (UserReset); unknown defaults to UserReset,
		// the pre-#663 safe direction. Orphan cleanup can remove the anchor
		// message itself (zz_issue702_test.go), which is why this branch —
		// not just the length guard — needs the classifier.
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

	// #651: real live-shrunk rejection. The live message list shrank below
	// the snapshot size only when messages were REMOVED during the compaction
	// window (/clear, checkpoint rewind, another compaction) — the lossy
	// summary assumed those messages exist and applying it on top of a
	// rewound context would resurrect dropped content. The LastMsgID scan
	// above already signalled this (LastMsgID absent + OrigLen > live);
	// previously the code only logged and applied anyway, which made the
	// agent-side liveShrunk refund branch (#612/#633) unreachable outside
	// mocks. len(extraStart) growth (messages appended) is unaffected.
	if len(m.messages) < len(snapshot.Messages) {
		// #663: classify the shrink. userReset (Clear/rewind/another
		// compaction) is a semantic reset — the agent may refund the precompact
		// cooldown because the context that triggered compaction no longer
		// exists. benignRemoval (retry truncation, orphan cleanup, reconcile
		// rebuild) is bookkeeping — the live context is still essentially the
		// one that warranted compaction, so the cooldown MUST stay, otherwise
		// maybeAutoCompact reschedules a redundant full-context summarization
		// every turn (retry storms amplified this into repeated wasted LLM
		// calls). Unknown (neither flag set — e.g. a Manager subclass removing
		// messages some other way) defaults to userReset: refunding is the
		// pre-#663 behavior and the safe direction for #612/#651 compat.
		if m.userReset {
			m.lastReject = CompactRejectUserReset
		} else if m.benignRemoval {
			m.lastReject = CompactRejectBenignTrim
		} else {
			m.lastReject = CompactRejectUserReset
		}
		debug.Log("ctx", "ApplyCompactResult: REJECT live-shrunk (%s): liveMsgs=%d < snapshotMsgs=%d (messages removed during compaction window)",
			m.lastReject, len(m.messages), len(snapshot.Messages))
		// Unlock before returning — this early return sat between Lock() and
		// the function's tail Unlock() and leaked m.mu forever, deadlocking
		// every later Manager call (Messages/TokenCount/next compaction).
		n := m.tokenCountLocked()
		m.mu.Unlock()
		return false, n
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

	// Messages appended after the marker (or after OrigLen in fallback) are
	// preserved as-is.
	extra := append([]provider.Message(nil), m.messages[extraStart:]...)
	newMsgs := append([]provider.Message(nil), result.Messages...)
	newMsgs = append(newMsgs, extra...)

	if hasLiveSystem && len(newMsgs) > 0 {
		// Identify the summary message by its marker, not by role.
		// Summarize outputs role=system, so checking role would
		// overwrite the actual summary with the live system prompt.
		for i := range newMsgs {
			if newMsgs[i].Role == "system" && len(newMsgs[i].Content) > 0 &&
				newMsgs[i].Content[0].Type == "text" &&
				strings.Contains(newMsgs[i].Content[0].Text, "[Previous conversation summary]") {
				// Preserve summary, only replace if it's a bare system prompt.
				continue
			}
			if newMsgs[i].Role == "system" {
				newMsgs[i] = liveSystem
				break
			}
		}
	}

	m.messages = newMsgs

	// Inject pinned context after compaction. Pinned items survive
	// compaction by being re-inserted as a system message right after the
	// summary. This ensures critical context (build flags, constraints, etc.)
	// is never lost during context summarization.
	m.injectPinnedAfterCompaction()
	// Re-materialize the registered post-compaction state note (task board
	// rehydration) - same "survives compaction" contract as pinned context.
	m.injectPostCompactNoteAfterCompaction()

	m.version++
	m.nonTailMutSeq++
	m.recalcTokens()
	liveTokensAfter := m.tokenCountLocked()

	// The compaction summary message must be persisted to JSONL immediately
	// so that checkpoint restore can find it by ID even if the run is
	// interrupted. We add it to runAdded AND trigger onPersist directly.
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
			break // only one summary per compaction
		}
	}

	debug.Log("ctx", "ApplyCompactResult: APPLIED liveTokensBefore=%d liveTokensAfter=%d snapshotMsgs=%d compacted=%d extra=%d resultTokenCount=%d",
		liveTokensBefore, liveTokensAfter, snapshot.OrigLen, len(result.Messages), len(extra), result.TokenCount)
	m.lastReject = CompactRejectNone // #663: successful apply clears the reject reason

	// Trigger onPersist outside the lock for the summary message.
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

// OutputReserve returns the raw (user-configured) output reserve value.
func (m *Manager) OutputReserve() int {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.outputReserve
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
	if usage.InputTokens <= 0 && usage.OutputTokens <= 0 {
		return
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	// Guard: if InputTokens is 0 (provider fallback didn't fill it in),
	// do NOT overwrite the baseline — otherwise context usage collapses
	// to just OutputTokens, showing an absurdly small number in the TUI.
	if usage.InputTokens <= 0 {
		debug.Log("ctx", "RecordUsage: skipping baseline update — InputTokens=%d OutputTokens=%d (missing input tokens)",
			usage.InputTokens, usage.OutputTokens)
		return
	}
	oldBaseline := m.baselineTokens
	// baselineTokens represents the starting point for the NEXT request's
	// input token count. The next request will include the previous turn's
	// output (assistant response) as part of its input, so we add OutputTokens
	// to get the correct next-request input baseline.
	//
	// CacheRead semantics differ by provider protocol:
	//   - Anthropic: InputTokens is NON-cached only; CacheRead is ADDITIVE.
	//     Total input = InputTokens + CacheRead.
	//   - OpenAI/Gemini: InputTokens ALREADY includes cached tokens;
	//     CacheRead is an informational subset (PromptTokensTotal == InputTokens).
	//
	// We detect the Anthropic case by checking whether CacheRead > 0 AND
	// InputTokens < PromptTokensTotal (meaning InputTokens is not the full
	// prompt). This avoids double-counting for OpenAI/Gemini.
	totalInput := usage.InputTokens
	if usage.CacheRead > 0 && usage.PromptTokensTotal > usage.InputTokens {
		// Anthropic semantics: PromptTokensTotal includes InputTokens +
		// CacheRead + CacheWrite. Use it directly for accurate baseline.
		totalInput = usage.PromptTokensTotal
	}
	m.baselineTokens = totalInput + usage.OutputTokens
	m.baselineDelta = 0
	m.baselineAvailable = true
	// Feed calibration sample: compare our estimate with actual API input tokens.
	// Use totalInput (including cache read) for accurate calibration.
	// The estimated side must include toolDefinitionOverhead to match the
	// actual side's composition — totalInput necessarily contains tool
	// schemas + system prompt; a content-only estimate made factor < 1 on
	// every sample, pushing asciiRatio into its clamp and double-counting
	// the overhead in tokenCountLocked afterwards (#383). Do NOT use
	// tokenCountLocked() here: the baseline was just overwritten by this
	// very usage, which would make the comparison circular.
	asciiChars, cjkChars, latinExtChars := m.compositionLocked()
	// #605 G3: compositionLocked counts only ASCII and true CJK (#598). A
	// pure-Cyrillic/Greek session returns (0,0) — RecordSample would fall
	// back to its ASCII-only assumption (asciiShare=1.0) and drive asciiRatio
	// with samples the estimation side prices via fixed per-script tiers
	// (tokenizer.go), structurally mis-calibrating ASCII while the actual
	// scripts stay invisible to the ratio loop. Freeze instead: skip the
	// sample entirely rather than mis-attribute it.
	// #623: the (0,0) check alone was near-dead code — scriptTokenClasses
	// counts spaces and punctuation as ASCII, so ANY non-CJK prose with a
	// single space returned asciiChars>0 and bypassed the freeze. Cyrillic
	// prose with space-derived "ASCII" then fed RecordSample at
	// asciiShare=1.0 and drove asciiRatio 3.5→3.0 (clamp) — #598's pollution
	// surviving from a second entry point. Freeze by script share instead:
	// when uncovered-script runes dominate the content, skip the sample.
	// #634: latinExt is a COVERED script for the freeze — the estimation
	// side prices it via its own tier (3.0 chars/token, close to ASCII's
	// 3.5), and counting it uncovered froze every sample of Vietnamese-style
	// sessions (>20% non-ASCII Latin), so asciiRatio never calibrated at
	// all — regressing the #598 drift family the freeze was built to stop.
	// latinExt still does NOT feed the ratio composition (#598).
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
	// #1618-A: the ACTUAL side (totalInput) prices images and thinking
	// tokens; the ESTIMATED side is text-composition only - a vision
	// session's samples structurally overshoot the ratio (observedRatio
	// pinned at the 3.0 clamp on every recalibration, tripling every
	// subsequent estimate and compacting long sessions early), and the
	// mirror (calibrate text-only, then attach images) under-reports and
	// 400s mid-run. The estimator has no image/thinking pricing yet, so
	// skip contaminated samples rather than mis-calibrate.
	for _, msg := range m.messages {
		for _, b := range msg.Content {
			// #1640-1: the provider side emits Type=='thinking' (anthropic
			// thinking blocks) - 'reasoning' never occurs as a block type
			// anywhere else, making the thinking half of #1618-A dead code.
			// #1640-2: tool_result blocks carry embedded images in
			// b.Images - the top-level type check let them through.
			// #1652-1: redacted_thinking carries encrypted data in
			// ThinkingData (ReasoningContent/Images empty) and slipped
			// through all three conditions on the non-streaming path -
			// base64/encrypted bytes depressed asciiRatio and polluted the
			// calibration sample.
			if b.Type == "image" || b.Type == "thinking" || b.Type == "redacted_thinking" || len(b.Images) > 0 {
				debug.Log("context-calibrator", "sample-frozen: image/thinking blocks present (see #1618-A)")
				return
			}
		}
	}
	// #649: latinExtChars feeds the composition shares so Vietnamese-style
	// residuals are attributed to their own (fixed-tier) bucket instead of
	// 100% to asciiRatio. RecordSample itself freezes the adjustment when
	// latinExt dominates (see token_calibrator.go).
	m.calibrator.RecordSample(m.tokens+m.toolDefinitionOverhead, totalInput, asciiChars, cjkChars, latinExtChars)
	debug.Log("ctx", "RecordUsage: input=%d cache_read=%d output=%d old_baseline=%d→new_baseline=%d estimated=%d delta=%d",
		usage.InputTokens, usage.CacheRead, usage.OutputTokens, oldBaseline, m.baselineTokens, m.tokens, m.baselineTokens-m.tokens)
}

// totalContentRunes counts all runes across message text/reasoning/output
// regardless of script (#605 G3) — used to distinguish "empty context" from
// "context written in scripts the ratio calibration does not cover".
func (m *Manager) totalContentRunes() int {
	total := 0
	for _, msg := range m.messages {
		for _, b := range msg.Content {
			total += utf8.RuneCountInString(b.Text + b.ReasoningContent + b.Output)
		}
	}
	return total
}

// compositionLocked returns the ASCII/CJK/Latin-Extended character counts
// of all message text, for composition-aware calibration (#355). Caller must
// hold m.mu. Uses scriptTokenClasses from #535 to properly categorize
// Cyrillic, Greek, and Latin-Extended scripts (previously invisible to
// calibration).
func (m *Manager) compositionLocked() (asciiChars, cjkChars, latinExtChars int) {
	for _, msg := range m.messages {
		for _, b := range msg.Content {
			text := b.Text + b.ReasoningContent + b.Output
			// #598: Latin-Extended/Cyrillic/Greek counts are intentionally
			// excluded from the ascii/cjk calibration composition — only true
			// CJK feeds the CJK ratio (see below). latinExt is returned
			// separately (#634) so RecordUsage's freeze check can treat it as a
			// covered script without feeding it into the ratio math.
			a, c, le, _, _, _ := scriptTokenClasses(text)
			asciiChars += a
			// Merge of Latin-Extended/Cyrillic/Greek into the CJK bucket for
			// calibration purposes was REMOVED by #598: only true CJK
			// participates in the CJK calibration ratio. #578's fix folded
			// Cyrillic/Greek/LatinExt into this bucket citing "similar density",
			// but the project's own tokenizer prices them 2.5/2.0/3.0
			// chars-per-token vs CJK 1.0 — a 2.5x gap. A pure-Cyrillic session
			// pegged cjkRatio to its 2.0 clamp and Chinese tokens were then
			// underestimated ~50%, delaying auto-compact back into #515-style
			// provider hard errors.
			cjkChars += c
			latinExtChars += le
		}
	}
	return asciiChars, cjkChars, latinExtChars
}

func (m *Manager) Clear() {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.userReset = true // #663: /clear is a user-driven semantic reset
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

// SetToolDefinitionOverhead records the token cost of the tool definitions
// (schemas, names, descriptions) that the Agent will pass to the provider.
// This overhead is included in fixedPromptOverheadLocked so the compact
// threshold reflects the actual space unavailable for conversation messages.
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

	// #625: single-group sessions have no recent group to keep verbatim
	// (len(groups) == minRecentGroups), so the compaction-triggering user
	// request would otherwise survive only as the summary's one-sentence
	// "Task" line. Embed its head verbatim in the summarization payload.
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
	// #479 TOCTOU guard: if any NON-TAIL mutation happened during the LLM
	// window (deletes from ReconcileToolCalls, mid-inserts, mechanical
	// clears like CompactOldReasoningBlocks, compaction replaces — anything
	// other than a pure tail append, which extraMsgs already rescues), this
	// snapshot is stale. Replaying it would resurrect deleted messages,
	// drop inserted tool_results, and undo in-place compaction (which then
	// re-triggers the threshold — a waste loop). Mirror ApplyCompactResult's
	// shrink guard: discard and let the next trigger re-plan from CURRENT
	// state.
	if m.nonTailMutSeq != plan.origVersion {
		m.lastSummarizeApplied = false // #702b: discarded — nothing was applied
		m.mu.Unlock()
		debug.Log("ctx", "Summarize: non-tail mutation during LLM window (seq %d→%d), discarding stale snapshot",
			plan.origVersion, m.nonTailMutSeq)
		return nil
	}
	oldTokens := m.tokenCountLocked()
	// Collect any messages that arrived during summarization (TOCTOU fix)
	var extraMsgs []provider.Message
	if len(m.messages) > plan.origLen {
		extraMsgs = make([]provider.Message, len(m.messages)-plan.origLen)
		copy(extraMsgs, m.messages[plan.origLen:])
	}

	newMsgs := make([]provider.Message, 0, len(extraMsgs)+2)
	if plan.hasSystem {
		// Use the live system message instead of the stale snapshot taken
		// before the LLM call. This prevents TOCTOU: system prompt may have
		// been updated (ratchet rules, autopilot state, peer changes) during
		// the summarization LLM call. Same fix as ApplyCompactResult (line 696).
		if len(m.messages) > 0 && m.messages[0].Role == "system" {
			newMsgs = append(newMsgs, m.messages[0])
		} else {
			newMsgs = append(newMsgs, plan.systemMsg)
		}
	}
	// Summary message gets a unique ID so checkpoint can reference it.
	// It will be persisted to JSONL by the caller via AppendMessageToDisk.
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
	m.lastSummarizeApplied = true // #702b: this Summarize call did apply
	// Re-inject pinned context after compaction — the pinned contract
	// ("survive compaction") must hold on the direct Summarize path too
	// (PTL recovery, /compact), not just ApplyCompactResult. Without this,
	// pinned items compressed into the summary were silently lost (#382).
	m.injectPinnedAfterCompaction()
	// Same contract for the registered state note (task board rehydration):
	// it must survive on the direct Summarize path (PTL recovery, /compact).
	m.injectPostCompactNoteAfterCompaction()
	m.version++
	m.nonTailMutSeq++
	m.recalcTokens()
	debug.Log("ctx", "Summarize: msgs=%d→%d oldTokens=%d newTokens=%d summaryChars=%d summaryEstimatedTokens=%d extraMsgs=%d stateTextChars=%d",
		oldLen, len(newMsgs), oldTokens, m.tokenCountLocked(), len(summaryText), EstimateTokens(summaryText), len(extraMsgs), len(stateText))
	m.mu.Unlock()
	return nil
}

// CheckAndSummarize runs Summarize unconditionally. The caller is expected
// to have already decided compaction is needed (e.g., a prompt-too-long error
// or the token count exceeding the auto-compact threshold). Removing the
// threshold check here guarantees that once the trigger fires, we always
// attempt LLM-based compression.
func (m *Manager) CheckAndSummarize(ctx context.Context, prov provider.Provider) (bool, error) {
	debug.Log("ctx", "CheckAndSummarize: tokens=%d contextWindow=%d threshold=%d ratio=%.2f", m.TokenCount(), m.ContextWindow(), m.AutoCompactThreshold(), m.UsageRatio())

	err := m.Summarize(ctx, prov)
	if err != nil {
		debug.Log("ctx", "CheckAndSummarize: Summarize FAILED: %v", err)
		return false, err
	}
	m.mu.Lock()
	// #702b: do NOT infer "compaction happened" from a version delta — Add()
	// also bumps m.version, so a concurrent Add during the LLM window made
	// this report a false "changed=true" on the no-plan and TOCTOU-discard
	// paths (both return nil without applying), causing callers to skip
	// fallbacks (e.g. PTL retry) while the context was still over budget.
	// Summarize now records explicitly whether it applied.
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
	m.markBenignRemoval() // #663/#702a: retry truncation is benign tail cleanup — a live-shrunk
	// compaction result rejected after this must classify as BenignTrim
	// (cooldown stays), not UserReset (which would refund the precompact
	// cooldown and reschedule a redundant full-context summarization).
	m.version++
	m.nonTailMutSeq++
	m.recalcTokens()
	return true
}

// RemoveLastAssistantGroup removes the most recent assistant message and any
// trailing tool messages that follow it. This is used by /regenerate to
// discard the agent's last response so it can be re-generated. Returns the
// text of the last remaining user message, or "" if no regeneration is
// possible (no assistant message found or no preceding user message).
// isToolResultCarrier reports whether a Role:"user" message is actually a
// tool_result carrier inserted by ReconcileToolCalls rather than a real user
// prompt: all its blocks are tool outputs (#3745).
func isToolResultCarrier(msg provider.Message) bool {
	hasToolResult := false
	for _, b := range msg.Content {
		if b.Type == "tool_result" {
			hasToolResult = true
			continue
		}
		if b.Type == "text" && strings.TrimSpace(b.Text) != "" {
			return false // real prompt text present
		}
	}
	return hasToolResult
}

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
	// Find the last REAL user message before the assistant message.
	// #3745: ReconcileToolCalls inserts tool_result carriers with
	// Role:"user" (L598); stopping the reverse scan on one truncates at the
	// carrier, deleting the trailing assistant while keeping the earlier
	// assistant(tool_use) - the next API call then fails with unpaired
	// tool_use. Skip carriers: a user message whose blocks are (only)
	// tool_result outputs is not a prompt.
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
	// Extract the user message text for re-submission.
	userText := ""
	for _, b := range m.messages[lastUserIdx].Content {
		if b.Type == "text" && b.Text != "" {
			userText = b.Text
			break
		}
	}
	// #3745: an empty userText (image-only input, or a carrier that slipped
	// through) means there is nothing to re-submit. The contract treats "" as
	// "cannot regenerate" and the caller gives up - but the truncation below
	// used to run FIRST, irreversibly deleting the assistant reply with no
	// undo path. Fail safe: no recoverable text, no deletion.
	if userText == "" {
		return ""
	}
	// Truncate: keep everything up to and including the last user message,
	// discard the assistant response and any trailing tool messages.
	// #3797: compute removed BEFORE the slice assignment - after it,
	// len(m.messages) == lastUserIdx+1 and the old expression evaluated to
	// a constant 1 (plus a meaningless +1 counting the KEPT user message),
	// misreporting every regenerate in the debug log.
	removed := len(m.messages) - lastUserIdx - 1
	m.messages = m.messages[:lastUserIdx+1]
	m.markBenignRemoval() // #663: retry/regenerate truncation — no semantic loss
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

// reasoningCompactMinLen is the minimum ReasoningContent length to bother
// compacting. Short reasoning traces waste negligible tokens. Reasoning
// from past turns provides zero marginal value once the turn is complete
// and the model has produced its response/tool calls.
const reasoningCompactMinLen = 200

// EstimateClearableTokens was removed (#718): it returned ~920*count CHARS
// while its name/doc promised TOKENS (a 4x overestimate that would have made
// any cache-break-vs-savings gate decide in the wrong direction), and it had
// zero callers repo-wide.
//
// ClearOldToolResults and ClearOldToolUseInputs were removed as dead code:
// they were never wired into Compact or any other call path (compaction uses
// the summary-payload path above instead), and grep found zero callers
// repo-wide.

// headRunesPlain returns the longest prefix of s that is at most maxBytes
// long AND ends on a rune boundary. Unlike headRunes it appends no marker.
// Rune-safe (#718): byte-boundary slicing split multi-byte UTF-8 sequences
// (CJK, emoji), and json.Marshal then emitted U+FFFD for the dangling
// partial rune.
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

// CompactOldReasoningBlocks truncates the ReasoningContent of thinking/reasoning
// blocks in all assistant messages EXCEPT the most recent one. Reasoning content
// from past turns consumes significant context (often 1K-10K+ tokens per turn)
// with zero marginal value once the turn is complete and the model has produced
// its response or tool calls.
//
// Both Anthropic extended thinking (ThinkingSignature) and DeepSeek reasoning
// (plain text) are handled. When a thinking block has a ThinkingSignature, the
// signature is cleared along with the content: the signature is a cryptographic
// binding to the exact reasoning text, so a tampered placeholder + stale
// signature would be rejected by Anthropic with 400 "invalid signature in
// thinking block". Clearing the signature makes buildParams skip the block
// entirely on echo-back. Blocks without a signature (DeepSeek reasoning_content)
// keep the placeholder behavior.
//
// This is a purely mechanical operation (no LLM call needed) and is safe because:
//  1. Anthropic's API verifies the thinking signature against the thinking text;
//     clearing both removes the block from the request entirely
//  2. DeepSeek reasoning_content is advisory — an empty/short string is accepted
//
// Returns the estimated number of tokens freed.
func (m *Manager) CompactOldReasoningBlocks() int {
	m.mu.Lock()
	defer m.mu.Unlock()

	if len(m.messages) == 0 {
		return 0
	}

	// Find the most recent assistant message index to protect it.
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

	// Collect targets: reasoning blocks in older assistant messages.
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
			// Idempotency: skip already-compacted blocks
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
		// The signature is a cryptographic binding to the original reasoning
		// text. Keeping it alongside a placeholder would be rejected by
		// Anthropic with 400 "invalid signature in thinking block" on every
		// subsequent request. Clear it so buildParams skips the block on
		// echo-back. Harmless for unsigned reasoning (DeepSeek).
		if block.ThinkingSignature != "" {
			block.ThinkingSignature = ""
		}
	}

	before := m.tokens
	m.version++
	m.nonTailMutSeq++
	m.recalcTokens()
	freed := before - m.tokens
	debug.Log("ctx", "CompactOldReasoningBlocks: compacted %d reasoning blocks, freed ~%d tokens", len(targets), freed)
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
	// Track:
	//   file path → ordered list of ToolIDs that read it
	//   ToolID → set of (normalized) paths it read (for multi-file reads)
	// For read_file, also record the line range [offset, offset+limit) so
	// #718 range-aware supersession can distinguish a head segment from a
	// tail segment of the same file. multi_file_read has no offset
	// parameter, so its calls keep a whole-file range.
	pathToToolIDs := make(map[string][]string)
	toolIDToPaths := make(map[string]map[string]bool)
	toolIDToRange := make(map[string]readRange) // read_file only: which slice of the file
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

	// Phase 2: For each path read more than once, all but the last read
	// are candidates for supersession. However, a multi_file_read tool_id
	// that read files A, B, C should only be compacted if ALL of A, B, C
	// have been re-read later — otherwise we lose content for files that
	// were NOT re-read. For single read_file calls (one path), the
	// current behavior is correct: the single file was re-read.
	//
	// #718: "re-read later" must additionally mean the later read COVERS
	// the earlier one. read_file encourages paging large files with
	// offset/limit; an offset=1 head read and an offset=5000 tail read
	// share no content, so keying supersession on path alone replaced the
	// head read's output with a placeholder and the model "forgot" the
	// top of the file. Coverage rule: a full read (no offset param)
	// supersedes every earlier read of the path; a partial read supersedes
	// only earlier reads whose recorded range it covers.
	partiallySuperseded := make(map[string]bool) // toolIDs with ≥1 file superseded
	for _, ids := range pathToToolIDs {
		if len(ids) < 2 {
			continue
		}
		for i, id := range ids[:len(ids)-1] {
			// superseded iff SOME later read of this path covers id's range
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
			// #3726: supersession requires CONTENT coverage, not mere
			// existence of a later read (#718 principle, as phase-2 above
			// implements for the triggering path). The old check - "some
			// later tool_id read this path" - let a LATER PARTIAL read of
			// this path (an offset re-read of a slice the original read
			// already covered) mark the whole multi-file read superseded,
			// dropping tool results the conversation still depends on.
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
				break // tool ids appear once per path
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

	// Phase 3: Compact tool_results for superseded ToolIDs.
	freedChars, compacted := m.compactSupersededToolResults(supersededIDs,
		"file was re-read later in the conversation")
	if freedChars == 0 {
		return 0
	}

	freed := m.commitMechanicalCompaction()
	debug.Log("ctx", "CompactSupersededReads: compacted %d superseded file reads, freed ~%d tokens", compacted, freed)
	return freed
}

// CompactSupersededCommands replaces the output of earlier run_command calls
// that were re-run later in the conversation with a compact placeholder.
// Repeated command execution is one of the largest sources of stale context
// in coding agents (build/test cycles): once `go build` has been re-run
// after an edit, the previous run's output is expired — only the latest run
// reflects the current state of the code (the "expired" waste category from
// AgentDiet, arXiv:2509.23586). Like CompactSupersededReads this is a purely
// mechanical operation (no LLM call) and protocol-safe: tool_result output
// is rewritten in place, so tool_use/tool_result pairing and message order
// are untouched.
//
// Returns the approximate number of tokens freed.
func (m *Manager) CompactSupersededCommands() int {
	m.mu.Lock()
	defer m.mu.Unlock()

	// Phase 1: collect run_command tool_use blocks keyed by normalized
	// command string, preserving chronological order.
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

	// Phase 2: for each command run more than once, all runs except the
	// last are superseded by the newer run.
	supersededIDs := make(map[string]bool)
	for _, ids := range cmdToToolIDs {
		for _, id := range ids[:len(ids)-1] {
			supersededIDs[id] = true
		}
	}
	if len(supersededIDs) == 0 {
		return 0
	}

	// Phase 3: rewrite superseded tool_results in place.
	freedChars, compacted := m.compactSupersededToolResults(supersededIDs,
		"command was re-run later in the conversation")
	if freedChars == 0 {
		return 0
	}

	freed := m.commitMechanicalCompaction()
	debug.Log("ctx", "CompactSupersededCommands: compacted %d superseded command runs, freed ~%d tokens", compacted, freed)
	return freed
}

// compactSupersededToolResults rewrites the tool_result blocks whose ToolID is
// in supersededIDs with a one-line placeholder. It is the shared Phase 3 of
// the mechanical supersession passes (reads and commands): both categories
// expire stale output the same way, so the rewrite loop, the idempotency
// guard, the small-result threshold and the placeholder format live here.
// Mutates blocks in place; the caller must hold m.mu. Returns the characters
// freed and the number of blocks compacted.
func (m *Manager) compactSupersededToolResults(supersededIDs map[string]bool, reason string) (freedChars, compacted int) {
	for i := range m.messages {
		for j := range m.messages[i].Content {
			b := &m.messages[i].Content[j]
			if b.Type != "tool_result" || !supersededIDs[b.ToolID] {
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
			b.Output = fmt.Sprintf("[superseded: %s, output was %d chars]", reason, origLen)
			b.Images = nil
			freedChars += origLen
			compacted++
		}
	}
	return freedChars, compacted
}

// commitMechanicalCompaction applies the shared bookkeeping after a mechanical
// supersession pass rewrote tool_result outputs: marks the removal benign
// (#663/#702a), bumps the version counters, recalculates the token estimate
// and returns the approximate tokens freed. The caller must hold m.mu and must
// only call this when something was actually freed.
func (m *Manager) commitMechanicalCompaction() int {
	before := m.tokens
	m.markBenignRemoval()
	m.version++
	m.nonTailMutSeq++
	m.recalcTokens()
	return before - m.tokens
}

// extractCommand returns the normalized key identifying a run_command
// invocation, or "" if the tool is not run_command or the input cannot be
// parsed. The key is the command string (trimmed) plus, when set, the
// working_dir: `go build` and `cd x && go build` are different commands, and
// so is the same command run in a different directory - none may supersede
// each other.
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

// readRange describes which slice of a file a read_file call covered.
// full=true means the whole file was requested (no offset parameter).
type readRange struct {
	offset int64
	limit  int64
	full   bool
}

// readCovers reports whether the later read's range covers the earlier
// read's range, i.e. every part the earlier read returned is also contained
// in the later read's window. read_file's offset/limit are 1-based line
// numbers; only their numeric order matters for coverage. A zero
// (full=false, offset=0, limit=0) earlier range — pre-#718 tool results
// whose recorded args are unknown — is treated as coverable by anything,
// preserving the pre-#718 whole-file semantics for those.
func readCovers(later, earlier readRange) bool {
	if later.full {
		return true // whole-file read covers everything
	}
	if earlier.full {
		return false // a partial read cannot cover a full read
	}
	if earlier == (readRange{}) {
		return true // unknown earlier range: keep pre-#718 behavior
	}
	if earlier.limit <= 0 {
		// Earlier read had offset but no limit → offset..EOF.
		return later.offset <= earlier.offset && later.limit <= 0
	}
	if later.limit <= 0 {
		// Later read: offset..EOF. Covers earlier iff it starts at or before it.
		return later.offset <= earlier.offset
	}
	return later.offset <= earlier.offset && later.offset+later.limit >= earlier.offset+earlier.limit
}

// readCall is the parsed form of a read tool's input: the paths it touched
// plus (for read_file) the line range it covered.
type readCall struct {
	paths []string
	rng   readRange // meaningful only for read_file (single path)
}

// extractReadCall parses a read tool's Input JSON into paths plus the
// range read for read_file (#718). multi_file_read keeps a full range — it
// has no offset parameter, whole files are always returned.
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

// extractReadPaths extracts file paths from the Input JSON of read tools.
// Supports read_file ({"path": "..."}) and multi_file_read ("files": [{"path": "..."}]}).
func extractReadPaths(toolName string, input json.RawMessage) []string {
	return extractReadCall(toolName, input).paths
}

// normalizeFilePath normalizes a file path for comparison purposes.
// Strips "./" prefix, converts backslashes to forward slashes, and lowercases
// only on case-insensitive filesystems (macOS, Windows). Linux filesystems
// are case-sensitive — unconditional lowercasing made src/Foo.go and
// src/foo.go (different files) collide in CompactSupersededReads, silently
// replacing one file's content with a factually-wrong superseded marker
// (#195).
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
	if runtime.GOOS != "linux" {
		return strings.ToLower(p)
	}
	return p
}

// countTokens uses the provider's token counting API when available,
// falling back to heuristic estimation.
//
// To avoid creating a context.WithTimeout on every call (which happens on
// every Add() — potentially hundreds of times per session), we cache whether
// the provider actually does RPC in CountTokens. OpenAI and Gemini use pure
// local estimation, so after the first probe we skip context creation entirely.
func (m *Manager) countTokens(msg provider.Message) int {
	if m.provider != nil {
		// Fast path: if we've already determined the provider doesn't do RPC,
		// skip context creation and go straight to estimation.
		if m.providerCountChecked && !m.providerCountSupportsRPC {
			return m.estimateTokens(msg)
		}
		ctx, cancel := context.WithTimeout(context.Background(), tokenCountTimeout)
		defer cancel()
		start := time.Now()
		n, err := m.provider.CountTokens(ctx, []provider.Message{msg})
		elapsed := time.Since(start)
		if err == nil && n > 0 {
			// If the call completed in <1ms, it's local estimation (no RPC).
			// Cache this so future calls skip context creation.
			if !m.providerCountChecked && elapsed < time.Millisecond {
				m.providerCountChecked = true
				m.providerCountSupportsRPC = false
				debug.Log("ctx", "countTokens: provider uses local estimation (elapsed=%v), caching fast path", elapsed)
			}
			return n
		} else if err != nil {
			// Provider returned an error. Transient failures (timeout, 429,
			// network) are NOT evidence the provider lacks the RPC — caching
			// "not supported" on the first error degraded every later count to
			// the heuristic for the whole session (#196). Only classify as
			// unsupported when the call completed but returned nothing.
			debug.Log("ctx", "countTokens: provider.CountTokens failed (%v), falling back to heuristic (will retry)", err)
		}
	}
	return m.estimateTokens(msg)
}

// estimateTokensStandalone provides estimation with default (uncalibrated) ratios.
// Used by tests and as a fallback when no Manager/calibrator is available.
func estimateTokensStandalone(msg provider.Message) int {
	m := &Manager{calibrator: NewTokenCalibrator()}
	return m.estimateTokens(msg)
}

func (m *Manager) estimateTokens(msg provider.Message) int {
	return m.estimateTokensCalibrated(msg)
}

// estimateTokensCalibrated is the shared per-message token accounting used by
// both estimateTokens and estimateMessagesTokens (#535). Previously the two
// diverged: estimateMessagesTokens (the budget basis for recent-group
// retention in buildSummaryPlan) skipped ReasoningContent and the ×6
// tool_use structural overhead, underestimating reasoning-model messages by
// up to 30x — recent groups far over budget were kept, compaction triggered
// again immediately, and the session entered a compaction loop.
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
		// #1640-2: embedded tool_result images are counted EACH - the old
		// hasImage bool priced multi-image messages at one flat image.
		imageCount += len(b.Images)
		if b.Type == "tool_use" {
			toolCallCount++
		}
	}
	n := EstimateTokensCalibrated(sb.String(), m.calibrator)
	// perImageTokenCost matches messageStructuralTokens' flat 170 (#1640-2:
	// extra images beyond the first are priced, not flattened into one).
	extra := imageCount - 1
	if extra < 0 {
		extra = 0
	}
	return n + messageStructuralTokens(toolCallCount, imageCount > 0) + 170*extra
}

// messageStructuralTokens returns the per-message structural overhead shared
// by all token accounting paths (#535):
//   - 4 tokens for message framing (role, separators)
//   - 6 tokens per tool_use block (JSON structure: name, id, type, braces)
//   - 170 tokens for images (85-170 for thumbnails, up to 1100 for large)
func messageStructuralTokens(toolCallCount int, hasImage bool) int {
	n := 4 + toolCallCount*6
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
	// origVersion is m.nonTailMutSeq at snapshot time (#479) — a counter
	// bumped ONLY by non-tail mutations (compaction replaces, mechanical
	// clears, truncations, mid/front inserts, ReconcileToolCalls edits).
	// Plain Add (tail append) does NOT bump it, so concurrent message
	// arrivals during the LLM window keep being rescued by extraMsgs while
	// everything else discards the stale snapshot.
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

	// Adaptive recent group retention: keep the last few interaction groups
	// verbatim to preserve immediate working context after compaction
	// (same approach as Claude Code). Budget-aware: only keep groups that
	// fit within maxRecentGroupTokenRatio of the context window. Requires
	// at least minRecentGroups+1 total groups so there's something to summarize.
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
		// Guarantee: never let budget checks violate minRecentGroups — the
		// last interaction group (which triggered compaction, i.e. the current
		// user request) is kept verbatim even if it exceeds the budget. Going
		// over budget only means compaction triggers sooner next round, and
		// TruncateOldestGroupForRetry can trim oversized groups downstream.
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
		// Budget exceeded or too few groups: summarize all (rolling compaction).
		plan.oldMsgs = append([]provider.Message(nil), m.messages[start:]...)
		plan.recentMsgs = nil
		debug.Log("ctx", "buildSummaryPlan: summarizing all %d messages (groups=%d, recent=0 budget_exceeded)", len(plan.oldMsgs), len(groups))
	}
	return plan, len(plan.oldMsgs) > 0
}

// estimateMessagesTokens returns a rough token estimate for a slice of
// messages, used for budget-aware recent group retention decisions.
// Uses the same per-message accounting as (*Manager).estimateTokens
// (#535): previously it skipped ReasoningContent and the tool_use ×6
// overhead, so reasoning-heavy recent groups were massively under-budgeted
// and compaction looped.
func estimateMessagesTokens(msgs []provider.Message) int {
	m := &Manager{calibrator: NewTokenCalibrator()}
	total := 0
	for _, msg := range msgs {
		total += m.estimateTokensCalibrated(msg)
	}
	return total
}

// triggerVerbatimMaxLen caps the verbatim embed of the compaction-triggering
// user message (#625). Single-group sessions have no recent group to keep
// verbatim (buildSummaryPlan requires len(groups) > minRecentGroups), so the
// trigger message would otherwise survive only as the summary's one-sentence
// "Task" line. The head of the raw request is embedded verbatim instead —
// large enough to carry a full task statement, small enough that the
// summarization prompt stays cheap.
const triggerVerbatimMaxLen = 8000

// headRunes returns the first n runes of s (rune-safe, unlike s[:n]).
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
		// #625: single-group compaction has no verbatim recent group, so the
		// triggering user request is embedded verbatim at the top of the
		// payload — otherwise it survives only as a one-sentence summary.
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

		// PCC (arXiv 2605.23296): large payloads fan out into parallel
		// per-block summaries to cut the blocking wall time; ANY block
		// failure or emptiness falls back to the sequential path below,
		// so parallel compaction is never worse than the baseline.
		if blocks := splitPayloadBlocks(payload, pccMinPayloadTokens); len(blocks) > 1 {
			if summaryText, ok := summarizeParallel(ctx, prov, blocks, summaryTokenLimit, onUsage); ok {
				// sa-237 fact retention: deterministically re-attach dropped
				// constraints and recurring paths on every summary path.
				// #3675: constraints come from user-role text only.
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
		// #3675: constraints come from user-role text only.
		return applyFactRetention(summaryText, payload, userConstraintSource(msgs)), nil
	}
	return "", fmt.Errorf("summarization returned empty text")
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
	// Without a real LLM usage baseline, estimate everything we send to the
	// provider: conversation messages plus the tool definitions overhead.
	return m.tokens + m.toolDefinitionOverhead
}

func (m *Manager) autoCompactThresholdLocked() int {
	if m.contextWindow <= 0 {
		debug.Log("ctx", "autoCompactThreshold: contextWindow=0 → threshold=0")
		return 0
	}
	// Always leave room for the model's maximum output tokens. The simple
	// rule: compact once conversation fills contextWindow - maxOutputTokens.
	reserve := m.effectiveOutputReserveLocked()
	threshold := m.contextWindow - reserve
	result := threshold
	if threshold < minSummaryReserve {
		result = minSummaryReserve
	}
	if m.lastLoggedThreshold != result {
		debug.Log("ctx", "autoCompactThreshold: contextWindow=%d outputReserve=%d rawThreshold=%d finalThreshold=%d",
			m.contextWindow, reserve, threshold, result)
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
		payloadToolResultMaxLen = 500 // max chars for tool result in summary payload
		payloadToolResultHead   = 200 // keep first N chars
		payloadToolInputMaxLen  = 300 // max chars for tool input in summary payload
		payloadUserMsgMaxLen    = 500 // max chars per user message in verbatim section
	)

	var sb strings.Builder

	// Extract user requests verbatim — these are the most critical signal for
	// continuing work (ACE paper: "brevity bias" causes summaries to drop
	// domain insights, especially the original user intent).
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
					// #718: rune-safe head — a byte cut split multi-byte runes
					// and json.Marshal emitted U+FFFD for the partial rune.
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

// lastUserMessageText returns the text of the LAST user-role text message
// in msgs, rune-truncated to triggerVerbatimMaxLen (#625). Returns "" when
// there is none.
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

// extractUserRequests collects text content from user-role messages, returning
// them as a list of truncated strings. This is used to prepend verbatim user
// requests to the summarization payload, preventing "brevity bias" (ACE paper)
// where summaries drop the original user intent.
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
			// Skip tool result echoes (some providers put tool_result content
			// in user messages with only tool_result blocks, but we check text)
			if len(text) > maxLen {
				text = headRunesPlain(text, maxLen-3) + "..."
			}
			requests = append(requests, text)
		}
	}
	return requests
}

// formatToolInputForSummary extracts the most informative fields from a tool
// call's JSON input and returns a concise human-readable string. This lets the
// summarization LLM know WHICH file was read, WHAT command was run, etc.
// Without this, the summarizer only sees "Tool call: read_file" with no path.
// formatImageBlockForSummary renders a textual marker for an image content block.
// Images cannot be carried through the textual summarization payload, so without
// this marker an image would be silently erased during compaction — the agent
// would forget it ever existed (e.g., a screenshot of an error, a design mockup).
// The marker preserves metadata (MIME type, approximate size) so the summarizer
// can record that an image was relevant to the conversation.
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

// approxImageKB returns an approximate decoded size in KB from base64-encoded
// image data, without decoding it (cheap, allocation-free). Returns 0 if the
// data is empty or too short.
func approxImageKB(b64 string) int {
	if len(b64) < 8 {
		return 0
	}
	// base64 encodes 3 bytes per 4 characters.
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
		if len([]rune(s)) > 80 {
			s = string([]rune(s)[:77]) + "..."
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
