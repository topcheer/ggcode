package agent

// #3908 probes: separators inside quoted shell literals must not produce
// segments that satisfy the verify/evidence matchers, while real compound
// commands keep splitting.

import (
	"strings"
	"testing"
)

func TestIssue3908_QuotedSeparatorNotSplit(t *testing.T) {
	cmds := []string{
		`git commit -m "fix bug && go test ./..."`,
		`echo "a && go test"`,
		`grep "make test && go build" Makefile`,
		`git commit -m 'fix && go test ./...'`,
	}
	for _, cmd := range cmds {
		for _, seg := range splitCompoundCommand(cmd) {
			seg = strings.ToLower(strings.TrimSpace(stripEnvAssignments(seg)))
			if isVerifyCommandSegment(seg) {
				t.Fatalf("quoted literal must not yield a verify segment: %q -> %q", cmd, seg)
			}
			if isRealTestSegment(seg) {
				t.Fatalf("quoted literal must not yield a real-test segment: %q -> %q", cmd, seg)
			}
		}
	}
}

func TestIssue3908_RealCompoundStillSplits(t *testing.T) {
	segs := splitCompoundCommand("cd /app && go test ./... ; make verify-ci | tee log")
	if len(segs) != 4 {
		t.Fatalf("real compound must keep splitting, got %d: %q", len(segs), segs)
	}
	found := false
	for _, seg := range segs {
		if isVerifyCommandSegment(strings.TrimSpace(stripEnvAssignments(seg))) {
			found = true
		}
	}
	if !found {
		t.Fatal("real verify segments must still be recognized")
	}
	// Background form and empty-filtering parity with the old FieldsFunc.
	if got := splitCompoundCommand("sleep 5 &"); len(got) != 1 || strings.TrimSpace(got[0]) != "sleep 5" {
		t.Fatalf("background form regressed: %q", got)
	}
	// Consecutive separators: no EMPTY string segments (each emitted
	// segment has content - parity with the old FieldsFunc, which also
	// kept whitespace-only segments between separator runs).
	got := splitCompoundCommand("a && && b")
	if len(got) == 0 {
		t.Fatalf("expected segments, got none")
	}
	for _, seg := range got {
		if seg == "" {
			t.Fatalf("no empty string segments allowed: %q", got)
		}
	}
}
