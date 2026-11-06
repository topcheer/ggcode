package agent

import "testing"

func newGateForTest() *irrevGateState { return newIrrevGateState() }

func Test1798CurrentCallNotOwnGrounding(t *testing.T) {
	s := newGateForTest()
	if w := s.recordAction("run_command", `{"command":"make build"}`); w == "" {
		t.Fatal("under-grounded run_command (Medium) must warn - self-grounding made it unreachable")
	}

	s2 := newGateForTest()
	s2.recordAction("read_file", `{}`)
	if w := s2.recordAction("run_command", `{"command":"make build"}`); w != "" {
		t.Fatalf("grounded run_command must stay quiet, got: %s", w)
	}
}

func Test1798FileOpsRecursiveDeleteHigh(t *testing.T) {
	s := newGateForTest()
	if w := s.recordAction("file_ops", `{"operations":[{"action":"delete","source":"/tmp/x","recursive":true}]}`); w == "" {
		t.Fatal("recursive dir delete with zero grounding must warn (High tier)")
	}

	s2 := newGateForTest()
	s2.recordAction("read_file", `{}`)
	if w := s2.recordAction("file_ops", `{"operations":[{"action":"delete","source":"/tmp/x/f.txt"}]}`); w != "" {
		t.Fatalf("single-file delete with 1 grounding must stay quiet, got: %s", w)
	}
}

// #5129 note: a recursive delete expressed WITHOUT the explicit recursive
// flag (e.g. glob path /tmp/x/*) is classified Medium (flag-shaped High);
// the flagged form stays High per #1798. The unflagged form still gates
// via Medium when under-grounded, so the safety net holds.
func Test1798FileOpsGlobDeleteStillGated(t *testing.T) {
	s := newGateForTest()
	// Non-grounding placeholder so this is NOT the exempted first action.
	s.recordAction("edit_file", `{}`)
	glob := `{"operations":[{"action":"delete","source":"/tmp/x/*"}]}`
	if w := s.recordAction("file_ops", glob); w == "" {
		t.Error("under-grounded glob dir delete must still warn via Medium tier")
	}
}

func Test1798ShellStashDropHigh(t *testing.T) {
	s := newGateForTest()
	if w := s.recordAction("run_command", `{"command":"git stash drop stash@{0}"}`); w == "" {
		t.Fatal("shell stash drop with zero grounding must warn")
	}
}

func Test1798GroundedHighQuiet(t *testing.T) {
	s := newGateForTest()
	s.recordAction("run_command", `{"command":"go test ./..."}`)
	s.recordAction("git_status", `{}`)
	if w := s.recordAction("run_command", `{"command":"git push --force"}`); w != "" {
		t.Fatalf("2 prior groundings must satisfy High threshold, got: %s", w)
	}
}
