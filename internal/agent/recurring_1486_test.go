package agent

import "testing"

// #1486 case D: sibling packages must not collide into one fingerprint.
func Test1486PathPrefixKeepsSiblingPackages(t *testing.T) {
	a := stripPathToBasename("a/util.go:42")
	b := stripPathToBasename("b/util.go:42")
	if a == b {
		t.Fatalf("a/util.go and b/util.go must fingerprint apart, got %q both", a)
	}
	if stripPathToBasename("./internal/agent/foo.go:1") != "internal/agent/foo.go:1" {
		t.Fatalf("root-marker strip: %q", stripPathToBasename("./internal/agent/foo.go:1"))
	}
	// The same error in the same package spelled two ways still merges
	// (the spelling tolerance the original pattern existed for).
	if stripPathToBasename("./internal/agent/foo.go:1") != stripPathToBasename("internal/agent/foo.go:1") {
		t.Fatal("same package with/without ./ must fingerprint equal")
	}
	// ../ forms strip too.
	if stripPathToBasename("../pkg/x.go:3") != "pkg/x.go:3" {
		t.Fatalf("../ strip: %q", stripPathToBasename("../pkg/x.go:3"))
	}
	// A bare basename with no directory passes through untouched.
	if stripPathToBasename("main.go:7") != "main.go:7" {
		t.Fatalf("bare basename must be untouched, got %q", stripPathToBasename("main.go:7"))
	}
}

// #1486 case E: the !IsError gate lives at the agent call site - a failed
// edit never reaches recordEdit, so identical re-verify still warns; a
// successful edit bumps editsSince and legitimately suppresses the warning.
func Test1486EditGateSemantics(t *testing.T) {
	args := `{"command":"go test ./..."}`

	// Failed edit path: agent.go no longer calls recordEdit, so the state
	// sees zero edits between identical verifies -> warns.
	s := newRedundantReverifyState()
	s.recordToolCall("run_command", args, 1, false)
	if h := s.recordToolCall("run_command", args, 2, false); h == "" {
		t.Fatal("identical re-verify with no landed edit must still warn")
	}

	// Successful edit path: recordEdit runs, editsSince=1 -> no warning.
	s2 := newRedundantReverifyState()
	s2.recordToolCall("run_command", args, 1, false)
	s2.recordEdit("edit_file")
	if h := s2.recordToolCall("run_command", args, 2, false); h != "" {
		t.Fatal("re-verify after a landed edit must not warn")
	}
}
