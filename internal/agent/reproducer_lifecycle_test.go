package agent

import (
	"strings"
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
	s.observeText(1, "Writing a script to reproduce the bug.", true, "")
	s.observeText(1, "Writing a script to reproduce the bug.", false, "")
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
	s.observeText(1, "Writing a script to reproduce the bug.", true, "")
	s.observeText(1, "Writing a script to reproduce the bug.", false, "")
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
	s.observeText(1, "Writing a script to reproduce the bug.", true, "")
	s.observeText(1, "Reproducing the crash now.", false, "")
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
	s.observeText(1, "Let me write a script to reproduce the error", true, "")
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
	s.observeText(1, "Reproducing the crash now.", true, "")
	s.observeText(1, "Reproducing the crash now.", false, "")
	s.observeToolCalls(1, []string{"run_command"}, []string{`{"command":"node test_bug.js"}`})
	if !s.hasReproducer {
		t.Fatal("node script should establish reproducer")
	}
}

func TestReproducerLifecycleGoRunReproducer(t *testing.T) {
	s := newReproducerLifecycleState()
	s.observeText(1, "Reproducing the bug now.", true, "")
	s.observeText(1, "Reproducing the bug now.", false, "")
	s.observeToolCalls(1, []string{"run_command"}, []string{`{"command":"go run repro.go"}`})
	if !s.hasReproducer {
		t.Fatal("go run script should establish reproducer")
	}
}

func TestReproducerLifecycleGracePeriod(t *testing.T) {
	s := newReproducerLifecycleState()
	s.observeText(1, "Reproducing the bug now.", true, "")
	s.observeText(1, "Reproducing the bug now.", false, "")
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
	s.observeText(1, "let me reproduce this bug", false, "")
	if s.hasReproducer {
		t.Fatal("text path must not establish REPRO without a run tool")
	}
	s.observeText(2, "let me reproduce this bug", true, "")
	if !s.hasReproducer {
		t.Fatal("text path with run tool must establish REPRO")
	}
}

// Regression for #2802: after a command-established reproducer, running an
// UNRELATED script (e.g. `node test/unit/foo.test.js`) must NOT discharge the
// re-run obligation. The rerun must reference the recorded reproducer script.
func TestReproducerRerunUnrelatedScriptNotDischarged(t *testing.T) {
	s := newReproducerLifecycleState()

	// Iter 1: reproducer established via command (snippet recorded)
	s.observeText(1, "Writing a script to reproduce the bug.", false, "")
	s.observeToolCalls(1, []string{"run_command"}, []string{`{"command":"python3 repro_bug.py"}`})
	if !s.hasReproducer {
		t.Fatal("expected hasReproducer=true after running script")
	}

	// Iter 2: edit after reproducer
	s.observeToolCalls(2, []string{"edit_file"}, []string{`{"file_path":"src/main.go"}`})
	if !s.editedAfterReproducer {
		t.Fatal("expected editedAfterReproducer=true after edit")
	}

	// Iter 3: agent runs an unrelated test script -- must NOT discharge
	s.observeToolCalls(3, []string{"run_command"}, []string{`{"command":"node test/unit/foo.test.js"}`})
	if s.reranAfterEdit {
		t.Fatal("unrelated script must not discharge rerun obligation (#2802)")
	}

	// Should still warn past the grace period
	hint := s.checkIncomplete(6)
	if hint == "" {
		t.Fatal("expected warning: unrelated script must not suppress rerun warning (#2802)")
	}
}

// #2802 companion: the genuine rerun (same script, path variant) still
// discharges the obligation.
func TestReproducerRerunSameScriptDischarged(t *testing.T) {
	s := newReproducerLifecycleState()
	s.observeText(1, "Writing a script to reproduce the bug.", false, "")
	s.observeToolCalls(1, []string{"run_command"}, []string{`{"command":"python3 repro_bug.py"}`})
	s.observeToolCalls(2, []string{"edit_file"}, []string{`{"file_path":"src/main.go"}`})

	// Re-run with a path-variant command sharing the script token
	s.observeToolCalls(3, []string{"run_command"}, []string{`{"command":"cd /tmp && python3 repro_bug.py"}`})
	if !s.reranAfterEdit {
		t.Fatal("same-script rerun (path variant) must discharge obligation")
	}
	hint := s.checkIncomplete(6)
	if hint != "" {
		t.Fatalf("expected no warning after genuine rerun, got: %s", hint)
	}
}

// #2802 companion: text-established reproducers have no recorded command to
// compare against (snippet == ""), so the loose script-shape match is kept.
func TestReproducerRerunTextEstablishedKeepsLooseShape(t *testing.T) {
	s := newReproducerLifecycleState()
	s.observeText(1, "Let me write a script to reproduce the error", true, "")
	if !s.hasReproducer {
		t.Fatal("expected hasReproducer=true from text intent")
	}

	s.observeToolCalls(2, []string{"edit_file"}, []string{`{"file_path":"src/main.go"}`})

	// Any script-shaped run still discharges on the text path
	s.observeToolCalls(3, []string{"run_command"}, []string{`{"command":"node run_check.js"}`})
	if !s.reranAfterEdit {
		t.Fatal("text-established reproducer keeps loose shape match")
	}
	hint := s.checkIncomplete(6)
	if hint != "" {
		t.Fatalf("expected no warning on text path rerun, got: %s", hint)
	}
}

// Regression for #2805: a text-established reproducer whose same-iteration
// run is a test runner (`go test ./pkg/ -run TestX`) must record a snippet so
// an identical re-run after an edit discharges the re-run obligation and the
// "Re-run:" tail is non-empty. Previously both discharge channels were dead
// for this shape: the regex channel lacks `go test`, and observeText never
// recorded a snippet for the token-overlap channel.
func TestReproducerGoTestRerunDischarges(t *testing.T) {
	s := newReproducerLifecycleState()
	s.observeText(1, "Let me write a test to reproduce the error", true,
		`{"command":"go test ./internal/foo/ -run TestBar"}`)
	if !s.hasReproducer {
		t.Fatal("expected hasReproducer=true via text+go test")
	}
	if s.reproducerSnippet == "" {
		t.Fatal("expected snippet recorded for test-runner reproducer (#2805)")
	}

	s.observeToolCalls(2, []string{"edit_file"}, []string{`{"file_path":"src/main.go"}`})

	// Identical re-run must discharge.
	s.observeToolCalls(3, []string{"run_command"}, []string{`{"command":"go test ./internal/foo/ -run TestBar"}`})
	if !s.reranAfterEdit {
		t.Fatal("identical go test re-run must discharge reranAfterEdit (#2805)")
	}
	if hint := s.checkIncomplete(6); hint != "" {
		t.Fatalf("expected no warning after discharge, got: %s", hint)
	}
}

// #2805 warning path: without the discharge, the hint must carry a
// non-empty "Re-run:" tail (previously blank for text-established reproducers).
func TestReproducerGoTestHintHasRerunCommand(t *testing.T) {
	s := newReproducerLifecycleState()
	s.observeText(1, "let me reproduce this with a test", true,
		`{"command":"go test ./internal/foo/ -run TestBar"}`)
	s.observeToolCalls(2, []string{"edit_file"}, []string{`{"file_path":"src/main.go"}`})
	s.observeToolCalls(3, []string{"run_command"}, []string{`{"command":"git status"}`})
	hint := s.checkIncomplete(6)
	if hint == "" {
		t.Fatal("expected warning when reproducer not re-run")
	}
	if !strings.Contains(hint, "go test ./internal/foo/ -run TestBar") {
		t.Fatalf("hint must quote the recorded reproducer command, got: %s", hint)
	}
}

// #2805 x #2802 guardrail: after a text-established go-test reproducer, an
// unrelated script run must NOT discharge, and a different package's go test
// must not either (token overlap, not verb shape).
func TestReproducerGoTestUnrelatedRerunStillWarns(t *testing.T) {
	s := newReproducerLifecycleState()
	s.observeText(1, "Let me write a test to reproduce the error", true,
		`{"command":"go test ./internal/foo/ -run TestBar"}`)
	s.observeToolCalls(2, []string{"edit_file"}, []string{`{"file_path":"src/main.go"}`})

	s.observeToolCalls(3, []string{"run_command"}, []string{`{"command":"node test/unit/foo.test.js"}`})
	if s.reranAfterEdit {
		t.Fatal("unrelated script must not discharge (#2802 semantics)")
	}

	s.observeToolCalls(4, []string{"run_command"}, []string{`{"command":"go test ./internal/bar/ -run TestOther"}`})
	if s.reranAfterEdit {
		t.Fatal("different package's go test must not discharge (no token overlap)")
	}

	if hint := s.checkIncomplete(7); hint == "" {
		t.Fatal("expected warning to persist until the actual reproducer is re-run")
	}
}

// #2802 unit-level contract for reproducerRerunMatches.
func TestReproducerRerunMatchesContract(t *testing.T) {
	cases := []struct {
		name         string
		inp, snippet string
		want         bool
	}{
		{"empty input", "", "python3 repro.py", false},
		{"unrelated script vs command snippet", "node test/unit/foo.test.js", "python3 repro_bug.py", false},
		{"same script", "python3 repro_bug.py", "python3 repro_bug.py", true},
		{"same script path variant", "cd /tmp && python3 repro_bug.py", "python3 repro_bug.py", true},
		{"non-script command vs snippet", "git diff", "python3 repro_bug.py", false},
		{"loose shape on empty snippet", "bash anything.sh", "", true},
		{"non-script on empty snippet", "git diff", "", false},
	}
	for _, tc := range cases {
		if got := reproducerRerunMatches(tc.inp, tc.snippet); got != tc.want {
			t.Errorf("%s: reproducerRerunMatches(%q, %q) = %v, want %v", tc.name, tc.inp, tc.snippet, got, tc.want)
		}
	}
}
