package context

// Fact retention (sa-237 round-3 finding): deterministic critical-fact
// preservation across compaction. The 9-section summary prompt *asks* the
// LLM to keep user constraints and key files, but prompt imperatives are
// not guarantees. This post-pass scans the pre-compaction payload and
// re-attaches verbatim constraint lines and high-frequency file paths the
// produced summary dropped, bounded to a small char budget so the
// guarantee costs little context. Recall-first by design: re-attaching a
// fact the summary paraphrased is harmless; losing a user constraint
// ("don't modify tests") changes agent behavior after compaction.

import (
	"regexp"
	"strings"
	"unicode"

	"github.com/topcheer/ggcode/internal/debug"
	"github.com/topcheer/ggcode/internal/provider"
)

// constraintMarkers: lines carrying an imperative/restriction signal.
// Matched lowercase (English) or by substring (CJK, caseless by nature).
var constraintMarkers = []string{
	"don't", "do not", "never", "must ", "must not", "always ", "forbidden",
	"不要", "必须", "别用", "别改", "不能", "禁止", "务必", "只能",
}

// filePathRe: source-file references in transcript text.
var filePathRe = regexp.MustCompile(`(?:^|[\s"'(` + "`" + `(=,:])(([\w\-./]+\.(?:go|md|ts|tsx|py|rs|java|js|jsx|yaml|yml|toml|json|mod|sql|sh)))`)

const (
	// minPathOccurrences: a path is high-signal only if it recurs across
	// the transcript (tool calls, edits, reads all reference it).
	minPathOccurrences = 3
	// maxConstraintLines caps extracted constraint sentences.
	maxConstraintLines = 8
	// maxRetentionChars caps the appended section (~200 tokens).
	maxRetentionChars = 800
)

// userConstraintSource collects text from user-role messages only -
// the sole source binding constraints may be extracted from (#3675).
// Assistant reasoning ("we must not touch X") and tool_result echoes
// (code comments, compiler errors) used to be lifted verbatim into the
// "treat as binding" section and then self-perpetuated across compactions,
// locking the agent to its own past self-talk. Truncation is deliberately
// absent: constraints already pass through the 200-char line cap in
// extractConstraintLines and the section-wide maxRetentionChars budget.
func userConstraintSource(msgs []provider.Message) string {
	var b strings.Builder
	for _, msg := range msgs {
		if msg.Role != "user" {
			continue
		}
		for _, block := range msg.Content {
			if block.Type != "text" {
				continue
			}
			text := strings.TrimSpace(block.Text)
			if text == "" {
				continue
			}
			b.WriteString(text)
			b.WriteByte('\n')
		}
	}
	return b.String()
}

// extractConstraintLines pulls short lines carrying constraint markers
// from user-sourced text. Line-granular (not sentence) on purpose:
// transcript text is already line-structured. NOTE: input must be
// user-role text only (see userConstraintSource); no role filtering
// happens here by design - the caller owns provenance (#3675).
func extractConstraintLines(payload string) []string {
	var out []string
	seen := map[string]bool{}
	for _, raw := range strings.Split(payload, "\n") {
		line := strings.TrimSpace(raw)
		if line == "" || len(line) > 200 || seen[line] {
			continue
		}
		low := strings.ToLower(line)
		for _, m := range constraintMarkers {
			if strings.Contains(low, m) {
				seen[line] = true
				out = append(out, line)
				break
			}
		}
		if len(out) >= maxConstraintLines {
			break
		}
	}
	return out
}

// topRecurringPaths returns file paths appearing >= min times, most
// frequent first.
func topRecurringPaths(payload string) []string {
	counts := map[string]int{}
	for _, m := range filePathRe.FindAllStringSubmatch(payload, -1) {
		p := m[1]
		if len(p) < 4 || strings.Count(p, ".") < 1 {
			continue
		}
		counts[p]++
	}
	type pc struct {
		path string
		n    int
	}
	var list []pc
	for p, n := range counts {
		if n >= minPathOccurrences {
			list = append(list, pc{p, n})
		}
	}
	// Stable-ish ordering: frequency desc, then path asc for determinism.
	for i := 1; i < len(list); i++ {
		for j := i; j > 0 && (list[j].n > list[j-1].n || (list[j].n == list[j-1].n && list[j].path < list[j-1].path)); j-- {
			list[j], list[j-1] = list[j-1], list[j]
		}
	}
	var out []string
	for _, e := range list {
		if len(out) >= 12 {
			break
		}
		out = append(out, e.path)
	}
	return out
}

// normForContain folds case and whitespace so a summary that paraphrases
// spacing/casing still counts as having preserved the fact.
func normForContain(s string) string {
	low := strings.ToLower(s)
	var b strings.Builder
	for _, r := range low {
		if unicode.IsSpace(r) {
			continue
		}
		b.WriteRune(r)
	}
	return b.String()
}

// applyFactRetention appends an "Auto-preserved Facts" section carrying
// constraint lines and recurring paths the summary dropped. No-op when
// the summary preserved everything (or there was nothing to preserve).
// #3675: constraint lines are extracted from userSrc (user-role text
// only, see userConstraintSource) — assistant reasoning and tool_result
// echoes must never be crowned "treat as binding" user constraints.
// Recurring paths still scan the full payload: file paths surface via
// tool activity, which is exactly their provenance.
func applyFactRetention(summary, payload, userSrc string) string {
	if summary == "" || payload == "" {
		return summary
	}
	summaryNorm := normForContain(summary)
	var facts []string
	for _, line := range extractConstraintLines(userSrc) {
		if !strings.Contains(summaryNorm, normForContain(line)) {
			facts = append(facts, line)
		}
	}
	for _, p := range topRecurringPaths(payload) {
		if !strings.Contains(summary, p) {
			facts = append(facts, "recurring file: "+p)
		}
	}
	if len(facts) == 0 {
		return summary
	}
	var b strings.Builder
	b.WriteString(summary)
	b.WriteString("\n\n## Auto-preserved Facts\nRe-attached deterministically from the pre-compaction transcript (the summary above dropped them; treat as binding):\n")
	appended := 0
	written := 0
	for _, f := range facts {
		if appended+len(f) > maxRetentionChars {
			break
		}
		b.WriteString("- ")
		b.WriteString(f)
		b.WriteString("\n")
		appended += len(f)
		written++
	}
	out := b.String()
	debug.Log("ctx", "fact retention: %d/%d candidate facts re-attached (%d chars appended)", written, len(facts), appended)
	return out
}
