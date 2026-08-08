package agent

import (
	"strings"

	"github.com/topcheer/ggcode/internal/debug"
	"github.com/topcheer/ggcode/internal/provider"
)

// Context Anchor Reinforcement -- Positional Attention Decay Mitigation
//
// Research basis:
//   - "Lost in the Middle: How Language Models Use Long Contexts" (Liu et al.,
//     2024 / TACL): LLMs exhibit a U-shaped attention curve -- they attend
//     strongly to the beginning (system prompt + user task) and end (most
//     recent tool results) of the context window, but content in the middle
//     receives significantly less attention regardless of importance.
//   - "Lost in the Middle: An Emergent Property from Information Retrieval
//     Demands" (Veseli et al., 2025, arXiv:2510.10276): confirms that
//     lost-in-the-middle is an emergent property driven by information
//     retrieval demands during pre-training; it persists even in
//     long-context models and agentic settings.
//   - "RULER: What's the Real Context Size of Your Long-Context Language
//     Models?" (Hsieh et al., 2024): effective context is far smaller than
//     nominal window size; critical instructions get "forgotten" as the
//     conversation grows.
//
// The gap: ggcode has detectors that monitor context BUDGET (how many tokens
// remain) and CONTEXT FOOTPRINT (which tools consume the most tokens), but
// NONE address ATTENTIONAL DECAY -- the phenomenon where the original user
// task, active constraints, and accumulated requirements "drift" into the
// low-attention middle zone as tool results accumulate. The agent may still
// have budget but has effectively "forgotten" key requirements because they're
// buried under layers of tool output.
//
// This detector:
//  1. Monitors context depth as a proxy for attentional risk (token count +
//     iteration count relative to the context window)
//  2. When the original user task is deep in the "middle zone" (not recent
//     enough to be in the recency-attention peak, but early enough that
//     primacy effects have faded), it periodically re-anchors the task by
//     re-injecting a concise reminder at the END of the context (the
//     high-attention recency zone).
//
// Key distinction from existing detectors:
//   - budget_guard: warns when tokens are RUNNING OUT (scarcity)
//   - context_footprint_tracker: identifies which tools waste the most tokens
//   - plan_drift_detection: detects scope creep / deviation from plan
//   - This detector: counters POSITIONAL ATTENTION DECAY by re-injecting
//     critical task context into the high-attention recency zone
//
// The re-anchoring is lightweight (single concise message), fires at most
// twice per run, and only when the context has grown large enough for
// positional decay to be a real risk.

const (
	maxAnchorWarnings   = 2    // fire at most twice per run
	anchorMinIterGap    = 4    // minimum iterations between anchor reinforcements
	anchorMinUsageRatio = 0.35 // only anchor when context is >35% full (middle-zone risk)
	anchorMinIter       = 5    // need at least 5 iterations before positional decay is plausible
)

// anchorState tracks context anchor reinforcement across a run.
type anchorState struct {
	fired         int    // number of times anchor has been reinforced this run
	lastFireIter  int    // iteration when anchor was last fired (0 = never)
	userTask      string // cached original user task text
	taskExtracted bool   // whether we've attempted to extract the user task
}

func newAnchorState() *anchorState {
	return &anchorState{}
}

func (a *anchorState) reset() {
	a.fired = 0
	a.lastFireIter = 0
	a.userTask = ""
	a.taskExtracted = false
}

// extractUserTask pulls a concise summary of the original user task from the
// context manager's message history. It looks for the first user message
// (after the system prompt) and truncates it for re-injection.
func (a *anchorState) extractUserTask(msgs []provider.Message) string {
	if a.taskExtracted {
		return a.userTask
	}
	a.taskExtracted = true

	for _, m := range msgs {
		if m.Role != "user" {
			continue
		}
		text := extractTextFromContent(m.Content)
		if strings.TrimSpace(text) == "" {
			continue
		}
		// Skip system-injected messages (they start with brackets)
		if strings.HasPrefix(strings.TrimSpace(text), "[") {
			continue
		}
		// Truncate to a reasonable length for re-injection
		task := strings.TrimSpace(text)
		if len(task) > 500 {
			task = task[:497] + "..."
		}
		a.userTask = task
		break
	}
	return a.userTask
}

// extractTextFromContent concatenates text blocks from content.
func extractTextFromContent(blocks []provider.ContentBlock) string {
	var sb strings.Builder
	for _, blk := range blocks {
		if blk.Type == "text" {
			sb.WriteString(blk.Text)
		}
	}
	return sb.String()
}

// checkAnchorReinforcement evaluates whether a context anchor re-injection
// is needed based on current context depth and iteration progress.
//
// Parameters:
//   - currentIter: 1-based current iteration number
//   - maxIter: maximum iteration budget
//   - tokenCount: current context token count
//   - contextWindow: total context window size
//   - msgs: message history for extracting the original user task
//
// Returns a non-empty guidance message if re-anchoring should occur.
func (a *anchorState) checkAnchorReinforcement(
	currentIter int,
	tokenCount, contextWindow int,
	msgs []provider.Message,
) string {
	if a.fired >= maxAnchorWarnings {
		return ""
	}

	// Need enough iterations for positional decay to be plausible
	if currentIter < anchorMinIter {
		return ""
	}

	// Need enough gap since last fire
	if a.lastFireIter > 0 && currentIter-a.lastFireIter < anchorMinIterGap {
		return ""
	}

	// Only anchor when context is large enough for middle-zone risk
	if contextWindow <= 0 {
		return ""
	}
	usageRatio := float64(tokenCount) / float64(contextWindow)
	if usageRatio < anchorMinUsageRatio {
		return ""
	}

	// Extract the user task if not done yet
	userTask := a.extractUserTask(msgs)
	if userTask == "" {
		return ""
	}

	a.fired++
	a.lastFireIter = currentIter
	debug.Log("context-anchor",
		"Iteration %d: re-anchoring user task (usage=%.0f%%, tokens=%d, fire #%d)",
		currentIter, usageRatio*100, tokenCount, a.fired)

	return "[Context Anchor] As context grows, your original task may receive less attention " +
		"(U-shaped attention curve, Liu et al. 2024). Re-confirming the core objective:\n" +
		"> " + truncateTaskForDisplay(userTask, 300) + "\n" +
		"Verify your current work aligns with this goal. If you've drifted from the original " +
		"request, re-focus. If you've completed all parts of this task, confirm completion."
}

// truncateTaskForDisplay shortens text for injection, adding ellipsis.
func truncateTaskForDisplay(s string, maxLen int) string {
	if len(s) <= maxLen {
		return s
	}
	return s[:maxLen-3] + "..."
}
