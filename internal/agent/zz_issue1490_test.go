package agent

// #1490 regressions:
//   - A: ErrorRate/ToolEfficiency must use the untruncated ErrorCount,
//     not len(Errors) (capped at 10 for the reflection prompt) - a run
//     with 22 failures in 25 calls used to score 10/25.
//   - C: an inner for-range's skip-invalid pattern (err != nil ->
//     continue) must NOT be absorbed as retry evidence for the OUTER
//     loop, which then got flagged missing-backoff.

import (
	"go/parser"
	"go/token"
	"testing"
)

// Case A: 15 recorded errors, 25 tool calls - ErrorRate must reflect
// 15 (old code read len(Errors)=10).
func TestIssue1490A_ErrorCountNotCapped(t *testing.T) {
	s := &RunStats{Iterations: 20, Success: false}
	s.ToolCalls = map[string]int{"edit_file": 25}
	for i := 0; i < 15; i++ {
		s.recordToolError("edit_file", "boom")
	}
	if s.ErrorCount != 15 {
		t.Fatalf("ErrorCount = %d, want 15 (counter must be uncapped)", s.ErrorCount)
	}
	if len(s.Errors) != 10 {
		t.Fatalf("Errors list = %d entries, want 10 (prompt cap stays)", len(s.Errors))
	}
	scorer := NewResponseQualityScorer(10)
	entry := scorer.computeScore(s, "test", "m")
	if entry.Signals.ErrorRate < 0.74 { // 15/20; old code computed 10/20 = 0.5
		t.Fatalf("ErrorRate = %.2f, want >= 0.74 (15 errors / 20 iterations)", entry.Signals.ErrorRate)
	}
	if entry.Signals.ToolEfficiency > 0.41 { // (25-15)/25 = 0.4; old code (25-10)/25 = 0.6
		t.Fatalf("ToolEfficiency = %.2f, want <= 0.41 (10 failed of 25)", entry.Signals.ToolEfficiency)
	}
}

// Case C: the issue's exact shape - an unbounded outer loop whose body
// is an inner for-range with a skip-invalid continue. The outer loop is
// NOT a retry loop (no error-continue at its own level) and must not be
// flagged missing-backoff.
func TestIssue1490C_InnerRangeContinueNotAbsorbed(t *testing.T) {
	src := `package x
func drain(w writer, q []item) {
	for len(q) > 0 {
		for _, it := range q {
			if err := w.write(it); err != nil {
				continue
			}
		}
		q = q[:0]
	}
}`
	fset := token.NewFileSet()
	file, err := parser.ParseFile(fset, "drain.go", src, 0)
	if err != nil {
		t.Fatal(err)
	}
	issues := findRetryLoopIssues(file, fset, map[string]bool{})
	for _, iss := range issues {
		if iss.kind == "missing-backoff" {
			t.Fatalf("outer loop must not be flagged missing-backoff: %s", iss.message)
		}
	}
}

// Case C companion: a REAL retry loop (error-continue at the loop's own
// level, no backoff) must still be flagged - the barrier only stops
// nested-loop absorption, not genuine detection.
func TestIssue1490C_RealRetryLoopStillFlagged(t *testing.T) {
	src := `package x
func poll(c client) {
	for {
		if err := c.Get(); err != nil {
			continue
		}
		return
	}
}`
	fset := token.NewFileSet()
	file, err := parser.ParseFile(fset, "poll.go", src, 0)
	if err != nil {
		t.Fatal(err)
	}
	issues := findRetryLoopIssues(file, fset, map[string]bool{})
	found := false
	for _, iss := range issues {
		if iss.kind == "missing-backoff" {
			found = true
		}
	}
	if !found {
		t.Fatal("genuine retry loop without backoff must still be flagged")
	}
}
