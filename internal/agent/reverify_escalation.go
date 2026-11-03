package agent

import (
	"fmt"
	"strings"
)

const (
	reverifyMaxWarnings = 2

	reverifyStaleAgeIters = 6

	reverifyMaxObservations = 20

	weightStaleObs = 1.5
)

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

type negativeObservation struct {
	toolName   string
	argSummary string
	snippet    string
	iteration  int
	seq        int
	consumed   bool
}

type reverifyState struct {
	observations     []negativeObservation
	lastMutationIter int
	lastMutationSeq  int
	recordSeq        int
	warnings         int
}

func newReverifyState() *reverifyState {
	return &reverifyState{lastMutationIter: -1, lastMutationSeq: -1}
}

func (s *reverifyState) reset() {
	s.observations = nil
	s.lastMutationIter = -1
	s.lastMutationSeq = -1
	s.recordSeq = 0
	s.warnings = 0
}

func (s *reverifyState) recordToolResult(toolName, args, resultContent string, isError bool, iter int) {
	s.recordSeq++
	if reverifyMutationTools[toolName] {

		if !isError {
			if iter > s.lastMutationIter {
				s.lastMutationIter = iter
			}
			s.lastMutationSeq = s.recordSeq
		}
		return
	}
	if isError {

		return
	}
	negative := indicatesNoResult(resultContent) || indicatesNotFound(resultContent)
	if !negative {

		curSummary := reverifySummarizeArgs(args)
		kept := s.observations[:0]
		for _, o := range s.observations {
			if o.toolName != toolName || o.argSummary != curSummary || o.consumed {
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
		seq:        s.recordSeq,
	})
	if len(s.observations) > reverifyMaxObservations {
		s.observations = s.observations[len(s.observations)-reverifyMaxObservations:]
	}
}

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

		mutatedAfter := s.lastMutationSeq > o.seq
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
