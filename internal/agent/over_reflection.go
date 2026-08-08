package agent

import (
	"fmt"
	"strings"
	"sync"
	"unicode"

	"github.com/topcheer/ggcode/internal/debug"
)

// overReflectionDetector tracks the ratio of reasoning/text output to
// concrete tool-call action across consecutive iterations. When an agent
// produces multiple consecutive turns of substantial text without issuing
// any actionable tool calls, it is "over-reflecting" -- spending test-time
// compute on deliberation rather than progress.
//
// Research basis: arXiv:2506.12928 "Scaling Test-time Compute for LLM Agents"
// (Zhu et al. 2025) -- finding #2: "Knowing when to reflect is important for
// agents." The paper demonstrates that agents often waste test-time compute
// on unnecessary self-reflection. Agent-R (arXiv:2503.07573) and SICA
// (arXiv:2504.15228) further document "analysis paralysis" -- the agent loops
// in deliberation mode without advancing toward the goal.
//
// Detection logic:
//   - Tracks consecutive "text-heavy" iterations where the assistant produced
//     substantial text (>textHeavyThreshold words) but NO tool calls.
//   - An iteration counts as "action" if it contains one or more tool calls
//     OR produces very little text (a transition, not deliberation).
//   - When consecutive text-heavy-no-action iterations reach warnThreshold,
//     guidance is injected to push the agent toward concrete action.
//   - At severeThreshold, a stronger message is emitted.
//
// This is distinct from existing detectors:
//   - analysisParalysisState: tool-call imbalance (too many reads, no writes)
//   - reasoningBlockCompaction: context size management of old reasoning
//   - adaptiveEffort: adjusts reasoning depth per tool type
//   - prematureSurrender: detecting give-up language
//   - toolStorm: too many tools without enough reasoning (inverse problem)
//
// This detector addresses a unique gap: the agent produces TEXT/REASONING
// without ANY tool calls -- pure deliberation with no action.

const (
	// textHeavyThreshold is the minimum word count for text to be considered
	// "substantial deliberation" (not just a brief transition phrase).
	textHeavyThreshold = 80

	// reflectionWarnThreshold is the number of consecutive text-heavy,
	// no-tool-call iterations before a gentle nudge toward action.
	reflectionWarnThreshold = 3

	// reflectionSevereThreshold triggers a stronger intervention.
	reflectionSevereThreshold = 5
)

type overReflectionDetector struct {
	mu sync.Mutex

	// consecutiveTextHeavy counts iterations in a row where the agent produced
	// substantial text but no tool calls.
	consecutiveTextHeavy int

	// firedWarn tracks whether the warn-level guidance has fired.
	firedWarn bool

	// firedSevere tracks whether the severe-level guidance has fired.
	firedSevere bool

	// maxConsecutiveSeen tracks the peak stagnation for debug logging.
	maxConsecutiveSeen int
}

func newOverReflectionDetector() *overReflectionDetector {
	return &overReflectionDetector{}
}

// recordIteration is called once per agent loop iteration.
// text is the assistant text output for this iteration.
// hasToolCalls indicates whether the iteration included any tool calls.
// iterNum is the 1-based iteration number (for logging).
// Returns guidance text if intervention is needed, "" otherwise.
func (d *overReflectionDetector) recordIteration(text string, hasToolCalls bool, iterNum int) string {
	d.mu.Lock()
	defer d.mu.Unlock()

	wordCount := countWordsInText(text)
	isTextHeavy := wordCount >= textHeavyThreshold && !hasToolCalls

	if isTextHeavy {
		d.consecutiveTextHeavy++
		if d.consecutiveTextHeavy > d.maxConsecutiveSeen {
			d.maxConsecutiveSeen = d.consecutiveTextHeavy
		}
	} else {
		// Reset on any action or short text.
		d.consecutiveTextHeavy = 0
	}

	// Check thresholds -- each level fires once.
	if d.consecutiveTextHeavy >= reflectionSevereThreshold && !d.firedSevere {
		d.firedSevere = true
		debug.Log("over-reflection", "Iteration %d: %d consecutive text-heavy no-action turns (severe)", iterNum, d.consecutiveTextHeavy)
		return fmt.Sprintf(
			"Over-reflection detected: You have spent %d consecutive turns producing text without any tool calls or concrete actions. "+
				"STOP planning and ACT NOW:\n"+
				"1. Pick the single most impactful next action and execute it immediately\n"+
				"2. If you are stuck, state the specific blocker in one sentence -- do not write paragraphs about it\n"+
				"3. Make your best-guess edit and verify with a build or test, rather than continuing to analyze\n"+
				"(Test-time compute research shows over-reflection without action degrades agent performance -- arXiv:2506.12928)",
			d.consecutiveTextHeavy,
		)
	}

	if d.consecutiveTextHeavy >= reflectionWarnThreshold && !d.firedWarn {
		d.firedWarn = true
		debug.Log("over-reflection", "Iteration %d: %d consecutive text-heavy no-action turns (warning)", iterNum, d.consecutiveTextHeavy)
		return fmt.Sprintf(
			"Over-reflection warning: %d consecutive turns with substantial text output but no tool calls. "+
				"You may be over-thinking. Translate your next insight into a concrete action (edit, build, test) rather than another paragraph of analysis.",
			d.consecutiveTextHeavy,
		)
	}

	return ""
}

// reset clears all tracking state for a new run.
func (d *overReflectionDetector) reset() {
	d.mu.Lock()
	defer d.mu.Unlock()
	d.consecutiveTextHeavy = 0
	d.firedWarn = false
	d.firedSevere = false
	d.maxConsecutiveSeen = 0
}

// countWordsInText returns the number of whitespace-separated words in s.
// Uses unicode.IsSpace for cross-platform whitespace handling.
func countWordsInText(s string) int {
	s = strings.TrimSpace(s)
	if s == "" {
		return 0
	}
	count := 0
	inWord := false
	for _, r := range s {
		if unicode.IsSpace(r) {
			if inWord {
				count++
				inWord = false
			}
		} else {
			inWord = true
		}
	}
	if inWord {
		count++
	}
	return count
}

// --- Agent integration ---

// maybeWarnOverReflection checks if the agent is stuck in over-reflection
// mode -- producing text without tool calls. Called from the agent loop after
// capturing assistantText and checking for tool calls.
func (a *Agent) maybeWarnOverReflection(text string, hasToolCalls bool, iterNum int) string {
	if a.overReflection == nil {
		return ""
	}
	return a.overReflection.recordIteration(text, hasToolCalls, iterNum)
}

// resetOverReflection clears tracking for a new run.
func (a *Agent) resetOverReflection() {
	if a.overReflection == nil {
		return
	}
	a.overReflection.reset()
}
