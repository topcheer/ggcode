package agent

import "testing"

// #1561 case A pin: mid-run spiral must fire within the streak window,
// not need S*3/7 consecutive failures over a lifelong denominator.
func Test1561StructuralDensityWindow(t *testing.T) {
	s := &failureModeState{fired: map[FailureMode]bool{}}
	// 50 successful calls first (the old math needed 22 consecutive fails).
	for i := 0; i < 50; i++ {
		s.recordResult("read_file", false, "")
	}
	fired := ""
	for i := 0; i < 6 && fired == ""; i++ {
		fired = s.recordResult("edit_file", true, "anchor not found")
	}
	if fired == "" {
		t.Fatal("true mid-run spiral (6 consecutive structural fails after 50 successes) must fire")
	}
	// Scattered FIXED failures never fire (streak reset already guards).
	s2 := &failureModeState{fired: map[FailureMode]bool{}}
	for round := 0; round < 10; round++ {
		s2.recordResult("edit_file", true, "anchor not found")
		s2.recordResult("edit_file", true, "anchor not found")
		s2.recordResult("edit_file", true, "anchor not found")
		if g := s2.recordResult("run_command", false, ""); g != "" {
			t.Fatalf("fixed-in-round failures must not fire, got %q", g)
		}
	}
}

// #1561 case C pin: scattered recovered transients never fire.
func Test1561TransientStreak(t *testing.T) {
	s := &failureModeState{fired: map[FailureMode]bool{}}
	for round := 0; round < 5; round++ {
		s.recordResult("run_command", true, "i/o timeout")
		s.recordResult("run_command", true, "context deadline exceeded")
		if g := s.recordResult("read_file", false, ""); g != "" {
			t.Fatalf("recovered timeout pair must not fire, got %q", g)
		}
	}
	// Three CONSECUTIVE transients fire.
	s3 := &failureModeState{fired: map[FailureMode]bool{}}
	s3.recordResult("run_command", true, "i/o timeout")
	s3.recordResult("run_command", true, "timeout")
	var fired string
	for i := 0; i < 2 && fired == ""; i++ {
		fired = s3.recordResult("run_command", true, "timed out")
	}
	if fired == "" {
		t.Fatal("3 consecutive transients must fire")
	}
}

// #1561 case D pin: fired latch resets on success (same-batch ordering).
func Test1561FiredRelatch(t *testing.T) {
	s := &failureModeState{fired: map[FailureMode]bool{}}
	for i := 0; i < 5; i++ {
		s.recordResult("edit_file", true, "anchor not found")
	}
	if !s.fired[FailureModeStructural] {
		t.Fatal("precondition: alert fired")
	}
	s.recordResult("run_command", false, "")
	if s.fired[FailureModeStructural] {
		t.Fatal("success must un-latch the premature fired flag")
	}
}

// #1561 case E pin: coordinate-embedded numbers are not status codes.
func Test1561TransientWordBoundary(t *testing.T) {
	if classifyFailureMode("run_command", "foo.go:429:5: syntax error") == FailureModeTransient {
		t.Fatal("file:line coordinate 429 must not classify TRANSIENT")
	}
	if classifyFailureMode("run_command", "ifoo.go:1502: missing") == FailureModeTransient {
		t.Fatal("coordinate 502 must not classify TRANSIENT")
	}
	if classifyFailureMode("run_command", "HTTP 429 Too Many Requests") != FailureModeTransient {
		t.Fatal("genuine HTTP 429 must classify TRANSIENT")
	}
	if classifyFailureMode("run_command", "error 503 service unavailable") != FailureModeTransient {
		t.Fatal("genuine 503 must classify TRANSIENT")
	}
	if classifyFailureMode("run_command", "14293 connection issue") == FailureModeTransient {
		t.Fatal("embedded digit run 14293 must not match 429")
	}
}

// #1561 case B pins: strict gate + scoped clearing.
func Test1561ChurnStrictScopedClear(t *testing.T) {
	c := &churnState{editCounts: map[string]int{}}
	c.recordEdit([]string{"/w/internal/a/foo.go", "/w/internal/b/bar.go"})
	// gofmt -l is NOT a strict verify - the agent.go gate never reaches
	// recordVerifySuccess for it, so the books survive.
	if isStrictVerifyCommand("gofmt -l .") {
		t.Fatal("gofmt -l must not be strict verify")
	}
	if c.editCounts["/w/internal/a/foo.go"] != 1 {
		t.Fatal("gofmt -l green must not clear churn books (gate upstream)")
	}
	// Scoped: go test ./internal/a only clears a/.
	c.recordVerifySuccess("go test ./internal/a/...")
	if _, ok := c.editCounts["/w/internal/a/foo.go"]; ok {
		t.Fatal("scoped command must clear its scope")
	}
	if c.editCounts["/w/internal/b/bar.go"] != 1 {
		t.Fatal("out-of-scope file must survive scoped clear")
	}
	// Repo-wide clears everything; make clean is not strict.
	if !isStrictVerifyCommand("go test ./...") || isStrictVerifyCommand("make clean") {
		t.Fatal("strict classification wrong")
	}
	c.recordVerifySuccess("go test ./...")
	if len(c.editCounts) != 0 {
		t.Fatal("repo-wide strict verify must clear all")
	}
}
