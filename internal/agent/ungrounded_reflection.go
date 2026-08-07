package agent

import (
	"strings"
	"sync"

	"github.com/topcheer/ggcode/internal/debug"
)

// ungroundedReflectionState tracks consecutive iterations where the agent
// produces substantial reasoning text without any tool calls. Research
// (2024-2025) shows intrinsic self-correction without external grounding
// (tool calls, test results) degrades performance. The agent gets stuck
// in "thinking loops" - re-deliberating without acting.
//
// References:
//   - EmergentMind "Reflection Agent: Self-Correcting AI" (2025)
//   - arXiv 2602.14798 "Overthinking Loops in Agents" (2025)
//   - Zylos "LLM-as-Judge in Production" (2026)
type ungroundedReflectionState struct {
	mu sync.Mutex

	// consecutiveTextOnly counts iterations where assistant emitted
	// substantial text (> ugrMinChars) with zero tool calls.
	consecutiveTextOnly int

	// totalWarnings keeps this bounded.
	totalWarnings int

	// firedOnce prevents re-warning every iteration after first fire.
	firedOnce bool
}

const (
	// After N consecutive text-only iterations, warn.
	ugrThreshold = 3

	// Minimum text length to count as "substantial reasoning".
	ugrMinChars = 200

	// Maximum warnings per run.
	ugrMaxWarnings = 2

	// Cooldown: extra consecutive iterations needed before re-warning.
	ugrCooldown = 4
)

// newUngroundedReflectionState creates a fresh detector.
func newUngroundedReflectionState() *ungroundedReflectionState {
	return &ungroundedReflectionState{}
}

// recordIteration tracks whether this iteration had tool calls and how much text.
// Returns a guidance message if ungrounded reflection is detected.
func (u *ungroundedReflectionState) recordIteration(iteration int, hasToolCalls bool, textLen int) string {
	u.mu.Lock()
	defer u.mu.Unlock()

	if hasToolCalls {
		u.consecutiveTextOnly = 0
		return ""
	}

	if textLen < ugrMinChars {
		return ""
	}

	u.consecutiveTextOnly++

	if u.consecutiveTextOnly < ugrThreshold {
		return ""
	}

	if u.firedOnce && u.consecutiveTextOnly < ugrThreshold+ugrCooldown {
		return ""
	}

	if u.totalWarnings >= ugrMaxWarnings {
		return ""
	}

	u.totalWarnings++
	u.firedOnce = true

	debug.Log("ungrounded-reflection", "iteration %d: %d consecutive text-only iterations without tool calls", iteration, u.consecutiveTextOnly)

	return u.buildMessage(u.consecutiveTextOnly)
}

func (u *ungroundedReflectionState) buildMessage(consecutive int) string {
	var sb strings.Builder
	sb.WriteString("[Ungrounded Reflection] You have produced ")
	if consecutive == ugrThreshold {
		sb.WriteString("several consecutive responses")
	} else {
		sb.WriteString("extended reasoning")
	}
	sb.WriteString(" with substantial text but NO tool calls in the last ")
	sb.WriteString(itoaUgr(consecutive))
	sb.WriteString(" iterations.\n\n")
	sb.WriteString("Research shows that intrinsic self-correction (thinking through it) without\n")
	sb.WriteString("external grounding degrades performance. You are likely stuck in an overthinking loop.\n\n")
	sb.WriteString("ACT NOW: Use a tool to make progress. Options:\n")
	sb.WriteString("- read_file / grep to verify assumptions against actual code\n")
	sb.WriteString("- edit_file to make the change you have been deliberating about\n")
	sb.WriteString("- run_command to test your hypothesis\n")
	sb.WriteString("- search_files / code_search to find what you need\n\n")
	sb.WriteString("Stop deliberating. Act.")
	return sb.String()
}

// reset clears state for a new user turn.
func (u *ungroundedReflectionState) reset() {
	u.mu.Lock()
	defer u.mu.Unlock()
	u.consecutiveTextOnly = 0
	u.totalWarnings = 0
	u.firedOnce = false
}

// itoaUgr is a minimal int-to-string without importing strconv.
func itoaUgr(n int) string {
	if n == 0 {
		return "0"
	}
	neg := n < 0
	if neg {
		n = -n
	}
	var buf [12]byte
	i := len(buf)
	for n > 0 {
		i--
		buf[i] = byte('0' + n%10)
		n /= 10
	}
	if neg {
		i--
		buf[i] = '-'
	}
	return string(buf[i:])
}
