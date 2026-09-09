package agent

import "testing"

// #1798: four gate-integrity regressions.
func newGateForTest() *irrevGateState { return newIrrevGateState() }

// Case 2: a lone run_command (grounding itself, Medium tier) must now WARN -
// the window must not count the current call as its own evidence.
func Test1798CurrentCallNotOwnGrounding(t *testing.T) {
	s := newGateForTest()
	if w := s.recordAction("run_command", `{"command":"go build ./..."}`); w == "" {
		t.Fatal("under-grounded run_command (Medium) must warn - self-grounding made it unreachable")
	}
	// With one PRIOR grounding, the window has evidence -> quiet.
	s2 := newGateForTest()
	s2.recordAction("read_file", `{}`)
	if w := s2.recordAction("run_command", `{"command":"go build ./..."}`); w != "" {
		t.Fatalf("grounded run_command must stay quiet, got: %s", w)
	}
}

// Case 3: file_ops delete+recursive is High (parity with rm -rf).
func Test1798FileOpsRecursiveDeleteHigh(t *testing.T) {
	s := newGateForTest()
	if w := s.recordAction("file_ops", `{"operations":[{"action":"delete","source":"/tmp/x","recursive":true}]}`); w == "" {
		t.Fatal("recursive dir delete with zero grounding must warn (High tier)")
	}
	// Plain single-file delete keeps Medium: one grounding suffices.
	s2 := newGateForTest()
	s2.recordAction("read_file", `{}`)
	if w := s2.recordAction("file_ops", `{"operations":[{"action":"delete","source":"/tmp/x/f.txt"}]}`); w != "" {
		t.Fatalf("single-file delete with 1 grounding must stay quiet, got: %s", w)
	}
}

// Case 4: shell stash drop reaches the High pattern table.
func Test1798ShellStashDropHigh(t *testing.T) {
	s := newGateForTest()
	if w := s.recordAction("run_command", `{"command":"git stash drop stash@{0}"}`); w == "" {
		t.Fatal("shell stash drop with zero grounding must warn")
	}
}

// Regression: well-grounded destructive action stays quiet.
func Test1798GroundedHighQuiet(t *testing.T) {
	s := newGateForTest()
	s.recordAction("run_command", `{"command":"go test ./..."}`)
	s.recordAction("git_status", `{}`)
	if w := s.recordAction("run_command", `{"command":"git push --force"}`); w != "" {
		t.Fatalf("2 prior groundings must satisfy High threshold, got: %s", w)
	}
}
