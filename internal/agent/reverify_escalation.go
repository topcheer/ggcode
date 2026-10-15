package agent

// Stale-Observation Re-Verification Escalation ("Belief Trapping" recovery)
//
// Research basis: PoS - explicit belief states with belief-trapping detection
// and recovery (arXiv:2610.01415). PoS's key finding is that DETECTING a
// stale belief is necessary but not sufficient: the differentiated mechanism
// is RECOVERY - when trapped in an outdated world model, the agent must
// re-execute the stale observation (re-validate the world) rather than
// accumulate more textual reminders.
//
// Gap this closes (found by research round sa-134, 2026-10):
//   - Detection side is deep: false premise (world-model drift, freshness
//     window 2 turns), cross-turn contradiction, belief-defense escalation,
//     bridging rationalization all exist.
//   - Recovery side is shallow: every detector's recovery action is a
//     one-shot injectGuidance nudge.
//   - The agent.go tool-result recording block COMMENT promises "record
//     negative observations from *successful* tool calls (no-match,
//     not-found, empty) for later delayed contradiction analysis" - but no
//     implementation backs that comment. This file is that implementation
//     plus the PoS-style recovery channel.
//
// Trigger (deliberately narrow, to avoid the noise trade-off that killed
// the satisficing/premature-surrender detectors in 387282a6):
//   1. A SUCCESSFUL tool call returned a negative observation (no-match /
//      not-found / empty indicators in non-error output), AND
//   2. the observation is aged (>= reverifyStaleAgeIters iterations), AND
//   3. file-mutating work happened after the observation, AND
//   4. the assistant now makes a positive/success claim that does not
//      acknowledge any error.
// Then: escalate to a HARD re-run instruction (not a lexical nudge) and
// feed the compounded-uncertainty accumulator.
//
// We never auto-execute the tool (permission/side-effect safety) - the
// recovery is an instruction upgrade, from "note this" to "re-run and cite
// the new output before further edits".
//
// Distinct from:
//   - false_premise_check.go: freshness window is 2 turns and it reacts to
//     tool ERRORS; this reacts to aged negatives from SUCCESSFUL calls
//     (e.g. "no matches" searches the agent keeps reasoning from).
//   - redundant_reverify.go: it discourages REPEATED re-verification; this
//     DEMANDS re-verification when a mutation may have invalidated a
//     negative observation the agent still relies on. The two compose:
//     re-running once on demand is not redundant re-verification.
//   - temporal_blindness family: those watch verification claims about
//     MUTATED code; this watches negative observations whose staleness is
//     caused by later mutations (the search space changed).

import (
	"fmt"
	"strings"
)

const (
	// reverifyMaxWarnings: max escalations per run (noise cap).
	reverifyMaxWarnings = 2

	// reverifyStaleAgeIters: an observation must be at least this many
	// iterations old before a contradicting positive claim escalates.
	// Younger contradictions are handled by the false-premise detector's
	// freshness window; this channel targets aged ("delayed") traps.
	reverifyStaleAgeIters = 6

	// reverifyMaxObservations: cap stored observations to bound memory.
	reverifyMaxObservations = 20

	// weightStaleObs: units contributed to the compounded-uncertainty
	// accumulator per escalation - between hedging (1.0) and false premise
	// (2.0): a stale negative being built upon is riskier than hedging but
	// the claim is not yet proven wrong.
	weightStaleObs = 1.5
)

// reverifyMutationTools mutate files and can invalidate earlier negative
// observations (a "no matches" grep may now match after an edit).
var reverifyMutationTools = map[string]bool{
	"edit_file":        true,
	"multi_edit_file":  true,
	"multi_file_edit":  true,
	"write_file":       true,
	"multi_file_write": true,
	"batch_replace":    true,
	"file_ops":         true,
	"notebook_edit":    true,
}

// negativeObservation is a no-match/not-found/empty result from a
// SUCCESSFUL tool call.
type negativeObservation struct {
	toolName   string
	argSummary string
	snippet    string
	iteration  int
	consumed   bool
}

// reverifyState tracks negative observations and mutation recency for the
// stale-observation escalation.
type reverifyState struct {
	observations     []negativeObservation
	lastMutationIter int
	warnings         int
}

func newReverifyState() *reverifyState {
	return &reverifyState{lastMutationIter: -1}
}

func (s *reverifyState) reset() {
	s.observations = nil
	s.lastMutationIter = -1
	s.warnings = 0
}

// recordToolResult feeds the escalation tracker with every tool result.
//   - Mutation tools advance lastMutationIter.
//   - Successful negative results (no-match/not-found/empty indicators in
//     non-error output) are recorded as observations.
//   - A later successful, NON-negative result from the same tool supersedes
//     that tool's unconsumed observations (newer evidence replaced the old
//     negative - same supersede semantics as false premise #331).
func (s *reverifyState) recordToolResult(toolName, args, resultContent string, isError bool, iter int) {
	if reverifyMutationTools[toolName] {
		if iter > s.lastMutationIter {
			s.lastMutationIter = iter
		}
		return
	}
	if isError {
		// Tool errors are the false-premise detector's domain.
		return
	}
	negative := indicatesNoResult(resultContent) || indicatesNotFound(resultContent)
	if !negative {
		// Supersede: same tool now returns real content; drop its stale
		// negatives so a later positive claim does not false-fire.
		kept := s.observations[:0]
		for _, o := range s.observations {
			if o.toolName != toolName || o.consumed {
				kept = append(kept, o)
			}
		}
		s.observations = kept
		return
	}
	s.observations = append(s.observations, negativeObservation{
		toolName:   toolName,
		argSummary: reverifySummarizeArgs(args),
		snippet:    reverifyFirstLine(resultContent),
		iteration:  iter,
	})
	if len(s.observations) > reverifyMaxObservations {
		s.observations = s.observations[len(s.observations)-reverifyMaxObservations:]
	}
}

// checkStaleTrap returns a hard re-run instruction when the assistant's
// positive claim rests on an aged negative observation that later mutations
// may have invalidated. Empty string = no escalation.
func (s *reverifyState) checkStaleTrap(assistantText string, iter int) string {
	if s.warnings >= reverifyMaxWarnings {
		return ""
	}
	lowered := strings.ToLower(assistantText)
	if !matchesGenericSuccessClaim(lowered) || acknowledgesError(lowered) {
		return ""
	}
	for idx := range s.observations {
		o := &s.observations[idx]
		if o.consumed {
			continue
		}
		aged := iter-o.iteration >= reverifyStaleAgeIters
		mutatedAfter := s.lastMutationIter >= o.iteration
		if !aged || !mutatedAfter {
			continue
		}
		o.consumed = true
		s.warnings++
		return fmt.Sprintf(
			"[stale-observation-reverify] Your success claim relies on a negative observation from iteration %d: "+
				"%s %s returned \"%s\". That observation is now stale - file changes have happened since. "+
				"Re-run that observation (%s %s) and cite the new output BEFORE making further edits or completion claims based on it. "+
				"If the fresh evidence contradicts your claim, revise the claim.",
			o.iteration, o.toolName, o.argSummary, o.snippet, o.toolName, o.argSummary)
	}
	return ""
}

// reverifySummarizeArgs renders tool arguments for the instruction message.
func reverifySummarizeArgs(args string) string {
	args = strings.TrimSpace(args)
	if args == "" {
		return ""
	}
	first := strings.SplitN(args, "\n", 2)[0]
	if len(first) > 80 {
		first = first[:80] + "..."
	}
	return first
}

// reverifyFirstLine extracts the first meaningful line of a tool result for
// the instruction excerpt.
func reverifyFirstLine(content string) string {
	for _, ln := range strings.Split(content, "\n") {
		ln = strings.TrimSpace(ln)
		if ln != "" {
			if len(ln) > 100 {
				ln = ln[:100] + "..."
			}
			return ln
		}
	}
	return ""
}
