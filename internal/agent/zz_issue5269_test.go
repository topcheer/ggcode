package agent

import "testing"

// #5269 pins: GET-class patrol calls must not trip the zero-grounding
// gate. Root FP: the shell-segment splitter was quote-blind, so quoted jq
// programs (`-q '.[] | select(.x) | .number]'`) were shredded on the pipes
// inside the quotes; the fragments failed the read-only table and every
// single-call patrol round gated. Mutating verbs keep full gating.

func TestIssue5269_GetPatrolWithQuotedJqPipesNoGate(t *testing.T) {
	// Exact patrol shape from the observed FP: cd && date && gh api GET with
	// a jq program whose pipes live inside single quotes.
	s := newIrrevGateState()
	warn := s.recordAction("run_command", `{"command":"cd /Volumes/new/ggai/ggcode && date '+%H:%M' && gh api 'repos/topcheer/ggcode/issues?state=open&per_page=100' -q '[.[] | select(.pull_request == null) | .number] | sort'"}`)
	if warn != "" {
		t.Fatalf("GET patrol with quoted jq pipes must not gate:\n%s", warn)
	}
	if got := irrevClassifyTool("run_command", `{"command":"gh api 'x' -q '.a | .b'"}`); got != irrevTierNone {
		t.Fatalf("gh api GET with quoted jq = tier %d, want None(0)", got)
	}
}

func TestIssue5269_MutatingGhApiStillGates(t *testing.T) {
	s := newIrrevGateState()
	// Explicit mutating method: -X POST must keep full gating.
	warn := s.recordAction("run_command", `{"command":"gh api -X POST repos/x/y -f title=t"}`)
	if warn == "" {
		t.Fatal("gh api -X POST with 0 grounding must still gate")
	}
	// Field flags imply a write payload even without -X (conservative).
	if got := irrevClassifyTool("run_command", `{"command":"gh api repos/x/y -f title=t"}`); got < irrevTierMedium {
		t.Fatalf("gh api -f (write payload) = tier %d, want >= Medium", got)
	}
}

func TestIssue5269_ForLoopReadOnlyBodyNoGate(t *testing.T) {
	// Patrol three-check loop shape: control keywords are transparent, the
	// read-only body carries the classification.
	s := newIrrevGateState()
	warn := s.recordAction("run_command", `{"command":"for n in 5258 5259; do echo \"--#$n--\"; gh api repos/o/r/issues/$n/comments -q '.[].body[0:55]' | head -3; done"}`)
	if warn != "" {
		t.Fatalf("read-only for/do/done patrol loop must not gate:\n%s", warn)
	}
}

func TestIssue5269_ForLoopMutatingBodyStillGates(t *testing.T) {
	s := newIrrevGateState()
	warn := s.recordAction("run_command", `{"command":"for f in a b; do rm -rf $f; done"}`)
	if warn == "" {
		t.Fatal("for/do/done with a destructive body must still gate (High)")
	}
}

func TestIssue5269_UnquotedRedirectStillGates(t *testing.T) {
	// Redirects OUTSIDE quotes write files - not read-only. (2>/dev/null etc.
	// are neutralized earlier; a real `> file` target must keep gating.)
	if got := irrevClassifyTool("run_command", `{"command":"echo hi > /tmp/x.txt"}`); got < irrevTierMedium {
		t.Fatalf("unquoted redirect = tier %d, want >= Medium", got)
	}
}

func TestIssue5269_QuotedPipeGlyphIsNotASegmentBreak(t *testing.T) {
	segs := irrevSplitShellSegments(`gh api 'x?y=1' -q '.a | .b' && date '+%F'`)
	if len(segs) != 2 {
		t.Fatalf("quote-aware split: got %d segments %q, want 2", len(segs), segs)
	}
}
