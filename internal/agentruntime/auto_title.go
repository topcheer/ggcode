package agentruntime

import (
	"regexp"
	"strings"
	"unicode/utf8"

	"github.com/topcheer/ggcode/internal/util"
)

// Smart Title Generation — Intelligent Session Naming
//
// Claude Code, Cursor, and ChatGPT all auto-generate concise, descriptive
// session titles. ggcode previously used a naive truncation of the first
// user message, which produced poor titles when the message contained:
//   - Code blocks or inline code
//   - Stack traces / error dumps
//   - File paths or URLs
//   - Multi-line task descriptions (title cut mid-sentence)
//   - Markdown formatting
//
// This module produces clean titles using deterministic heuristics:
//   1. Strip code blocks, URLs, file paths, and markdown noise
//   2. Extract the first meaningful sentence or clause
//   3. Collapse whitespace and truncate at word boundaries
//   4. Handle both English and CJK text (UTF-8 aware)
//
// No LLM call is needed — the heuristics are fast, deterministic, and free.

const (
	// titleMaxRunes caps the title length. Claude Code uses ~50 chars,
	// ChatGPT uses ~40. We use 60 for bilingual coverage (CJK chars
	// carry more meaning per rune).
	titleMaxRunes = 60
	// titleMinRunes is the minimum meaningful length; shorter inputs are
	// used verbatim without further processing.
	titleMinRunes = 3
)

var (
	// fencedCodeBlockRe matches ``` or ~~~ fenced code blocks (multiline).
	fencedCodeBlockRe = regexp.MustCompile("(?s)```[\\s\\S]*?```|~~~[\\s\\S]*?~~~")
	// inlineCodeRe matches `inline code`, capturing the inner text so the
	// $1 replacement below preserves identifiers. A missing capture group
	// expands $1 to the empty string and silently deletes them (#1479).
	inlineCodeRe = regexp.MustCompile("`([^`]+)`")
	// urlRe matches http/https URLs.
	urlRe = regexp.MustCompile(`https?://\S+`)
	// markdownHeadingRe matches markdown headings (#, ##, etc.).
	markdownHeadingRe = regexp.MustCompile(`^#{1,6}\s+`)
	// markdownBoldItalicRe matches **bold**, *italic*, __bold__, _italic_.
	markdownBoldItalicRe = regexp.MustCompile(`\*{1,3}([^*]+)\*{1,3}|_{1,3}([^_]+)_{1,3}`)
	// longFilePathRe matches paths that look like file references (3+ segments).
	longFilePathRe = regexp.MustCompile(`[\w\-./]+\b\.\w{1,5}\b`)
	// commandPrefixRe strips leading shell comment or prompt artifacts.
	commandPrefixRe = regexp.MustCompile(`^(#|\$|>|%)\s*`)
	// bracketedTagRe matches [tag] or <tag> prefixes like [bug], <fix>, etc.
	bracketedTagRe = regexp.MustCompile(`^[\[(<][\w\- ]+[\])>]\s*`)
	// excessiveSpacesRe collapses runs of whitespace.
	excessiveSpacesRe = regexp.MustCompile(`\s+`)
)

// GenerateTitle produces a clean, concise session title from a user message.
// Returns empty string if the input is too short to produce a meaningful title.
func GenerateTitle(userMessage string) string {
	s := userMessage

	// 1. Remove fenced code blocks entirely (they pollute titles).
	s = fencedCodeBlockRe.ReplaceAllString(s, " ")

	// 2. Replace inline code with just the inner text (preserves identifiers).
	s = inlineCodeRe.ReplaceAllString(s, "$1")

	// 3. Remove URLs.
	s = urlRe.ReplaceAllString(s, "")

	// 4. Remove markdown headings prefix.
	s = markdownHeadingRe.ReplaceAllString(s, "")

	// 5. Unwrap bold/italic markers, keep inner text.
	s = markdownBoldItalicRe.ReplaceAllString(s, "$1$2")

	// 6. Collapse file paths to just the basename (last segment).
	s = longFilePathRe.ReplaceAllStringFunc(s, func(m string) string {
		parts := strings.Split(m, "/")
		return parts[len(parts)-1]
	})

	// 7. Strip leading shell-prompt artifacts and bracketed tags.
	s = commandPrefixRe.ReplaceAllString(s, "")
	s = bracketedTagRe.ReplaceAllString(s, "")

	// 8. Take the first line BEFORE collapsing whitespace (multi-line →
	// first meaningful line). This ensures newlines are still available as
	// separators.
	if idx := strings.IndexByte(s, '\n'); idx > 0 {
		firstLine := strings.TrimSpace(s[:idx])
		if utf8.RuneCountInString(firstLine) >= titleMinRunes {
			s = firstLine
		}
	}

	// 9. Collapse whitespace and trim.
	s = strings.TrimSpace(excessiveSpacesRe.ReplaceAllString(s, " "))

	if utf8.RuneCountInString(s) < titleMinRunes {
		return ""
	}

	// 10. Take the first sentence if the text is long enough to warrant it.
	if utf8.RuneCountInString(s) > titleMaxRunes {
		s = firstSentence(s)
	}

	// 11. Truncate to max length at a word/rune boundary.
	s = truncateTitle(s, titleMaxRunes)

	return s
}

// firstSentence extracts the text up to the first sentence-ending
// punctuation (. ! ? ？ 。！), or returns the input unchanged if no
// terminator is found within the first titleMaxRunes runes.
func firstSentence(s string) string {
	endChars := ".!?。！？"
	runeIdx := 0
	for i, r := range s {
		if strings.ContainsRune(endChars, r) {
			candidate := strings.TrimSpace(s[:i+utf8.RuneLen(r)])
			if utf8.RuneCountInString(candidate) >= titleMinRunes {
				return strings.TrimRight(candidate, ".!?。！？")
			}
		}
		runeIdx++
		if runeIdx >= titleMaxRunes*2 {
			break
		}
	}
	return s
}

// truncateTitle truncates to maxRunes at a word boundary (space for Latin,
// any rune boundary for CJK). Appends "…" if truncated.
func truncateTitle(s string, maxRunes int) string {
	if utf8.RuneCountInString(s) <= maxRunes {
		return s
	}

	runes := []rune(s)
	truncated := string(runes[:maxRunes])

	// Try to break at the last space within the truncated portion.
	if idx := strings.LastIndex(truncated, " "); idx > maxRunes/2 {
		truncated = truncated[:idx]
	}

	// Clean trailing punctuation.
	truncated = strings.TrimRight(truncated, " ,;:、，；：")

	return truncated + "…"
}

// ShouldAutoTitle returns true if the session should have its title
// auto-generated (i.e., the current title is empty or a placeholder).
func ShouldAutoTitle(currentTitle string) bool {
	t := strings.TrimSpace(currentTitle)
	return t == "" || t == "New session" || t == "新会话"
}

// RefineTitleAfterRun updates the title after the first agent run if the
// initial title was too generic (e.g., "hi", "help", "test"). This uses the
// first user message to try again with stricter heuristics, or falls back to
// a summary derived from the run stats.
//
// agentSummary is a short description of what the agent actually did (e.g.,
// "Edited 3 files in internal/agent"). If non-empty and the user message was
// generic, the summary is used as the title.
func RefineTitleAfterRun(currentTitle, firstUserMessage, agentSummary string) string {
	// Don't override user-set titles (they typed /title manually).
	if !ShouldAutoTitle(currentTitle) && !isGenericTitle(currentTitle) {
		return ""
	}

	// If the first message produced a decent title, keep it.
	cleaned := GenerateTitle(firstUserMessage)
	if !isGenericTitle(cleaned) {
		// Only update if different from current (avoids unnecessary writes).
		if cleaned != "" && cleaned != currentTitle {
			return cleaned
		}
		return ""
	}

	// Fall back to the agent summary if the user message was too generic.
	if agentSummary != "" {
		return util.Truncate(agentSummary, titleMaxRunes)
	}

	return ""
}

// isGenericTitle returns true for titles that are too vague to be useful
// (e.g., "hi", "help", "test", single words with no task context).
func isGenericTitle(title string) bool {
	t := strings.ToLower(strings.TrimSpace(title))
	if t == "" || t == "new session" || t == "新会话" {
		return true
	}
	// Very short generic words
	generics := map[string]bool{
		"hi": true, "hello": true, "hey": true, "help": true,
		"test": true, "ok": true, "你好": true, "测试": true,
		"start": true, "begin": true, "question": true,
	}
	if generics[t] {
		return true
	}
	// Single-word titles under 6 chars are likely not descriptive.
	// CJK scripts carry no ASCII spaces, so this no-space branch would mark
	// every short Chinese title (改个名字, 修个bug) generic and let
	// RefineTitleAfterRun overwrite it with the English template; the
	// generics table above already covers real CJK filler words (#1479).
	if !strings.Contains(t, " ") && utf8.RuneCountInString(t) < 6 && !containsCJK(t) {
		return true
	}
	return false
}

// containsCJK reports whether s contains any CJK rune (Han incl. Ext-A,
// kana, Hangul, compatibility ideographs, full-width forms).
func containsCJK(s string) bool {
	for _, r := range s {
		if (r >= 0x2E80 && r <= 0x9FFF) || // CJK radicals + Han + Ext-A
			(r >= 0x3040 && r <= 0x30FF) || // Hiragana + Katakana
			(r >= 0xAC00 && r <= 0xD7AF) || // Hangul syllables
			(r >= 0xF900 && r <= 0xFAFF) || // CJK compatibility ideographs
			(r >= 0xFF01 && r <= 0xFF60) { // full-width forms
			return true
		}
	}
	return false
}
