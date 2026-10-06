package memory

import (
	"strings"
	"unicode"
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

// correctiveActionWords are the ENGLISH action verbs that turn a
// correction into a durable preference: the user is specifying WHAT to
// use/do instead, not just complaining. Matched with word boundaries
// (#3311): a bare substring match hit "because"/"cause"/"refuse" via
// "use" and let complaint sentences into the store.
var correctiveActionWords = []string{
	"use", "run", "go with", "prefer", "switch", "install",
}

// selfSufficientCorrectiveMarkers are Chinese corrective markers in
// verb+object form (别用 X / 改用 X / 换成 X): the marker itself IS the
// action, so no separate action word is required (#3311).
var selfSufficientCorrectiveMarkers = []string{
	"别用", "改用", "换成",
}

// correctiveActionWordsZH are MULTI-CHARACTER Chinese action verbs
// required to co-occur with a bare corrective marker (不对，...).
// Single-character verbs (用/换/跑) were removed: they live inside
// ordinary words (应用/作用/用户/更换/转换/跑单) and misfired on plain
// bug reports - the exact precision failure #3311 documents (#3311).
var correctiveActionWordsZH = []string{
	"使用", "运行", "执行", "安装", "换掉",
}

// correctiveZHVerbObjectChars: single-character Chinese action verbs that
// count ONLY in verb+object form - immediately followed by a space or an
// ASCII token (用 pnpm / 换 docker). Inside CJK words (应用/更换/跑单)
// the same characters are ordinary morphemes and must not match (#3311).
var correctiveZHVerbObjectChars = []rune{'用', '换', '跑', '装'}

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
	// #3311: the corrective gate must not fire on plain complaints.
	// Three tiers: verb+object Chinese markers are self-sufficient;
	// bare Chinese correction markers need a multi-char action verb;
	// English markers need a word-boundary action verb.
	for _, c := range correctiveMarkers {
		if !strings.Contains(lower, strings.ToLower(c)) {
			continue
		}
		for _, s := range selfSufficientCorrectiveMarkers {
			if strings.Contains(lower, s) {
				return true
			}
		}
		for _, a := range correctiveActionWordsZH {
			if strings.Contains(lower, a) {
				return true
			}
		}
		if containsZHVerbObject(lower) {
			return true
		}
		if containsWordBoundary(lower, correctiveActionWords) {
			return true
		}
		return false
	}
	return false
}

// containsZHVerbObject reports whether any single-character Chinese
// action verb appears in verb+object form: immediately followed by a
// space or an ASCII letter/digit (the object is a latin token, 用
// pnpm). The same character inside a CJK word (应用) is followed by
// another CJK rune and does not count (#3311).
func containsZHVerbObject(s string) bool {
	for _, c := range correctiveZHVerbObjectChars {
		start := 0
		for {
			i := strings.IndexRune(s[start:], c)
			if i < 0 {
				break
			}
			pos := start + i
			tail := s[pos+utf8.RuneLen(c):]
			if tail != "" {
				r, _ := utf8.DecodeRuneInString(tail)
				if r == ' ' || (r >= 'a' && r <= 'z') || (r >= '0' && r <= '9') {
					return true
				}
			}
			start = pos + utf8.RuneLen(c)
			if start >= len(s) {
				break
			}
		}
	}
	return false
}

// containsWordBoundary reports whether s contains any word as a whole
// word: both neighbors must be non-letter (or the string edge). English
// action verbs matched as bare substrings hit because/cause/refuse
// (#3311), so "use" in "because" must NOT count while "use" in
// "no, use pnpm" must.
func containsWordBoundary(s string, words []string) bool {
	for _, w := range words {
		start := 0
		for {
			i := strings.Index(s[start:], w)
			if i < 0 {
				break
			}
			pos := start + i
			end := pos + len(w)
			beforeOK := pos == 0 || !unicode.IsLetter(rune(s[pos-1]))
			afterOK := end == len(s) || !unicode.IsLetter(rune(s[end]))
			if beforeOK && afterOK {
				return true
			}
			start = pos + 1
			if start >= len(s) {
				break
			}
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

// preferenceSlots maps known tool tokens to a semantic preference slot
// (package manager, test runner, container runtime...). Two entries whose
// sentences carry tokens of the SAME slot are statements about the SAME
// preference domain (Hindsight arXiv:2512.12818: opinion coherence per
// subject). Dictionary-outside tokens carry no slot and never trigger
// supersession - precision over recall, same contract as the markers.
var preferenceSlots = map[string]string{
	"npm":     "pkg-manager",
	"pnpm":    "pkg-manager",
	"yarn":    "pkg-manager",
	"bun":     "pkg-manager",
	"pytest":  "test-runner",
	"jest":    "test-runner",
	"vitest":  "test-runner",
	"docker":  "container-runtime",
	"podman":  "container-runtime",
	"bash":    "shell",
	"zsh":     "shell",
	"fish":    "shell",
	"gofmt":   "go-formatter",
	"gofumpt": "go-formatter",
}

// reversalMarkers signal that the new statement REVOKES a previous choice:
// without supersession the store keeps "always use npm" AND "以后改用 pnpm"
// and injects both into every future prompt - the exact evidence/inference
// blur Hindsight flags (sa-72 PARTIAL gap). Bilingual.
var reversalMarkers = []string{
	// English revocation markers.
	"never use",
	"don't use",
	"do not use",
	"no, use",
	"no, run",
	"instead of",
	"instead, ",
	"switch to",
	"switch back",
	"rather than",
	// Chinese revocation markers.
	"以后不要",
	"以后不再",
	"别用",
	"别再",
	"改用",
	"换成",
	"改回",
	"换回",
	"不再用",
	"不要用",
}

// preferenceSlotsOf returns every slot the sentence expresses an opinion on
// (word-boundary matched, #3311 lesson: "use" inside "because" must not
// count; here "go" inside "gofmt" must not double-fire either).
func preferenceSlotsOf(sent string) []string {
	lower := strings.ToLower(sent)
	var slots []string
	seen := map[string]bool{}
	for tok, slot := range preferenceSlots {
		if !containsWordBoundary(lower, []string{tok}) {
			continue
		}
		if !seen[slot] {
			seen[slot] = true
			slots = append(slots, slot)
		}
	}
	return slots
}

// hasReversalMarker reports whether the sentence revokes a prior choice.
// A standalone "instead" (word-boundary, so "instead,"/"instead of" also
// qualify via the substring set above) is itself a replacement signal:
// "no, run scripts with zsh instead" names no other marker.
func hasReversalMarker(sent string) bool {
	lower := strings.ToLower(sent)
	for _, m := range reversalMarkers {
		if strings.Contains(lower, strings.ToLower(m)) {
			return true
		}
	}
	return containsWordBoundary(lower, []string{"instead"})
}

// MergePreferenceMemory merges newly distilled preference sentences into the
// existing "user-preferences" memory body. Existing lines are preserved in
// order; new (non-duplicate, case-insensitive) lines are appended; the total
// is capped at maxPreferenceEntries with the OLDEST entries dropped from the
// top. Returns the merged body and how many new lines were added.
//
// Supersession (sa-72 / Hindsight opinion-coherence): when a NEW entry
// carries a reversal marker and expresses an opinion on a known slot, older
// entries on the SAME slot are dropped instead of coexisting - the newest
// statement wins and contradictory preferences are never injected together.
// Conservative by design: a reversal with NO known slot deletes nothing, and
// a plain (non-reversal) statement never deletes - a missed supersession
// costs one stale line, a wrong one deletes a real user statement.
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
		// Supersession pass: a reversal statement on known slots drops the
		// older same-slot entries (they describe a choice the user just
		// revoked - keeping both would inject a contradiction).
		if hasReversalMarker(p) {
			newSlots := preferenceSlotsOf(p)
			if len(newSlots) > 0 {
				slotSet := map[string]bool{}
				for _, s := range newSlots {
					slotSet[s] = true
				}
				kept := lines[:0]
				for _, l := range lines {
					overlap := false
					for _, s := range preferenceSlotsOf(l) {
						if slotSet[s] {
							overlap = true
							break
						}
					}
					if overlap {
						debug.Log("preferences", "superseded by %q (same slot): %q", p, l)
						continue
					}
					kept = append(kept, l)
				}
				lines = kept
			}
		}
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
