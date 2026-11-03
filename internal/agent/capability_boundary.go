package agent

import (
	"fmt"
	"regexp"
	"strings"
)

const (
	capBoundaryPivotThreshold = 3

	capBoundaryMaxWarnings = 1
)

var approachPivotRe = regexp.MustCompile(`(?i)(?:` +
	`let\s+me\s+try\s+(?:a\s+)?(?:different|another|alternative|new)\s+(?:approach|strategy|way|method|solution|tactic)` +
	`|alternatively[,!.]` +
	`|another\s+(?:approach|strategy|way|method|option)\s+(?:would|could|to|is)` +
	`|let\s+me\s+(?:reconsider|rethink|revisit|take\s+a\s+step\s+back)` +
	`|i\s*'ll\s+(?:try\s+)?(?:a\s+|an\s+)?(?:different|another|alternative)\s+(?:approach|strategy|way|tactic)` +
	`|switching\s+(?:to|approach|strategy)` +
	`|pivoting\s+(?:to|from)` +
	`|instead\s+of\s+.{2,60}[,;.]\s+let(?:'s|\s+me)\s+` +
	`|different\s+(?:tack|strategy|approach)` +
	`)`)

type approachPivotHit struct {
	excerpt string
}

type capabilityBoundaryState struct {
	warnings   int
	pivots     []approachPivotHit
	failErrors int
}

func newCapabilityBoundaryState() *capabilityBoundaryState {
	return &capabilityBoundaryState{}
}

func (s *capabilityBoundaryState) reset() {
	s.warnings = 0
	s.pivots = nil
	s.failErrors = 0
}

func (s *capabilityBoundaryState) recordApproachPivot(excerpt string) {
	s.pivots = append(s.pivots, approachPivotHit{excerpt: excerpt})
}

func (s *capabilityBoundaryState) recordToolResult(isError bool) {
	if isError {
		s.failErrors++
	} else {
		s.failErrors = 0
	}
}

func scanApproachPivots(text string) []approachPivotHit {
	if len(text) == 0 {
		return nil
	}
	var hits []approachPivotHit
	matches := approachPivotRe.FindAllStringIndex(text, -1)
	for _, m := range matches {
		start := m[0]
		excerptStart := start - 20
		if excerptStart < 0 {
			excerptStart = 0
		}
		excerptEnd := m[1] + 40
		if excerptEnd > len(text) {
			excerptEnd = len(text)
		}
		excerpt := strings.TrimSpace(text[excerptStart:excerptEnd])
		if len(excerpt) > 80 {
			excerpt = excerpt[:80] + "..."
		}
		hits = append(hits, approachPivotHit{excerpt: excerpt})
	}
	return hits
}

func (a *Agent) maybeWarnCapabilityBoundary(text string) string {
	if a.capBoundary == nil {
		return ""
	}
	if a.capBoundary.warnings >= capBoundaryMaxWarnings {
		return ""
	}

	newPivots := scanApproachPivots(text)
	for _, np := range newPivots {
		a.capBoundary.recordApproachPivot(np.excerpt)
	}

	if len(a.capBoundary.pivots) < capBoundaryPivotThreshold {
		return ""
	}
	if a.capBoundary.failErrors < 2 {

		return ""
	}

	a.capBoundary.warnings++

	var excerpts []string
	for idx, ph := range a.capBoundary.pivots {
		if idx >= 3 {
			break
		}
		excerpts = append(excerpts, fmt.Sprintf("  pivot #%d: ...%s...", idx+1, ph.excerpt))
	}

	msg := fmt.Sprintf("[capability-boundary] %d distinct approach pivots detected with %d+ recent failures. ",
		len(a.capBoundary.pivots), a.capBoundary.failErrors)
	msg += "This pattern suggests the problem may be at the edge of autonomous solvability. "
	msg += "The agent has tried multiple fundamentally different strategies and all have failed. "
	msg += "Metacognitive self-assessment (ICML 2025, arXiv:2506.05109) indicates that recognizing "
	msg += "capability boundaries is critical for efficient agent operation. "
	msg += "Consider: (1) Escalate to the user - explain what you've tried and ask for guidance. "
	msg += "(2) Re-examine the root cause - all approaches failing suggests a shared underlying issue. "
	msg += "(3) Simplify - try the simplest possible reproduction before another fix.\n"
	msg += "Detected strategy pivots:\n" + strings.Join(excerpts, "\n") + "\n"
	return msg
}
