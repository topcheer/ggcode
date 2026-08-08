package agent

import (
	"fmt"
	"regexp"
	"strings"
	"sync"

	"github.com/topcheer/ggcode/internal/debug"
)

// Subgoal Completion Integrity Detector -- Missing-Step Planning Failure Awareness
//
// Research basis:
//   - "Taxonomy of Failure Mode in Agentic AI Systems" (arXiv:2508.13143,
//     Microsoft, 2025): three-tier taxonomy identifies "missing steps" under
//     planning errors as a top failure cause. Agents declare multi-step plans
//     but skip individual subgoals without acknowledgment, leading to
//     incomplete deliverables.
//   - "Reducing Cost of LLM Agents with Trajectory Reduction" (arXiv:2509.23586):
//     trajectory analysis shows agents waste iterations on unrelated work
//     while stated subgoals remain unaddressed -- a token-efficiency failure.
//   - "Exploring Autonomous Agents: Why They Fail" (arXiv:2508.13143):
//     planning errors (including step omission) account for a significant
//     fraction of agent failures in multi-step software engineering tasks.
//
// The gap: ggcode has planAbandon (entire plan abandoned), fulfillmentGate
// (user request vs work match), and inputUnderspec (vague request detection).
// NONE track INDIVIDUAL subgoals within a multi-step plan. An agent that says
// "1. Fix auth 2. Update tests 3. Update docs" but only does steps 1 and 2
// won't trigger any existing detector until fulfillmentGate fires at the end.
// This detector provides EARLY awareness when subgoals are being skipped
// mid-run, giving the agent a chance to course-correct.
//
// Design:
//  1. Extracts numbered subgoals from assistant text (e.g., "1. Fix auth")
//  2. Derives keyword signatures from each subgoal
//  3. Tracks whether subsequent tool calls reference those keywords
//  4. After enough iterations pass with unaddressed subgoals, warns
//  5. Non-blocking, advisory, zero LLM cost
//  6. Fires at most once per run to avoid noise

const (
	sgMaxSubgoals       = 8   // max subgoals to track (avoid noise from long lists)
	sgMinSubgoals       = 3   // only track plans with 3+ items
	sgMinIterGap        = 3   // min iterations before checking coverage
	sgUnaddressedThresh = 0.4 // warn if >40% of subgoals are unaddressed
	sgMaxWarns          = 1   // fire at most once per run
)

// subgoalEntry tracks a single extracted subgoal.
type subgoalEntry struct {
	number    int
	text      string
	keywords  []string
	addressed bool
}

// subgoalState tracks multi-step plan subgoals across a run.
type subgoalState struct {
	mu       sync.Mutex
	subgoals []subgoalEntry
	planIter int  // iteration when the plan was declared
	fired    bool // whether the warning has been issued
}

func newSubgoalState() *subgoalState {
	return &subgoalState{}
}

func (s *subgoalState) reset() {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.subgoals = nil
	s.planIter = 0
	s.fired = false
}

// numberedItemRe matches lines like "1. Fix the auth module" or "2) Update tests".
var numberedItemRe = regexp.MustCompile(`(?m)^\s*(\d+)[.)]\s+(.{3,80})$`)

// extractSubgoals parses numbered list items from agent text.
// Returns at most sgMaxSubgoals entries; requires sgMinSubgoals items to activate.
func extractSubgoals(text string) []subgoalEntry {
	matches := numberedItemRe.FindAllStringSubmatch(text, sgMaxSubgoals+2)
	if len(matches) < sgMinSubgoals {
		return nil
	}
	var result []subgoalEntry
	for _, m := range matches {
		num := 0
		fmt.Sscanf(m[1], "%d", &num)
		desc := strings.TrimSpace(m[2])
		// Skip non-action items (e.g., "see above", "etc.")
		if len(desc) < 5 {
			continue
		}
		result = append(result, subgoalEntry{
			number:   num,
			text:     desc,
			keywords: extractSubgoalKeywords(desc),
		})
	}
	if len(result) < sgMinSubgoals {
		return nil
	}
	if len(result) > sgMaxSubgoals {
		result = result[:sgMaxSubgoals]
	}
	return result
}

// extractKeywords derives lowercase keyword tokens from a subgoal description.
// Filters common English stop words and short tokens.
var sgStopWords = map[string]bool{
	"the": true, "a": true, "an": true, "to": true, "of": true, "in": true,
	"for": true, "and": true, "or": true, "on": true, "at": true, "by": true,
	"is": true, "it": true, "this": true, "that": true, "with": true,
	"from": true, "be": true, "will": true, "was": true, "are": true,
	"as": true, "if": true, "we": true, "i": true, "you": true,
	"fix": true, "update": true, "add": true, "create": true, "check": true,
	"make": true, "set": true, "ensure": true, "do": true,
}

func extractSubgoalKeywords(desc string) []string {
	// Split on non-alphanumeric
	tokens := regexp.MustCompile(`[a-zA-Z][a-zA-Z0-9_-]{2,}`).FindAllString(desc, -1)
	var kws []string
	seen := make(map[string]bool)
	for _, t := range tokens {
		lower := strings.ToLower(t)
		if sgStopWords[lower] || seen[lower] {
			continue
		}
		seen[lower] = true
		kws = append(kws, lower)
	}
	return kws
}

// recordAssistantText captures the plan from agent text (only the first
// numbered list with enough items).
func (s *subgoalState) recordAssistantText(text string, iter int) {
	s.mu.Lock()
	defer s.mu.Unlock()
	// Only capture the first plan
	if len(s.subgoals) > 0 {
		return
	}
	subs := extractSubgoals(text)
	if len(subs) >= sgMinSubgoals {
		s.subgoals = subs
		s.planIter = iter
		debug.Log("agent", "subgoal_track: plan detected at iter %d with %d subgoals", iter, len(subs))
	}
}

// recordToolCall checks whether a tool call addresses any unaddressed subgoal.
func (s *subgoalState) recordToolCall(toolName, args string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if len(s.subgoals) == 0 {
		return
	}
	argLower := strings.ToLower(args)
	toolLower := strings.ToLower(toolName)
	for i := range s.subgoals {
		if s.subgoals[i].addressed {
			continue
		}
		for _, kw := range s.subgoals[i].keywords {
			if strings.Contains(argLower, kw) || strings.Contains(toolLower, kw) {
				s.subgoals[i].addressed = true
				break
			}
		}
	}
}

// maybeWarn checks if unaddressed subgoals warrant a warning.
func (s *subgoalState) maybeWarn(currentIter int) string {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.fired || len(s.subgoals) < sgMinSubgoals {
		return ""
	}
	// Need enough iterations to have had a chance to address subgoals
	if currentIter-s.planIter < sgMinIterGap {
		return ""
	}
	unaddressed := 0
	var missing []string
	for _, sg := range s.subgoals {
		if !sg.addressed {
			unaddressed++
			missing = append(missing, fmt.Sprintf("  %d. %s", sg.number, truncate(sg.text, 60)))
		}
	}
	ratio := float64(unaddressed) / float64(len(s.subgoals))
	if ratio < sgUnaddressedThresh {
		return ""
	}
	s.fired = true
	debug.Log("agent", "subgoal_track: %d/%d subgoals unaddressed at iter %d", unaddressed, len(s.subgoals), currentIter)
	return fmt.Sprintf(
		"[Subgoal Completion] %d of %d planned subgoals have received no tool activity:\n%s\n"+
			"Research shows missing-step planning errors are a top agent failure cause (arXiv:2508.13143). "+
			"Review whether these subgoals are still needed. If so, address them before declaring completion. "+
			"If requirements changed, explicitly acknowledge which subgoals were intentionally deferred.",
		unaddressed, len(s.subgoals), strings.Join(missing, "\n"))
}

func truncate(s string, max int) string {
	if len(s) <= max {
		return s
	}
	return s[:max-3] + "..."
}
