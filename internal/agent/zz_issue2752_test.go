package agent

// #2752 regression: Phase 3 must validate the re-run command itself, not
// just the tool name. Bare reproducerRunToolNames matching let ANY
// run_command/start_command ("git diff", "ls", "echo") clear the rerun
// defense, so the detector went silent exactly for its headline failure
// mode (SWE-bench #1: edit, never re-run the reproducer, claim success).
// Also pins the grace period after the fertilityWindow->rerunGrace
// constant rename: behavior must be identical to the old literal 2.

import "testing"

// Intermediate-state commands must NOT clear the defense.
func TestIssue2752_NonScriptRunCommandDoesNotClearDefense(t *testing.T) {
	s := newReproducerLifecycleState()
	s.observeToolCalls(1, []string{"run_command"}, []string{`{"command":"python3 repro.py"}`})
	s.observeToolCalls(2, []string{"edit_file"}, []string{`{"file_path":"src/main.go"}`})

	// Iter 3: agent runs inspection commands, NOT the reproducer script.
	s.observeToolCalls(3, []string{"run_command"}, []string{`{"command":"git diff"}`})
	s.observeToolCalls(4, []string{"run_command"}, []string{`{"command":"ls -la"}`})
	s.observeToolCalls(5, []string{"start_command"}, []string{`{"command":"echo done"}`})

	if s.reranAfterEdit {
		t.Fatal("git diff / ls / echo must not count as reproducer re-run")
	}
	hint := s.checkIncomplete(6)
	if hint == "" {
		t.Fatal("defense must stay armed after non-script commands")
	}
}

// A real script-shaped re-run must still clear the defense (both phases
// use the same reproducerCommandRe predicate).
func TestIssue2752_ScriptRerunStillClearsDefense(t *testing.T) {
	s := newReproducerLifecycleState()
	s.observeToolCalls(1, []string{"run_command"}, []string{`{"command":"python3 repro.py"}`})
	s.observeToolCalls(2, []string{"edit_file"}, []string{`{"file_path":"src/main.go"}`})

	s.observeToolCalls(3, []string{"run_command"}, []string{`{"command":"python3 repro.py"}`})
	if !s.reranAfterEdit {
		t.Fatal("script-shaped re-run must clear the defense")
	}
	if hint := s.checkIncomplete(5); hint != "" {
		t.Fatalf("no warning expected after genuine re-run, got: %s", hint)
	}
}

// Non-script run after edit, then the genuine script re-run arrives late:
// the defense only clears on the script.
func TestIssue2752_ScriptRerunAfterNoiseClearsDefense(t *testing.T) {
	s := newReproducerLifecycleState()
	s.observeToolCalls(1, []string{"run_command"}, []string{`{"command":"node bug.js"}`})
	s.observeToolCalls(2, []string{"edit_file"}, []string{`{"file_path":"a.js"}`})
	s.observeToolCalls(3, []string{"run_command"}, []string{`{"command":"cat a.js"}`})
	if s.reranAfterEdit {
		t.Fatal("cat must not count as re-run")
	}
	s.observeToolCalls(4, []string{"run_command"}, []string{`{"command":"node bug.js"}`})
	if !s.reranAfterEdit {
		t.Fatal("node bug.js re-run must clear the defense")
	}
}

// Grace period semantics must be unchanged by the constant rename:
// edit at iter 2 -> no warning at iter 3 (gap 1), warning at iter 4+ (gap 2).
func TestIssue2752_GracePeriodUnchangedAfterConstantRename(t *testing.T) {
	s := newReproducerLifecycleState()
	s.observeToolCalls(1, []string{"run_command"}, []string{`{"command":"python3 repro.py"}`})
	s.observeToolCalls(2, []string{"edit_file"}, []string{`{"file_path":"m.go"}`})

	if hint := s.checkIncomplete(3); hint != "" {
		t.Fatalf("gap 1 must stay inside grace period, got: %s", hint)
	}
	if hint := s.checkIncomplete(4); hint == "" {
		t.Fatal("gap 2 must exceed grace period and warn")
	}
}

// The reproducer-establishing call itself must not double as the re-run
// when it happens in the same iteration batch as the edit (order within
// the batch is not a re-run sequence).
func TestIssue2752_EstablishAndEditSameIteration(t *testing.T) {
	s := newReproducerLifecycleState()
	// Batch: reproducer run + edit in the same iteration.
	s.observeToolCalls(1,
		[]string{"run_command", "edit_file"},
		[]string{`{"command":"python3 repro.py"}`, `{"file_path":"m.go"}`})
	if !s.hasReproducer || !s.editedAfterReproducer {
		t.Fatal("batch must establish reproducer and edit")
	}
	if s.reranAfterEdit {
		t.Fatal("the establishing run must not count as the post-edit re-run")
	}
	// Non-script noise afterwards must not clear it either.
	s.observeToolCalls(2, []string{"run_command"}, []string{`{"command":"git status"}`})
	if s.reranAfterEdit {
		t.Fatal("git status must not clear defense")
	}
}
