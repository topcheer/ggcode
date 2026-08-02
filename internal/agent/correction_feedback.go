package agent

import (
	"fmt"
	"strings"

	"github.com/topcheer/ggcode/internal/debug"
	"github.com/topcheer/ggcode/internal/provider"
)

// maybeInjectCorrectionFeedback checks whether the user has undone agent file
// changes since the last run (via /undo or /undo-run). If so, it injects an
// assistant-visible guidance message into the conversation so the agent knows
// its previous approach was rejected and should try a different strategy.
//
// This implements the "Undo as Negative Signal" pattern: user undo is the
// strongest implicit feedback an agent can receive. Without surfacing it,
// the agent has no awareness that its work was rejected and will repeat the
// same approach on the next run.
//
// Competitor analysis:
//   - Cursor: tracks per-suggestion accept/reject rates internally but does
//     not feed them back into the agent's context for the next turn.
//   - Claude Code: shows undo history in the UI but the agent itself is
//     unaware that its work was reverted.
//   - Cline/OpenHands: no undo feedback mechanism.
//
// ggcode is the first to surface undo feedback as an explicit in-context
// guidance message, creating a closed-loop correction signal.
//
// The correction is one-shot: once injected, corrections are cleared so they
// don't accumulate. Only corrections from the immediately preceding run are
// surfaced (older undos are stale context).
func (a *Agent) maybeInjectCorrectionFeedback() {
	a.mu.RLock()
	cpMgr := a.checkpoints
	a.mu.RUnlock()
	if cpMgr == nil {
		return
	}

	corrections := cpMgr.RecentCorrections()
	if len(corrections) == 0 {
		return
	}

	// Build a concise summary of what was undone.
	fileSet := make(map[string]bool)
	for _, c := range corrections {
		for _, f := range c.Files {
			fileSet[f] = true
		}
	}

	files := make([]string, 0, len(fileSet))
	for f := range fileSet {
		files = append(files, f)
	}

	var b strings.Builder
	b.WriteString("Note: The user reverted your previous file changes (via /undo or /undo-run).\n")
	b.WriteString("The following files were reverted to their pre-edit state:\n")
	// Cap at 10 files to avoid context bloat.
	for i, f := range files {
		if i >= 10 {
			b.WriteString(fmt.Sprintf("  ... and %d more\n", len(files)-10))
			break
		}
		b.WriteString(fmt.Sprintf("  - %s\n", f))
	}
	b.WriteString("\n")
	b.WriteString("This means your previous approach was likely wrong or unwanted. ")
	b.WriteString("Do NOT simply repeat the same changes. Instead:\n")
	b.WriteString("1. Re-read the files to understand their current (reverted) state\n")
	b.WriteString("2. Consider a fundamentally different approach\n")
	b.WriteString("3. If unsure why your changes were rejected, ask the user for clarification\n")

	msg := b.String()

	// Inject as a user message so it's visible to the model and distinct
	// from the user's actual request that follows.
	a.contextManager.Add(provider.Message{
		Role: "user",
		Content: []provider.ContentBlock{
			{Type: "text", Text: msg},
		},
	})

	debug.Log("agent", "injected correction feedback: %d corrections, %d unique files", len(corrections), len(files))

	// Clear corrections so they're one-shot -- only the most recent undo
	// batch is surfaced, not accumulated history.
	cpMgr.ClearCorrections()
}

// maybeInjectSentimentFeedback analyzes the user's latest message for negative
// feedback signals (frustration, rejection, redirection). When detected, it
// injects escalating course-correction guidance and resets monitoring state
// (overseer, repetition tracker, scope drift) so the agent starts fresh with
// the corrected approach.
//
// This complements maybeInjectCorrectionFeedback (which handles file reverts)
// by catching TEXTUAL negative feedback in the user's message itself.
func (a *Agent) maybeInjectSentimentFeedback(userPrompt string) {
	if a.userSentiment == nil {
		return
	}

	fb := a.userSentiment.analyzeAndUpdate(userPrompt)
	if fb.Level == 0 {
		return
	}

	guidance := buildSentimentGuidance(fb)
	if guidance == "" {
		return
	}

	a.contextManager.Add(provider.Message{
		Role: "user",
		Content: []provider.ContentBlock{
			{Type: "text", Text: guidance},
		},
	})

	debug.Log("agent", "%s", formatSentimentLogLine(fb))

	// Reset monitoring state when the user strongly rejects the approach.
	// The previous trajectory is now invalid -- those systems would carry
	// stale progress data from the rejected work.
	if shouldResetMonitoringOnFeedback(fb) {
		a.resetOverseer()
		a.resetScopeDrift()
		a.recurringError.reset()
		a.confidence.reset()
		a.resetPlanner()
		debug.Log("agent", "reset monitoring systems due to strong negative user feedback (level=%d)", fb.Level)
	}
}
