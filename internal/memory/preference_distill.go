package memory

import (
	"strings"
	"unicode/utf8"

	"github.com/topcheer/ggcode/internal/debug"
)

// Preference distillation - the "Gather" seam of memory consolidation
// (cf. Anthropic Auto Dream, 2026-03: scan session transcripts for durable
// user preferences that were never saved as memory; without this, a user
// statement like "from now on always run tests with -p=1" lives only in the
// conversation and is lost on the next cold start).
//
// Distinct from:
//   - run-insights (agent.GenerateInsights): what the AGENT learned
//   - experience cases (experience.go): task/approach/outcome per run
//   - cmd_snippet distillation (r262): verified commands from the tape
//
// This seam captures what the USER asked to be remembered, deterministically
// (no LLM call), with high-precision markers only. Precision over recall:
// a missed preference costs one repetition; a false one pollutes every
// future prompt.

// preferenceMarkers are substrings whose containing sentence is a durable
// user preference statement with high probability. Bilingual (EN/CJK).
var preferenceMarkers = []string{
	// English durable-intent markers.
	"from now on",
	"from here on",
	"going forward",
	"always use",
	"always run",
	"always prefer",
	"never use",
	"never run",
	"don't use",
	"do not use",
	"remember to",
	"remember that",
	// Chinese durable-intent markers.
	"以后都",
	"以后请",
	"以后用",
	"以后改用",
	"以后不要",
	"以后不再",
	"以后一律",
	"从现在起",
	"从今以后",
	"别再",
	"记住要",
	"记住：",
	"记住:",
}

// correctiveMarkers signal an IMMEDIATE correction of the agent's just-made
// choice (sa-222: users reteach the same lesson every session because
// "不对，用 pnpm 跑" / "no, use pnpm instead" carry no durable-intent word
// like always/never/以后). Alone they are too noisy ("不对，这个结果错了"
// is a bug report, not a preference) - isPreferenceSentence requires an
// action-word co-occurrence to accept them (precision-over-recall, same
// contract as the explicit markers above).
var correctiveMarkers = []string{
	// English correction markers.
	"no, use",
	"no, run",
	"instead of",
	"instead, ",
	// Chinese correction markers.
	"不对，",
	"不对,",
	"别用",
	"换成",
	"改用",
}

// correctiveActionWords are the action verbs that turn a correction into a
// durable preference: the user is specifying WHAT to use/do instead, not
// just complaining about a result.
var correctiveActionWords = []string{
	"use", "run", "go with", "prefer", "switch",
	"用", "换", "跑", "执行", "安装",
}

const (
	// maxPreferencesPerRun caps captures per run to bound noise.
	maxPreferencesPerRun = 2
	// maxPreferenceLineLen bounds a single captured sentence.
	maxPreferenceLineLen = 240
	// maxPreferenceEntries bounds total stored entries (old ones drop off
	// the top, keeping the most recent - bounded growth per the
	// minimal-context-file lesson, ETH Zurich agents.md study 2026-03).
	maxPreferenceEntries = 30
)

// DistillUserPreferences extracts high-confidence durable preference
// statements from a user prompt (typically RunStats.UserPrompt). Pure and
// deterministic: sentences containing a marker are returned verbatim,
// deduplicated, capped at maxPreferencesPerRun.
func DistillUserPreferences(text string) []string {
	if strings.TrimSpace(text) == "" {
		return nil
	}
	var prefs []string
	seen := map[string]bool{}
	for _, sent := range splitPreferenceSentences(text) {
		sent = strings.TrimSpace(sent)
		if sent == "" || !isPreferenceSentence(sent) {
			continue
		}
		if utf8.RuneCountInString(sent) > maxPreferenceLineLen {
			// Keep the head of an over-long sentence plus the marker.
			runes := []rune(sent)
			sent = strings.TrimSpace(string(runes[:maxPreferenceLineLen]))
		}
		if seen[sent] {
			continue
		}
		seen[sent] = true
		prefs = append(prefs, sent)
		if len(prefs) >= maxPreferencesPerRun {
			break
		}
	}
	return prefs
}

// isPreferenceSentence reports whether the sentence contains an explicit
// marker, or a corrective marker co-occurring with an action word.
func isPreferenceSentence(sent string) bool {
	lower := strings.ToLower(sent)
	for _, m := range preferenceMarkers {
		if strings.Contains(lower, strings.ToLower(m)) {
			return true
		}
	}
	for _, c := range correctiveMarkers {
		if strings.Contains(lower, strings.ToLower(c)) {
			for _, a := range correctiveActionWords {
				if strings.Contains(lower, a) {
					return true
				}
			}
			return false
		}
	}
	return false
}

// CapturePreferences distills durable preference statements from text and
// merges them into the "user-preferences" project memory key (Auto Dream
// Gather seam). It is the shared run-terminal hook for every entry point
// (TUI reflection, pipe, daemon), so a preference stated in ANY session
// shape outlives it. Failures are debug-logged only; capture must never
// disturb the caller's flow. Returns how many new entries were added.
func CapturePreferences(workingDir, text string) int {
	prefs := DistillUserPreferences(text)
	if len(prefs) == 0 {
		return 0
	}
	if workingDir == "" {
		return 0
	}
	autoMem := NewProjectAutoMemory(workingDir)
	if autoMem == nil {
		return 0
	}
	const key = "user-preferences"
	// #1388 discipline: single-key load, merge, save - never blind overwrite.
	existing, err := autoMem.LoadKey(key)
	if err != nil {
		debug.Log("preferences", "failed to load existing, skipping save: %v", err)
		return 0
	}
	merged, added := MergePreferenceMemory(existing, prefs)
	if added == 0 {
		return 0
	}
	if err := autoMem.SaveMemory(key, merged); err != nil {
		debug.Log("preferences", "save failed: %v", err)
		return 0
	}
	debug.Log("preferences", "captured %d new user preference(s)", added)
	return added
}

// splitPreferenceSentences splits on CJK/EN sentence terminators and
// newlines. An ASCII '.', '!', or '?' followed by an alphanumeric rune is
// treated as part of a token (pubspec.yaml, config.json) rather than a
// sentence boundary - same rule as the ambiguity detector's sentence scan.
// Semicolons are kept inside sentences (they often separate marker from
// rationale, e.g. "from now on; it avoids flakiness").
func splitPreferenceSentences(text string) []string {
	var parts []string
	var cur []rune
	runes := []rune(text)
	for i, r := range runes {
		switch r {
		case '\n', '\r', '。', '！', '？':
			parts = append(parts, string(cur))
			cur = cur[:0]
			continue
		case '.', '!', '?':
			if i+1 < len(runes) && isPreferenceWordRune(runes[i+1]) {
				cur = append(cur, r) // in-token dot: pubspec.yaml
				continue
			}
			parts = append(parts, string(cur))
			cur = cur[:0]
			continue
		}
		cur = append(cur, r)
	}
	parts = append(parts, string(cur))
	return parts
}

// isPreferenceWordRune reports whether r is a letter or digit (ASCII or CJK
// ideograph) - used to detect in-token punctuation like file extensions.
func isPreferenceWordRune(r rune) bool {
	switch {
	case r >= 'a' && r <= 'z', r >= 'A' && r <= 'Z', r >= '0' && r <= '9':
		return true
	case r >= 0x4e00 && r <= 0x9fff: // CJK unified ideographs
		return true
	}
	return false
}

// MergePreferenceMemory merges newly distilled preference sentences into the
// existing "user-preferences" memory body. Existing lines are preserved in
// order; new (non-duplicate, case-insensitive) lines are appended; the total
// is capped at maxPreferenceEntries with the OLDEST entries dropped from the
// top. Returns the merged body and how many new lines were added.
func MergePreferenceMemory(existing string, prefs []string) (string, int) {
	var lines []string
	for _, l := range strings.Split(existing, "\n") {
		l = strings.TrimSpace(l)
		if l == "" {
			continue
		}
		lines = append(lines, strings.TrimPrefix(l, "- "))
	}
	seen := map[string]bool{}
	for _, l := range lines {
		seen[strings.ToLower(l)] = true
	}
	added := 0
	for _, p := range prefs {
		key := strings.ToLower(p)
		if seen[key] {
			continue
		}
		seen[key] = true
		lines = append(lines, p)
		added++
	}
	if len(lines) > maxPreferenceEntries {
		lines = lines[len(lines)-maxPreferenceEntries:]
	}
	var b strings.Builder
	for _, l := range lines {
		b.WriteString("- ")
		b.WriteString(l)
		b.WriteByte('\n')
	}
	return b.String(), added
}
