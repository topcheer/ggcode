package agent

import (
	"go/parser"
	"go/token"
	"testing"
)

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
	if entry.Signals.ErrorRate < 0.74 {
		t.Fatalf("ErrorRate = %.2f, want >= 0.74 (15 errors / 20 iterations)", entry.Signals.ErrorRate)
	}
	if entry.Signals.ToolEfficiency > 0.41 {
		t.Fatalf("ToolEfficiency = %.2f, want <= 0.41 (10 failed of 25)", entry.Signals.ToolEfficiency)
	}
}

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
