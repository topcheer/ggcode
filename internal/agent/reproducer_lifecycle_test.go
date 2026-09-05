package agent

import (
	"testing"

	"github.com/topcheer/ggcode/internal/provider"
)

func TestReproducerLifecycleReset(t *testing.T) {
	s := newReproducerLifecycleState()
	s.hasReproducer = true
	s.editedAfterReproducer = true
	s.warned = true
	s.reset()
	if s.hasReproducer || s.editedAfterReproducer || s.warned {
		t.Fatal("reset did not clear state")
	}
}

func TestReproducerLifecycleFullCycle(t *testing.T) {
	s := newReproducerLifecycleState()

	// Iter 1: agent runs reproducer script
	s.observeToolCalls(1, []string{"run_command"}, []string{`{"command":"python3 reproduce_bug.py"}`})
	if !s.hasReproducer {
		t.Fatal("expected hasReproducer=true after running script")
	}

	// Iter 2: agent edits source
	s.observeToolCalls(2, []string{"edit_file"}, []string{`{"file_path":"src/main.go"}`})
	if !s.editedAfterReproducer {
		t.Fatal("expected editedAfterReproducer=true after edit")
	}

	// Iter 3: agent re-runs reproducer
	s.observeToolCalls(3, []string{"run_command"}, []string{`{"command":"python3 reproduce_bug.py"}`})
	if !s.reranAfterEdit {
		t.Fatal("expected reranAfterEdit=true after re-run")
	}

	// Should NOT warn -- lifecycle completed
	hint := s.checkIncomplete(4)
	if hint != "" {
		t.Fatalf("expected no warning on complete lifecycle, got: %s", hint)
	}
}

func TestReproducerLifecycleMissingRerun(t *testing.T) {
	s := newReproducerLifecycleState()

	// Iter 1: reproducer established
	s.observeToolCalls(1, []string{"run_command"}, []string{`{"command":"python3 reproduce_bug.py"}`})

	// Iter 2: edit after reproducer
	s.observeToolCalls(2, []string{"edit_file"}, []string{`{"file_path":"src/main.go"}`})

	// Iter 5: never re-ran -- should warn (gap >= 2 iterations after edit)
	hint := s.checkIncomplete(5)
	if hint == "" {
		t.Fatal("expected warning when reproducer not re-run after edit")
	}
}

func TestReproducerLifecycleNoReproducerEstablished(t *testing.T) {
	s := newReproducerLifecycleState()

	// Only edits, no reproducer
	s.observeToolCalls(1, []string{"edit_file"}, []string{`{"file_path":"src/main.go"}`})
	hint := s.checkIncomplete(3)
	if hint != "" {
		t.Fatalf("expected no warning without reproducer, got: %s", hint)
	}
}

func TestReproducerLifecycleWarnsOnlyOnce(t *testing.T) {
	s := newReproducerLifecycleState()
	s.observeToolCalls(1, []string{"run_command"}, []string{`{"command":"python3 reproduce.py"}`})
	s.observeToolCalls(2, []string{"edit_file"}, []string{`{"file_path":"main.go"}`})

	hint1 := s.checkIncomplete(5)
	if hint1 == "" {
		t.Fatal("expected first warning")
	}
	hint2 := s.checkIncomplete(6)
	if hint2 != "" {
		t.Fatal("expected no second warning (warned=true)")
	}
}

func TestReproducerLifecycleTextIntent(t *testing.T) {
	s := newReproducerLifecycleState()
	// Agent text mentions reproducer + has a run tool call
	s.observeText(1, "Let me write a script to reproduce the error", true)
	if !s.hasReproducer {
		t.Fatal("expected hasReproducer=true from text intent")
	}
}

func TestReproducerLifecycleNoFalseTriggerOnReadTools(t *testing.T) {
	s := newReproducerLifecycleState()
	// read_file should not be treated as reproducer or edit
	s.observeToolCalls(1, []string{"read_file"}, []string{`{"path":"src/main.go"}`})
	if s.hasReproducer {
		t.Fatal("read_file should not establish reproducer")
	}
	if s.editedAfterReproducer {
		t.Fatal("read_file should not count as edit")
	}
}

func TestReproducerLifecycleNodeReproducer(t *testing.T) {
	s := newReproducerLifecycleState()
	s.observeToolCalls(1, []string{"run_command"}, []string{`{"command":"node test_bug.js"}`})
	if !s.hasReproducer {
		t.Fatal("node script should establish reproducer")
	}
}

func TestReproducerLifecycleGoRunReproducer(t *testing.T) {
	s := newReproducerLifecycleState()
	s.observeToolCalls(1, []string{"run_command"}, []string{`{"command":"go run repro.go"}`})
	if !s.hasReproducer {
		t.Fatal("go run script should establish reproducer")
	}
}

func TestReproducerLifecycleGracePeriod(t *testing.T) {
	s := newReproducerLifecycleState()
	s.observeToolCalls(1, []string{"run_command"}, []string{`{"command":"python3 repro.py"}`})
	s.observeToolCalls(2, []string{"edit_file"}, []string{`{"file_path":"main.go"}`})
	// Only 1 iteration gap -- should NOT warn yet (grace period)
	hint := s.checkIncomplete(3)
	if hint != "" {
		t.Fatalf("expected no warning during grace period, got: %s", hint)
	}
}

func TestExtractToolNamesAndInputs(t *testing.T) {
	calls := []provider.ToolCallDelta{
		{Name: "run_command", Arguments: []byte(`{"command":"echo hi"}`)},
		{Name: "edit_file", Arguments: []byte(`{"file_path":"x.go"}`)},
	}
	names, inputs := extractToolNamesAndInputs(calls)
	if len(names) != 2 || names[0] != "run_command" || names[1] != "edit_file" {
		t.Fatalf("unexpected names: %v", names)
	}
	if len(inputs) != 2 || inputs[0] != `{"command":"echo hi"}` {
		t.Fatalf("unexpected inputs: %v", inputs)
	}
}

// Regression for #1488: observeText's hasRunTool used to receive "any tool
// call present", so a read-only iteration that merely said "reproduce" forged
// the REPRO state. The gate must stay false without a command-executing tool.
func TestObserveTextRequiresRunTool(t *testing.T) {
	s := newReproducerLifecycleState()
	s.observeText(1, "let me reproduce this bug", false)
	if s.hasReproducer {
		t.Fatal("text path must not establish REPRO without a run tool")
	}
	s.observeText(2, "let me reproduce this bug", true)
	if !s.hasReproducer {
		t.Fatal("text path with run tool must establish REPRO")
	}
}
