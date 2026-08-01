package agent

// Task Re-anchoring — Context Collapse Prevention
//
// Research basis: Multiple 2026 papers (AgentMarketCap survey, arXiv:2603.07670)
// identify "context collapse on long repair chains" as a critical agent failure
// mode. After 8-10 tool calls or correction rounds, models lose context about
// the original task specification. Corrections become local and disconnected
// from the broader goal — the agent "forgets" what it was asked to do, even
// though the original request is still technically in the context window.
//
// This is different from:
//   - Context budget (context is full → needs compaction)
//   - Scope drift (touching too many files)
//   - Progress checkpoint (tied to maxIter percentage, not tool call count)
//   - Plan suggestion (one-time early injection)
//
// The key insight: context collapse is driven by TOOL CALL COUNT, not iteration
// count. An agent doing parallel tool calls or many tools per iteration reaches
// 10+ tool calls much faster than 10 iterations. Existing injections (progress
// checkpoint at 60% of maxIter, convergence at 85%) fire too late — by then
// the damage is already done.
//
// Competitor approaches:
//   - Claude Code: re-injects task summary after every N tool calls (internal)
//   - Cursor: no runtime re-anchoring; relies on system prompt
//   - Devin: periodic "navigator" LLM call that reviews progress vs task
//   - OpenHands: separate planner agent re-checks task alignment
//
// Our approach: deterministic, zero-LLM-cost re-anchoring. After every N tool
// calls within a single run, inject a compact reminder of the original task.
// The anchor is built from RunStats (original prompt, files edited, iteration
// count) and is capped to ~200 tokens to minimize context overhead. Only fires
// for long runs (totalToolCalls >= anchorInterval) and at most every
// anchorInterval tool calls to avoid noise.

import (
	"fmt"
	"strings"
	"time"

	"github.com/topcheer/ggcode/internal/debug"
)

const (
	// anchorFirstFire: minimum total tool calls before the first anchor.
	// Research consistently identifies 8-10 as the threshold where context
	// collapse begins. We use 10 to avoid false positives on medium tasks.
	anchorFirstFire = 10

	// anchorInterval: subsequent anchors fire every this many tool calls.
	// Re-anchoring every 8 calls after the first keeps the task fresh without
	// overwhelming the context with reminders.
	anchorInterval = 8

	// anchorMaxPromptLen: truncate the original prompt in the anchor message.
	// Long prompts (>500 chars) make the anchor too heavy and waste context.
	anchorMaxPromptLen = 500

	// anchorMaxFiles: max number of file paths to list in the anchor.
	anchorMaxFiles = 8
)

// taskAnchorState tracks cumulative tool call counts to decide when to
// re-inject the original task as a context-collapse countermeasure.
type taskAnchorState struct {
	// lastAnchoredAt is the total tool call count when the last anchor
	// was injected. 0 means no anchor has been injected yet.
	lastAnchoredAt int

	// userPrompt is the original user request text, captured at run start.
	userPrompt string

	// runStartTime is when the current run began, for elapsed time display.
	runStartTime time.Time
}

// newTaskAnchorState creates a fresh anchor state for a new run.
func newTaskAnchorState(userPrompt string, startTime time.Time) *taskAnchorState {
	return &taskAnchorState{
		userPrompt:   userPrompt,
		runStartTime: startTime,
	}
}

// maybeReanchorTask checks whether a task re-anchor should be injected based
// on the cumulative tool call count. Returns the anchor message text (empty
// string if no anchor is needed).
//
// The decision uses a two-phase schedule:
//   - Phase 1 (first anchor): fires when totalToolCalls >= anchorFirstFire
//   - Phase 2 (subsequent): fires every anchorInterval calls after the first
//
// This ensures the first anchor fires early enough to prevent collapse (at
// ~10 tool calls), then refreshes regularly without being noisy.
func (s *taskAnchorState) maybeReanchorTask(totalToolCalls int, iterations int, filesEdited []string) string {
	if totalToolCalls < anchorFirstFire {
		return ""
	}

	var shouldFire bool
	if s.lastAnchoredAt == 0 {
		// First anchor: fire at the threshold.
		shouldFire = totalToolCalls >= anchorFirstFire
	} else {
		// Subsequent anchors: fire every anchorInterval calls.
		shouldFire = totalToolCalls-s.lastAnchoredAt >= anchorInterval
	}

	if !shouldFire {
		return ""
	}

	s.lastAnchoredAt = totalToolCalls
	return s.buildAnchorMessage(totalToolCalls, iterations, filesEdited)
}

// buildAnchorMessage constructs a compact task reminder message.
// Capped to ~200 tokens of context overhead.
func (s *taskAnchorState) buildAnchorMessage(totalToolCalls int, iterations int, filesEdited []string) string {
	var sb strings.Builder

	sb.WriteString("[Task Anchor — you have made ")
	sb.WriteString(fmt.Sprintf("%d tool calls across %d iterations", totalToolCalls, iterations))
	sb.WriteString(". Verify you are still on track for the original request.]\n\n")

	sb.WriteString("Original request:\n")
	prompt := strings.TrimSpace(s.userPrompt)
	if len(prompt) > anchorMaxPromptLen {
		prompt = prompt[:anchorMaxPromptLen] + "..."
	}
	sb.WriteString(prompt)
	sb.WriteString("\n\n")

	if elapsed := time.Since(s.runStartTime); elapsed > 0 {
		sb.WriteString(fmt.Sprintf("Elapsed: %s. ", formatAnchorDuration(elapsed)))
	}

	if len(filesEdited) > 0 {
		sb.WriteString("Files modified so far: ")
		display := filesEdited
		if len(display) > anchorMaxFiles {
			display = display[:anchorMaxFiles]
			sb.WriteString(strings.Join(display, ", "))
			sb.WriteString(fmt.Sprintf(", ... (%d total)", len(filesEdited)))
		} else {
			sb.WriteString(strings.Join(display, ", "))
		}
		sb.WriteString(". ")
	}

	sb.WriteString("\nIf your current actions have drifted from the original request, refocus. Do not start unrelated work.")

	debug.Log("task-anchor", "injecting task anchor at %d tool calls, %d iterations, %d files edited",
		totalToolCalls, iterations, len(filesEdited))
	return sb.String()
}

// formatAnchorDuration produces a compact human-readable duration string.
func formatAnchorDuration(d time.Duration) string {
	if d < time.Minute {
		return fmt.Sprintf("%ds", int(d.Seconds()))
	}
	if d < time.Hour {
		return fmt.Sprintf("%dm%ds", int(d.Minutes()), int(d.Seconds())%60)
	}
	return fmt.Sprintf("%dh%dm", int(d.Hours()), int(d.Minutes())%60)
}

// reset clears the anchor state for a new run.
func (s *taskAnchorState) reset(userPrompt string, startTime time.Time) {
	s.lastAnchoredAt = 0
	s.userPrompt = userPrompt
	s.runStartTime = startTime
}
