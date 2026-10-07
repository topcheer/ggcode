package memory

// Pre-Compaction Memory Flush (r491). Context compaction is lossy: facts that
// fall outside the summarizer's attention are gone from the live context, and
// fact_retention's re-attachment (internal/context/fact_retention.go) only
// keeps them alive until the NEXT compaction folds them into a
// summary-of-summary. This flush runs BEFORE compaction folds the older slice
// and deterministically persists constraint-line facts into project auto
// memory, so they outlive the whole compaction chain.
//
// Online evidence (2026-10): PraisonAI ships the same concept as a product
// feature ("Pre-Compaction Memory Flush" — one bounded pass with
// search_memory/store_memory before compaction); zylos.ai's 2026 compaction
// review rates external memory offload as the highest-fidelity strategy and
// Factory.ai's probe eval found artifact-trail facts are preserved worst by
// every production method (2.19–2.45/5.0) while being the most practically
// important for coding agents.
//
// Deterministic by design: no LLM call, line-granular extraction reusing the
// same proven constraint-marker heuristic as fact_retention (copied — the
// originals are unexported and exporting them would grow a 27-fan-in hub
// package's API surface). Idempotent: dedupe-on-merge means a reactive
// compaction following an auto pre-compact flush persists zero duplicates.

import (
	"regexp"
	"strings"

	"github.com/topcheer/ggcode/internal/debug"
)

// PreflushMsg is the minimal message shape the flush consumes. The agent
// package adapts provider.Message at the call site so this package stays
// provider-free.
type PreflushMsg struct {
	Role string // "user" or "assistant" (others are skipped)
	Text string
}

// preflushMarkers: same imperative/restriction signal set as fact_retention.
// Matched lowercase (English) or by substring (CJK, caseless by nature).
var preflushMarkers = []string{
	"don't", "do not", "never", "must ", "must not", "always ", "forbidden",
	"不要", "必须", "别用", "别改", "不能", "禁止", "务必", "只能",
}

// preflushSpeakerRe strips transcript speaker tags so "User: don't X" and a
// bare "don't X" dedupe to one entry.
var preflushSpeakerRe = regexp.MustCompile(`(?i)^(?:user|assistant|system|tool|human|ai)\s*[>:]\s*`)

const (
	// preflushKey is the project auto-memory key facts are merged into.
	preflushKey = "compaction-facts"
	// preflushMaxLineLen matches fact_retention's line cap: longer lines are
	// rarely single constraints.
	preflushMaxLineLen = 200
	// preflushMaxExtract caps extraction per flush.
	preflushMaxExtract = 12
	// preflushMaxEntries caps the stored list (bounded like user-preferences).
	preflushMaxEntries = 24
)

// extractPreflushLines pulls short constraint-carrying lines from one
// message's text. Line-granular on purpose: transcript text is already
// line-structured. System prompts are excluded upstream (they are full of
// instruction-shaped noise that is not user/session facts).
func extractPreflushLines(m PreflushMsg) []string {
	if m.Role != "user" && m.Role != "assistant" {
		return nil
	}
	var out []string
	seen := map[string]bool{}
	for _, raw := range strings.Split(m.Text, "\n") {
		line := strings.TrimSpace(preflushSpeakerRe.ReplaceAllString(strings.TrimSpace(raw), ""))
		if line == "" || len(line) > preflushMaxLineLen || seen[line] {
			continue
		}
		low := strings.ToLower(line)
		for _, mk := range preflushMarkers {
			if strings.Contains(low, mk) {
				seen[line] = true
				out = append(out, line)
				if len(out) >= preflushMaxExtract {
					return out
				}
				break
			}
		}
	}
	return out
}

// mergeFactLines merges new fact lines into the existing "- line" list with
// case-insensitive dedupe (same merge discipline as MergePreferenceMemory:
// load-merge-save, never blind overwrite — #1388).
func mergeFactLines(existing string, facts []string) (string, int) {
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
	for _, f := range facts {
		key := strings.ToLower(f)
		if seen[key] {
			continue
		}
		seen[key] = true
		lines = append(lines, f)
		added++
	}
	if len(lines) > preflushMaxEntries {
		lines = lines[len(lines)-preflushMaxEntries:]
	}
	var b strings.Builder
	for _, l := range lines {
		b.WriteString("- ")
		b.WriteString(l)
		b.WriteByte('\n')
	}
	return b.String(), added
}

// PreflushFacts extracts constraint-line facts from the about-to-be-folded
// messages and merges them into the "compaction-facts" project memory key.
// Failures are debug-logged only: the flush must never disturb (delay or
// abort) the compaction itself — compaction refusal would strand the session
// at PTL. Returns how many new entries were added.
func PreflushFacts(workingDir string, msgs []PreflushMsg) int {
	if workingDir == "" || len(msgs) == 0 {
		return 0
	}
	var facts []string
	fseen := map[string]bool{}
	for _, m := range msgs {
		for _, line := range extractPreflushLines(m) {
			if !fseen[line] {
				fseen[line] = true
				facts = append(facts, line)
			}
		}
	}
	if len(facts) == 0 {
		return 0
	}
	autoMem := NewProjectAutoMemory(workingDir)
	if autoMem == nil {
		return 0
	}
	existing, err := autoMem.LoadKey(preflushKey)
	if err != nil {
		debug.Log("preflush", "failed to load existing %s, skipping save: %v", preflushKey, err)
		return 0
	}
	merged, added := mergeFactLines(existing, facts)
	if added == 0 {
		return 0
	}
	if err := autoMem.SaveMemoryWithSource(preflushKey, merged, "preflush"); err != nil {
		debug.Log("preflush", "save failed: %v", err)
		return 0
	}
	debug.Log("preflush", "persisted %d new fact(s) before compaction", added)
	return added
}
