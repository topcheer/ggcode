package agent

import "testing"

// #1494 case A pin: Record + reset wiring exists and tiers fire.
func Test1494SessionBudgetTiers(t *testing.T) {
	a := &Agent{}
	a.SetSessionTokenBudget(1000)
	a.resetSessionTokenUsage()
	if _, stop := a.RecordSessionTokenUsage(900, 0); stop {
		t.Fatal("80% must be guidance, not stop")
	}
	msg, stop := a.RecordSessionTokenUsage(60, 0) // 96%
	if stop || msg == "" {
		t.Fatalf("95%% must warn: stop=%v msg=%q", stop, msg)
	}
	_, stop = a.RecordSessionTokenUsage(100, 0) // 106%
	if !stop {
		t.Fatal("100%+ must stop")
	}
	// Reset clears the latched stop (per-run freshness).
	a.resetSessionTokenUsage()
	if _, stop := a.RecordSessionTokenUsage(10, 0); stop {
		t.Fatal("after reset the budget must re-arm")
	}
}

// #1494 case C pin: editing a source file addresses a command-class error.
func Test1494EditAddressesCommandError(t *testing.T) {
	st := newSilentErrorStateFor1494()
	st.recordToolError("run_command", "go build ./...", "exit status 1", 1)
	if !st.actionAddressesError("/src/foo.go", "edit_file") {
		t.Fatal("file edit must address a command-class error (textbook fix flow)")
	}
}

// #1494 case D pin: uncovered-tool successes are not advancement.
func Test1494UncoveredToolNoAdvancement(t *testing.T) {
	st := newSilentErrorStateFor1494()
	st.recordToolError("edit_file", "/a/foo.go", "anchor not found", 1)
	for i := 0; i < 8; i++ {
		if msg := st.recordToolAction("lsp_diagnostics", ""); msg != "" {
			t.Fatalf("uncovered tool must be silent, got %q", msg)
		}
	}
}

// #1494 case E pins: empty-key errors survive unrelated clears; sibling
// paths do not cross-clear.
func Test1494ClearBoundaryAndEmptyKey(t *testing.T) {
	st := newSilentErrorStateFor1494()
	st.recordToolError("web_search", "", "boom", 1)
	st.recordToolError("edit_file", "/a/foo.go", "anchor", 2)
	st.clearAddressedErrors("/a/foo.go")
	if len(st.unresolvedErrors) != 1 {
		t.Fatalf("empty-key error must survive an unrelated clear, got %d", len(st.unresolvedErrors))
	}
	st2 := newSilentErrorStateFor1494()
	st2.recordToolError("edit_file", "/a/foo.go", "anchor", 1)
	st2.clearAddressedErrors("/a/foobar.go")
	if len(st2.unresolvedErrors) != 1 {
		t.Fatal("/a/foobar.go must not clear /a/foo.go (byte-prefix, not path-prefix)")
	}
}

func newSilentErrorStateFor1494() *silentErrorState {
	return &silentErrorState{}
}
