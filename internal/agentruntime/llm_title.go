package agentruntime

// LLM title refinement — optional semantic upgrade over the deterministic
// heuristics in auto_title.go.
//
// Research basis: episodic-memory benchmarks model memory systems as
// indexing/retrieval/reading (LongMemEval, ICLR'25 —
// https://xiaowu0162.github.io/long-mem-eval/). The /sessions list is the
// human-facing INDEX over ggcode's episodic memory, and index quality
// depends on titles that describe the TASK, not the literal first message.
// The auto_title.go heuristics clean formatting but cannot infer intent:
// paste-heavy openers (stack traces, code dumps, "帮我看看这个") still yield
// weak titles after cleaning. Industry practice (ChatGPT, Claude Code,
// Cursor) uses a cheap side LLM call for exactly this high-frequency,
// tiny-output job — the "heterogeneous model tiers" pattern where cheap
// models absorb high-frequency auxiliary tasks (FinOps for agents,// machinelearningmastery.com 7 Agentic AI Trends 2026, trend 6).
//
// Design constraints (auto_title.go's zero-cost decision remains the
// DEFAULT path — this module only runs when explicitly enabled):
//   - opt-in via config auto_title_llm (default off)
//   - the TUI fires it on at most the first two assistant turns
//   - only when the heuristic pipeline left an empty or generic title
//   - fail-open: any provider error keeps the heuristic title
//   - LLM output is sanitized by deterministic code before it is applied

import (
	"strings"
	"unicode/utf8"
)

const (
	// llmTitleUserExcerptRunes caps how much of the first user message is
	// sent to the title call. Long enough for a stack trace + task line,
	// short enough to keep the side call cheap.
	llmTitleUserExcerptRunes = 1200
	// llmTitleAssistantExcerptRunes caps the first assistant reply excerpt;
	// the reply often names what was actually worked on.
	llmTitleAssistantExcerptRunes = 800
)

// TitleWantsLLMRefine reports whether a session title is weak enough that
// the optional LLM refinement should be attempted: the deterministic
// pipeline produced nothing (empty/placeholder) or something generic
// ("hi", "test", a bare file basename...). Called by the TUI after the
// heuristic refine pass, so a strong heuristic title never costs a call.
func TitleWantsLLMRefine(currentTitle string) bool {
	return ShouldAutoTitle(currentTitle) || isGenericTitle(currentTitle)
}

// BuildLLMTitlePrompt builds the single user message for the title side
// call. It returns ok=false when there is no usable conversation content
// (the caller then skips the call entirely — no provider round trip).
// There is deliberately no system message: one compact user message keeps
// the call minimal and provider-agnostic.
func BuildLLMTitlePrompt(firstUserMessage, firstAssistantReply string) (string, bool) {
	user := collapseTitleWhitespace(firstUserMessage)
	assistant := collapseTitleWhitespace(firstAssistantReply)
	if utf8.RuneCountInString(user) < titleMinRunes {
		return "", false
	}

	var b strings.Builder
	b.WriteString("Generate a concise session title for this coding-assistant conversation.\n\n")
	b.WriteString("Rules:\n")
	b.WriteString("- 3 to 8 words (for CJK: 4 to 16 characters)\n")
	b.WriteString("- Same language as the conversation\n")
	b.WriteString("- Describe the task or topic, not greetings or filler\n")
	b.WriteString("- No quotes, no code formatting, no trailing punctuation\n")
	b.WriteString("- Reply with ONLY the title text, nothing else\n\n")
	b.WriteString("Conversation start:\n")
	b.WriteString("User: " + excerptRunes(user, llmTitleUserExcerptRunes) + "\n")
	if assistant != "" {
		b.WriteString("Assistant: " + excerptRunes(assistant, llmTitleAssistantExcerptRunes) + "\n")
	}
	b.WriteString("\nTitle:")
	return b.String(), true
}

// SanitizeLLMTitle normalizes a raw LLM title reply into a safe display
// title: first line only, wrappers (quotes/backticks/bold) stripped,
// whitespace collapsed, trailing punctuation and markdown markers removed,
// capped at titleMaxRunes like every other title source.
func SanitizeLLMTitle(raw string) string {
	s := strings.TrimSpace(raw)
	if s == "" {
		return ""
	}
	// Models occasionally prefix a label; take everything after the last
	// line that still looks like content only via first-line selection.
	if idx := strings.IndexByte(s, '\n'); idx >= 0 {
		s = strings.TrimSpace(s[:idx])
	}
	s = strings.TrimPrefix(s, "**")
	s = strings.TrimSuffix(s, "**")
	for {
		trimmed := stripTitleWrappers(s)
		if trimmed == s || trimmed == "" {
			break
		}
		s = trimmed
	}
	s = strings.TrimSpace(excessiveSpacesRe.ReplaceAllString(s, " "))
	s = strings.Trim(s, "#*")
	s = strings.TrimSpace(s)
	// Trailing sentence punctuation never belongs in a title.
	s = strings.TrimRight(s, "。．，、;；:：!！?？…·,.")
	return truncateTitle(s, titleMaxRunes)
}

// LLMTitleAcceptable reports whether a sanitized LLM candidate may replace
// the current title: non-empty, actually different, long enough to be
// meaningful, and itself not generic (a model can echo "hi" back).
func LLMTitleAcceptable(currentTitle, candidate string) bool {
	c := strings.TrimSpace(candidate)
	if c == "" || c == currentTitle {
		return false
	}
	if utf8.RuneCountInString(c) < titleMinRunes {
		return false
	}
	return !isGenericTitle(c)
}

// stripTitleWrappers removes one symmetric wrapper layer (quotes, corner
// brackets) if present. Returns the input unchanged when no wrapper matches.
func stripTitleWrappers(s string) string {
	runes := []rune(strings.TrimSpace(s))
	if len(runes) < 2 {
		return s
	}
	first, last := runes[0], runes[len(runes)-1]
	pairs := [][2]rune{
		{'"', '"'}, {'\'', '\''}, {'`', '`'},
		{'“', '”'}, {'‘', '’'}, {'「', '」'},
	}
	for _, p := range pairs {
		if first == p[0] && last == p[1] {
			return strings.TrimSpace(string(runes[1 : len(runes)-1]))
		}
	}
	return s
}

// collapseTitleWhitespace normalizes all whitespace (incl. newlines) to
// single spaces so excerpt caps apply to content, not formatting.
func collapseTitleWhitespace(s string) string {
	return strings.TrimSpace(excessiveSpacesRe.ReplaceAllString(s, " "))
}

// excerptRunes truncates to max runes (safe for CJK) appending an ellipsis.
func excerptRunes(s string, max int) string {
	if utf8.RuneCountInString(s) <= max {
		return s
	}
	return string([]rune(s)[:max]) + "…"
}
