package agentruntime

import (
	"fmt"
	"sort"
	"strings"
)

type TrajectoryEntry struct {
	Kind string
	Text string
}

type RolloutSummary struct {
	Hypotheses []string
	Progress   []string
	Failures   []string
	Verdict    string
}

const maxDetailEntries = 5

var failureSignals = map[string]int{
	"error":    3,
	"failed":   3,
	"panic":    4,
	"fatal":    4,
	"timeout":  2,
	"denied":   1,
	"conflict": 2,
}

var progressSignals = []string{
	"pass", "succeed", "ok:", "no error", "built", "committed", "created", "verified",
}

var negationSignals = []string{
	"no error", "no errors", "0 errors", "0 error",
	"no failures", "0 failures", "without error",
}

var resolutionSignals = []string{
	"fixed", "resolved", "now passes", "now pass", "already fixed",
}

var hypothesisSignals = []string{
	"plan:", "approach:", "attempt", "try", "hypothesis", "strategy", "fix:",
}

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

		positive := kind != "error" && positiveStatement(lower)

		switch {
		case !positive && (kind == "error" || hasSignal(lower, failureSignalsKeys)):
			anyFailure = true
			s.addFailure(compactLine(text, 200))
		case kind == "result" || kind == "note" || hasAny(lower, progressSignals):
			if !positive && looksLikeFailure(lower) {
				anyFailure = true
				s.addFailure(compactLine(text, 200))
				continue
			}
			anyProgress = true
			s.addProgress(compactLine(text, 200))
		case kind == "action" || hasAny(lower, hypothesisSignals):
			s.addHypothesis(compactLine(text, 160))
		default:

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

	for _, existing := range *dst {
		if existing == v {
			return
		}
	}
	*dst = append(*dst, v)
}

var failureSignalsKeys = func() []string {
	keys := make([]string, 0, len(failureSignals))
	for k := range failureSignals {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	return keys
}()

func hasSignal(lower string, keys []string) bool { return hasAny(lower, keys) }

func hasAny(lower string, signals []string) bool {
	for _, sig := range signals {
		if strings.Contains(lower, sig) {
			return true
		}
	}
	return false
}

func positiveStatement(lower string) bool {
	if hasAny(lower, negationSignals) {
		return true
	}
	return hasAny(lower, resolutionSignals) && !strings.Contains(lower, "fail")
}

func looksLikeFailure(lower string) bool {
	return strings.Contains(lower, "fail") || strings.Contains(lower, "error")
}

func compactLine(text string, limit int) string {
	text = strings.Join(strings.Fields(text), " ")
	r := []rune(text)
	if len(r) <= limit {
		return text
	}
	return string(r[:limit]) + "…"
}

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

	v += len(s.Hypotheses) / 2
	return v
}

func Compare(a, b RolloutSummary) RolloutSummary {
	sa, sb := a.score(), b.score()
	if sa > sb {
		return a
	}
	if sb > sa {
		return b
	}

	if strings.Join(b.Progress, "|")+strings.Join(b.Failures, "|") <
		strings.Join(a.Progress, "|")+strings.Join(a.Failures, "|") {
		return b
	}
	return a
}

func RecursiveTournament(candidates []RolloutSummary, groupSize int) RolloutSummary {
	if len(candidates) == 0 {
		return RolloutSummary{Verdict: "failed"}
	}
	if len(candidates) == 1 {
		return candidates[0]
	}
	if groupSize < 2 {
		groupSize = 4
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

func DistillIntoPrompt(prior []RolloutSummary) string {
	if len(prior) == 0 {
		return ""
	}
	var b strings.Builder
	b.WriteString("## Prior attempts (distilled trajectories  -  reuse progress, avoid failure modes)\n")
	for i, s := range prior {
		fmt.Fprintf(&b, "### Attempt %d  -  %s\n", i+1, s.Verdict)
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
