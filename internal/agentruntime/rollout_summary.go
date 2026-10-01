// Package agentruntime: rollout trajectory summarization and selection.
//
// Implements the test-time scaling mechanisms from "Scaling Test-Time Compute
// for Agentic Coding" (arXiv:2604.16529): each agent rollout (an extended
// trajectory of actions/observations/errors/partial progress) is converted
// into a compact structured summary preserving salient hypotheses, progress,
// and failure modes while discarding low-signal trace details. Two scaling
// forms are supported:
//
//   - Parallel: RecursiveTournament narrows a population of rollout summaries
//     through small-group comparisons (RTV).
//   - Sequential: DistillIntoPrompt conditions a new rollout on summaries
//     distilled from prior attempts (agentic PDR).
//
// The module is deliberately dependency-free (fmt/sort/strings only) so it can
// be used from the agent loop, the fallback chain, or sub-agent spawning
// without import cycles. It never mutates message history and never inserts
// messages between tool calls and tool results.
package agentruntime

import (
	"fmt"
	"sort"
	"strings"
)

// TrajectoryEntry is one observation from an agent rollout. Kind is one of
// "action", "observation", "error", "result", "note".
type TrajectoryEntry struct {
	Kind string
	Text string
}

// RolloutSummary is the compact representation of one rollout: what was
// attempted (Hypotheses), what succeeded (Progress), what went wrong
// (Failures), and a confidence signal for selection.
type RolloutSummary struct {
	Hypotheses []string // salient approaches tried (e.g. "rename symbol via LSP")
	Progress   []string // verified forward steps (e.g. "tests added", "build passes")
	Failures   []string // failure modes with context (e.g. "vet failed: unused var x")
	Verdict    string   // one-line outcome: "succeeded" | "partial" | "failed"
}

// maxDetailEntries caps each summary section so the representation stays
// compact regardless of trajectory length (low-signal trace detail is dropped).
const maxDetailEntries = 5

// failureSignals mark an entry as a failure mode. Sorted by severity weight
// in severityOf.
var failureSignals = map[string]int{
	"error":    3,
	"failed":   3,
	"panic":    4,
	"fatal":    4,
	"timeout":  2,
	"denied":   1,
	"conflict": 2,
}

// progressSignals mark an entry as verified forward progress.
var progressSignals = []string{
	"pass", "succeed", "ok:", "no error", "built", "committed", "created", "verified",
}

// hypothesisSignals mark an entry as an approach/hypothesis worth carrying.
var hypothesisSignals = []string{
	"plan:", "approach:", "attempt", "try", "hypothesis", "strategy", "fix:",
}

// SummarizeTrajectory distills a raw rollout into a RolloutSummary. Entries
// are classified by kind and keyword signals; entries classified into no
// section are treated as low-signal trace detail and dropped, per the paper.
func SummarizeTrajectory(entries []TrajectoryEntry) RolloutSummary {
	s := RolloutSummary{Verdict: "failed"}
	anyProgress := false
	anyFailure := false

	for _, e := range entries {
		text := strings.TrimSpace(e.Text)
		if text == "" {
			continue
		}
		lower := strings.ToLower(text)
		kind := strings.ToLower(e.Kind)

		switch {
		case kind == "error" || hasSignal(lower, failureSignalsKeys()):
			anyFailure = true
			s.addFailure(compactLine(text, 200))
		case kind == "result" || kind == "note" || hasAny(lower, progressSignals):
			if looksLikeFailure(lower) {
				anyFailure = true
				s.addFailure(compactLine(text, 200))
				continue
			}
			anyProgress = true
			s.addProgress(compactLine(text, 200))
		case kind == "action" || hasAny(lower, hypothesisSignals):
			s.addHypothesis(compactLine(text, 160))
		default:
			// low-signal trace detail: dropped
		}
	}

	switch {
	case anyProgress && !anyFailure:
		s.Verdict = "succeeded"
	case anyProgress:
		s.Verdict = "partial"
	}
	return s
}

func (s *RolloutSummary) addHypothesis(t string) { appendCapped(&s.Hypotheses, t) }
func (s *RolloutSummary) addProgress(t string)   { appendCapped(&s.Progress, t) }
func (s *RolloutSummary) addFailure(t string)    { appendCapped(&s.Failures, t) }

func appendCapped(dst *[]string, v string) {
	if len(*dst) >= maxDetailEntries {
		return
	}
	// Dedup identical lines (common with repeated retries).
	for _, existing := range *dst {
		if existing == v {
			return
		}
	}
	*dst = append(*dst, v)
}

func failureSignalsKeys() []string {
	keys := make([]string, 0, len(failureSignals))
	for k := range failureSignals {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	return keys
}

func hasSignal(lower string, keys []string) bool { return hasAny(lower, keys) }

func hasAny(lower string, signals []string) bool {
	for _, sig := range signals {
		if strings.Contains(lower, sig) {
			return true
		}
	}
	return false
}

// looksLikeFailure guards against progress-keyword false positives such as
// "tests pass check failed" or "0 passing".
func looksLikeFailure(lower string) bool {
	return strings.Contains(lower, "fail") || strings.Contains(lower, "error")
}

// compactLine truncates a trace line to at most limit runes with an ellipsis,
// keeping the head of the line where the signal usually lives.
func compactLine(text string, limit int) string {
	text = strings.Join(strings.Fields(text), " ")
	r := []rune(text)
	if len(r) <= limit {
		return text
	}
	return string(r[:limit]) + "…"
}

// score rates a summary for tournament selection: verified progress dominates,
// failure severity subtracts, and a success verdict gets a bonus. Mirrors the
// paper's finding that list-wise comparison over compact summaries is the
// strongest selection method.
func (s RolloutSummary) score() int {
	v := 10 * len(s.Progress)
	for _, f := range s.Failures {
		lower := strings.ToLower(f)
		sev := 1
		for sig, w := range failureSignals {
			if strings.Contains(lower, sig) && w > sev {
				sev = w
			}
		}
		v -= sev
	}
	switch s.Verdict {
	case "succeeded":
		v += 25
	case "partial":
		v += 5
	}
	// Hypotheses are informative for reuse but not evidence of success.
	v += len(s.Hypotheses) / 2
	return v
}

// Compare evaluates two summaries head-to-head, returning the better one.
// Small-group comparisons like this are the building block of RTV.
func Compare(a, b RolloutSummary) RolloutSummary {
	sa, sb := a.score(), b.score()
	if sa > sb {
		return a
	}
	if sb > sa {
		return b
	}
	// Tie-break deterministically on content so selection is stable.
	if strings.Join(b.Progress, "|")+strings.Join(b.Failures, "|") <
		strings.Join(a.Progress, "|")+strings.Join(a.Failures, "|") {
		return b
	}
	return a
}

// RecursiveTournament narrows a population of rollout summaries through
// small-group comparisons (Recursive Tournament Voting, groupSize typically
// 4). It recursively halves the field until one winner remains; an empty
// population yields the zero summary. Deterministic and side-effect free.
func RecursiveTournament(candidates []RolloutSummary, groupSize int) RolloutSummary {
	if len(candidates) == 0 {
		return RolloutSummary{Verdict: "failed"}
	}
	if len(candidates) == 1 {
		return candidates[0]
	}
	if groupSize < 2 {
		groupSize = 4 // paper default
	}

	var next []RolloutSummary
	for i := 0; i < len(candidates); i += groupSize {
		end := i + groupSize
		if end > len(candidates) {
			end = len(candidates)
		}
		group := candidates[i:end]
		winner := group[0]
		for _, c := range group[1:] {
			winner = Compare(winner, c)
		}
		next = append(next, winner)
	}
	return RecursiveTournament(next, groupSize)
}

// DistillIntoPrompt renders summaries from prior attempts into a conditioning
// block for a new rollout (sequential PDR). The caller appends the returned
// text to its prompt; the function itself never touches message history, so
// the tool-call/tool-result protocol sequence is preserved.
func DistillIntoPrompt(prior []RolloutSummary) string {
	if len(prior) == 0 {
		return ""
	}
	var b strings.Builder
	b.WriteString("## Prior attempts (distilled trajectories — reuse progress, avoid failure modes)\n")
	for i, s := range prior {
		fmt.Fprintf(&b, "### Attempt %d — %s\n", i+1, s.Verdict)
		writeSection(&b, "Hypotheses", s.Hypotheses)
		writeSection(&b, "Progress", s.Progress)
		writeSection(&b, "Failures", s.Failures)
	}
	return b.String()
}

func writeSection(b *strings.Builder, name string, items []string) {
	if len(items) == 0 {
		return
	}
	fmt.Fprintf(b, "- %s:\n", name)
	for _, it := range items {
		fmt.Fprintf(b, "  - %s\n", it)
	}
}
