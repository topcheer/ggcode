package agent

// User Sentiment Detection - Negative Feedback Course Correction
//
// Research basis: Studies in conversational AI and coding assistant UX show that
// detecting user frustration and corrective signals is critical for maintaining
// trust and avoiding the "repeated wrong approach" failure mode. Key references:
//
//   - Lai et al. "User Reactions to Coding Assistants" (CHI 2024): Users who
//     express frustration through short corrective messages ("no", "wrong",
//     "stop") are 3x more likely to abandon the tool if it doesn't adjust.
//   - Devin/SICA overseer (arXiv:2504.15228): mentions "user satisfaction
//     monitoring" as a key signal for when to reset agent state.
//   - Cursor's telemetry tracks implicit signals (accept/reject rates) but
//     does not analyze explicit user message sentiment at runtime.
//   - Claude Code: no runtime sentiment detection; relies on system prompt
//     guidance only.
//
// Existing ggcode systems that overlap but don't cover this gap:
//   - correction_feedback.go: detects FILE REVERTS (undo) as negative signal,
//     but not TEXTUAL feedback in user messages.
//   - repetition_tracker.go: tracks repeated TOOL CALLS, not user frustration.
//   - overseer.go: monitors agent trajectory shape, not user sentiment.
//   - convergence_lock.go: prevents premature convergence, but doesn't detect
//     when the user is telling the agent it's on the wrong track.
//
// This module fills the gap with deterministic, zero-LLM-cost detection:
//
//   1. NEGATIVE FEEDBACK DETECTION: analyzes each user message for patterns
//      indicating the agent's previous output was wrong, unwanted, or the user
//      is frustrated. Uses keyword matching + message brevity heuristics.
//
//   2. ESCALATION TRACKING: tracks how many consecutive negative messages the
//      user has sent. First detection → gentle guidance. Second → stronger.
//      Third+ → force the agent to stop and ask for clarification.
//
//   3. STATE RESET: when strong negative feedback is detected, resets the
//      monitoring systems (overseer, repetition tracker, etc.) because the
//      user's correction invalidates the previous trajectory - those systems
//      would otherwise carry stale "progress" data from the rejected approach.
//
// All operations are pure string matching and counter increments - no I/O,
// no blocking, no external dependencies.

import (
	"bytes"
	"fmt"
	"strings"
	"sync"

	"github.com/topcheer/ggcode/internal/debug"
)

const (
	// sentimentEscalationSoft: first negative message → gentle course correction.
	sentimentEscalationSoft = 1
	// sentimentEscalationStrong: second consecutive negative → strong guidance.
	sentimentEscalationStrong = 2
	// sentimentEscalationMax: third+ → force stop and ask clarification.
	sentimentEscalationMax = 3
)

// userSentimentState tracks user feedback patterns across runs.
type userSentimentState struct {
	mu sync.Mutex

	// consecutiveNegatives counts consecutive user messages that express
	// negative feedback. Reset to 0 when a non-negative message is received.
	consecutiveNegatives int

	// totalCorrections tracks the total number of negative feedback events
	// in the session (never reset). Used for session-level analytics.
	totalCorrections int

	// lastDetectedCategory remembers what type of negative feedback was
	// detected most recently, so guidance can be tailored.
	lastDetectedCategory string
}

func newUserSentimentState() *userSentimentState {
	return &userSentimentState{}
}

func (s *userSentimentState) reset() {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.consecutiveNegatives = 0
}

// NegFeedbackCategory classifies the type of negative feedback detected.
const (
	negCatRejection   = "rejection"   // "no", "wrong", "not what I wanted"
	negCatFrustration = "frustration" // "stop", "ugh", "this is broken"
	negCatRedirection = "redirection" // "instead", "actually", "I meant"
)

// sentimentPositiveWhitelist lists phrases that look negative to the word
// matcher but are actually positive/neutral acknowledgements (#1223).
// "no problem" / "no worries" / "not bad" all contain bare rejection words
// ("no", "bad") yet mean the opposite; technical how-to questions like
// "how do I stop the daemon" contain "stop" without any frustration intent.
// A first line containing any of these phrases is never classified as
// negative feedback.
var sentimentPositiveWhitelist = []string{
	"no problem", "no worries", "no issues", "no issue",
	"not bad", "not too bad", "looks good", "looks great", "looks fine",
	"all good", "well done", "good job", "nice work", "great work",
	"perfect", "excellent", "awesome", "thank you", "thanks",
	"how do i stop", "how to stop", "how can i stop", "how do you stop",
	"how do i restart", "how to restart",
}

// negFeedbackPatterns maps patterns to categories. Matching is case-insensitive
// and checks for word boundaries to avoid false positives (e.g., "now" matching
// "no"). Patterns are ordered by priority - earlier matches take precedence.
var negFeedbackPatterns = []struct {
	patterns []string
	category string
}{
	// Rejection: direct negation of agent's work.
	// #1223: bare "no"/"bad" remain here deliberately - the positive whitelist
	// above neutralizes the opposite-meaning phrases ("no problem", "not bad")
	// before these patterns are consulted.
	{[]string{"no", "nope", "wrong", "incorrect", "not right", "not what", "not correct",
		"that's wrong", "thats wrong", "this is wrong", "bad", "doesn't work",
		"doesnt work", "didnt work", "didn't work", "broken", "still broken",
		"still failing", "still not working", "still doesn't", "still doesnt",
		"not working", "fails", "failing", "error again"},
		negCatRejection},
	// Redirection: user changing direction.
	{[]string{"instead", "actually", "i meant", "i should have", "let me rephrase",
		"let me clarify", "what i actually", "what i really", "on second thought",
		"never mind", "nevermind", "ignore that", "forget that", "disregard",
		"start over", "try again", "redo"},
		negCatRedirection},
	// Frustration: emotional signals.
	// #1223: "why are you" narrowed to "why are you doing"/"why do you keep" -
	// the generic form matched technical questions like "why are you using a
	// mutex here?". How-to-stop questions are whitelist phrases above.
	{[]string{"stop", "ugh", "wtf", "this is frustrating", "frustrating",
		"why are you doing", "why do you keep", "why do you still",
		"you keep", "stop doing", "stop trying",
		"i said", "as i said", "like i said", "i already told",
		"are you listening", "read my message", "pay attention",
		"this doesn't make sense", "this doesnt make sense",
		"nonsense", "garbage", "useless", "terrible"},
		negCatFrustration},
}

// detectNegativeFeedback analyzes a user message and returns the feedback
// category (empty string if no negative feedback detected).
//
// Heuristics:
//  1. SHORT MESSAGE + REJECTION/FRUSTRATION WORD: a very short message
//     (<100 chars) containing a rejection or frustration keyword is a strong
//     negative signal. Short messages are more likely to be reactive feedback.
//  2. REDIRECTION KEYWORDS anywhere: these indicate course correction
//     regardless of message length.
//  3. MULTI-LINE messages are NOT treated as pure negative feedback -
//     the user is likely providing detailed context, even if they express
//     some frustration. Only treat as negative if the FIRST line is a
//     clear rejection/frustration signal.
func detectNegativeFeedback(message string) string {
	message = strings.TrimSpace(message)
	if message == "" {
		return ""
	}

	// Analyze only the first line for multi-line messages - the first line
	// captures the reactive sentiment; subsequent lines usually contain context.
	firstLine := message
	if idx := strings.IndexByte(message, '\n'); idx >= 0 {
		firstLine = strings.TrimSpace(message[:idx])
	}

	lower := strings.ToLower(firstLine)
	isShort := len(firstLine) < 100

	// Positive-phrase masking (#1223, refined by #1227): whitelist phrases
	// like "no problem" / "not bad" contain bare rejection words but express
	// satisfaction. Rather than short-circuiting the whole line - which hid
	// genuine negative signals in mixed messages like "thanks, but still
	// broken" and wrongly reset the escalation counter - the matched phrases
	// are blanked out and the negative patterns then scan what remains.
	scanText := maskPositivePhrases(lower)

	// Check patterns in priority order.
	for _, group := range negFeedbackPatterns {
		for _, pat := range group.patterns {
			if wordContains(scanText, pat) {
				// All categories only trigger for short messages to avoid
				// false positives in detailed technical messages. A long
				// message with "actually" mid-sentence is context, not
				// a course-correction signal.
				if isShort {
					return group.category
				}
			}
		}
	}

	return ""
}

// maskPositivePhrases blanks out word-bounded occurrences of every
// whitelist phrase in text, returning the text with those spans replaced by
// spaces. Blanking keeps surrounding word boundaries intact (spaces are
// non-alphanumeric), so leftover negative-signal words are still matched by
// wordContains on the returned text (#1227).
func maskPositivePhrases(text string) string {
	b := []byte(text)
	for _, w := range sentimentPositiveWhitelist {
		for {
			idx := wordIndexIn(b, w)
			if idx < 0 {
				break
			}
			for i := idx; i < idx+len(w); i++ {
				b[i] = ' '
			}
		}
	}
	return string(b)
}

// wordIndexIn returns the start of the first word-bounded occurrence of
// pattern in b, or -1. Boundary semantics mirror wordContains.
func wordIndexIn(b []byte, pattern string) int {
	pb := []byte(pattern)
	for from := 0; from+len(pb) <= len(b); {
		rel := bytes.Index(b[from:], pb)
		if rel < 0 {
			return -1
		}
		idx := from + rel
		beforeOK := idx == 0 || !isAlnum(b[idx-1])
		afterIdx := idx + len(pb)
		afterOK := afterIdx >= len(b) || !isAlnum(b[afterIdx])
		if beforeOK && afterOK {
			return idx
		}
		from = idx + 1
	}
	return -1
}

// wordContains checks if text contains the pattern as a standalone word
// (bounded by non-alphanumeric characters or string boundaries). This prevents
// false positives like "now" matching "no".
func wordContains(text, pattern string) bool {
	idx := strings.Index(text, pattern)
	for idx >= 0 {
		// Check character before.
		beforeOK := idx == 0 || !isAlnum(text[idx-1])
		// Check character after.
		afterIdx := idx + len(pattern)
		afterOK := afterIdx >= len(text) || !isAlnum(text[afterIdx])
		if beforeOK && afterOK {
			return true
		}
		// Search for next occurrence.
		next := strings.Index(text[idx+1:], pattern)
		if next < 0 {
			break
		}
		idx = idx + 1 + next
	}
	return false
}

func isAlnum(b byte) bool {
	return (b >= 'a' && b <= 'z') || (b >= 'A' && b <= 'Z') || (b >= '0' && b <= '9') || b == '\''
}

// SentimentFeedback represents the result of sentiment analysis for a single
// user message. If Level is 0, no negative feedback was detected.
type SentimentFeedback struct {
	Level    int    // 0=none, 1=soft, 2=strong, 3=max
	Category string // rejection, frustration, redirection, or empty
}

// analyzeAndUpdate checks the user message for negative feedback and updates
// internal state. Returns the current escalation level and category.
func (s *userSentimentState) analyzeAndUpdate(message string) SentimentFeedback {
	s.mu.Lock()
	defer s.mu.Unlock()

	category := detectNegativeFeedback(message)
	if category == "" {
		// Non-negative message resets the consecutive counter.
		s.consecutiveNegatives = 0
		return SentimentFeedback{Level: 0}
	}

	s.consecutiveNegatives++
	s.totalCorrections++
	s.lastDetectedCategory = category

	level := s.consecutiveNegatives
	if level > sentimentEscalationMax {
		level = sentimentEscalationMax
	}

	debug.Log("agent", "negative user feedback detected: category=%s consecutive=%d level=%d",
		category, s.consecutiveNegatives, level)

	return SentimentFeedback{Level: level, Category: category}
}

// buildSentimentGuidance generates the guidance message to inject when negative
// feedback is detected. The guidance is tailored to the escalation level and
// feedback category.
func buildSentimentGuidance(fb SentimentFeedback) string {
	if fb.Level == 0 {
		return ""
	}

	var b strings.Builder

	switch fb.Level {
	case sentimentEscalationSoft:
		b.WriteString("The user's last message indicates your previous approach may have been incorrect or unwanted")
		b.WriteString(tailoredSuffix(fb.Category, false))
		b.WriteString("\n\nDo NOT repeat your previous approach. ")
		b.WriteString("Re-read the relevant files to understand their current state, ")
		b.WriteString("and consider a different strategy.")

	case sentimentEscalationStrong:
		b.WriteString("The user has expressed negative feedback multiple times in a row. ")
		b.WriteString("Your recent work is clearly not meeting expectations")
		b.WriteString(tailoredSuffix(fb.Category, false))
		b.WriteString("\n\nSTOP and reassess:\n")
		b.WriteString("1. Re-read the user's ORIGINAL request carefully\n")
		b.WriteString("2. Re-read all files you modified to understand the current state\n")
		b.WriteString("3. Identify what specifically went wrong in your previous approach\n")
		b.WriteString("4. Propose a fundamentally different solution")

	case sentimentEscalationMax:
		b.WriteString("The user has expressed repeated frustration. ")
		b.WriteString("Do NOT make any more changes without first confirming your understanding.\n\n")
		b.WriteString("Use ask_user to:\n")
		b.WriteString("- Summarize what you THINK the user wants\n")
		b.WriteString("- Explain what went wrong with your previous attempts\n")
		b.WriteString("- Ask for explicit confirmation before proceeding\n")
		b.WriteString("Do not continue making edits until the user confirms.")
	}

	return b.String()
}

// tailoredSuffix adds category-specific context to the guidance message.
func tailoredSuffix(category string, _ bool) string {
	switch category {
	case negCatRejection:
		return " (rejection of output)."
	case negCatFrustration:
		return " (frustration signal)."
	case negCatRedirection:
		return " (direction change)."
	default:
		return "."
	}
}

// shouldResetMonitoringOnFeedback returns true when the negative feedback level
// is strong enough to warrant resetting agent monitoring state.
//
// Rationale: the overseer, repetition tracker, scope drift, and other monitoring
// systems accumulate trajectory data across iterations. When the user explicitly
// rejects the agent's approach, that trajectory data becomes stale - it reflects
// work that was wrong. Resetting ensures these systems start fresh with the
// corrected approach, avoiding false "drift" or "stuck" alerts triggered by
// the now-irrelevant previous trajectory.
func shouldResetMonitoringOnFeedback(fb SentimentFeedback) bool {
	return fb.Level >= sentimentEscalationStrong
}

// formatSentimentLogLine produces a debug log line for the feedback event.
func formatSentimentLogLine(fb SentimentFeedback) string {
	return fmt.Sprintf("sentiment feedback: level=%d category=%s", fb.Level, fb.Category)
}
