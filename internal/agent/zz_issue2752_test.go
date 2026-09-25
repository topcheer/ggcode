package agent

import (
	"strings"
	"testing"
)

// Regression tests for #2752: Phase 3 re-run detection must verify the
// command content, not just the tool name. A bare `run_command "git diff"`
// between edit and completion must NOT discharge the reproducer re-run
// obligation.

func TestIssue2752BareGitDiffDoesNotDischarge(t *testing.T) {
	s := newReproducerLifecycleState()
	// Iter 1: establish reproducer.
	s.observeToolCalls(1, []string{"run_command"}, []string{"python3 repro.py"})
	if !s.hasReproducer {
		t.Fatal("reproducer should be established")
	}
	// Iter 2: edit source.
	s.observeToolCalls(2, []string{"edit_file"}, []string{"src/main.go"})
	if !s.editedAfterReproducer {
		t.Fatal("edit after reproducer should be recorded")
	}
	// Iter 3: run an unrelated command (git diff).
	s.observeToolCalls(3, []string{"run_command"}, []string{"git diff HEAD~1"})
	if s.reranAfterEdit {
		t.Fatal("bare 'git diff' must NOT count as reproducer re-run (#2752)")
	}
	// checkIncomplete past grace period must fire.
	hint := s.checkIncomplete(6)
	if !strings.Contains(hint, "reproducer-lifecycle") {
		t.Fatalf("expected lifecycle warning, got: %q", hint)
	}
}

func TestIssue2752LsEchoDoNotDischarge(t *testing.T) {
	s := newReproducerLifecycleState()
	s.observeToolCalls(1, []string{"run_command"}, []string{"node crash.js"})
	s.observeToolCalls(2, []string{"write_file"}, []string{"internal/foo.go"})
	for _, cmd := range []string{"ls -la", "echo done", "cat internal/foo.go"} {
		s.observeToolCalls(3, []string{"run_command"}, []string{cmd})
		if s.reranAfterEdit {
			t.Fatalf("%q must NOT count as reproducer re-run", cmd)
		}
	}
}

func TestIssue2752ActualRerunDischarges(t *testing.T) {
	s := newReproducerLifecycleState()
	s.observeToolCalls(1, []string{"run_command"}, []string{"python3 repro.py"})
	s.observeToolCalls(2, []string{"edit_file"}, []string{"src/main.go"})
	// Re-run the same reproducer script (matches reproducerCommandRe).
	s.observeToolCalls(3, []string{"run_command"}, []string{"python3 repro.py"})
	if !s.reranAfterEdit {
		t.Fatal("re-running the reproducer script must discharge the obligation")
	}
	if hint := s.checkIncomplete(6); hint != "" {
		t.Fatalf("no warning expected after genuine re-run, got: %q", hint)
	}
}

func TestIssue2752StartCommandValidatedToo(t *testing.T) {
	s := newReproducerLifecycleState()
	s.observeToolCalls(1, []string{"run_command"}, []string{"python3 repro.py"})
	s.observeToolCalls(2, []string{"edit_file"}, []string{"src/main.go"})
	// start_command with unrelated content must not discharge.
	s.observeToolCalls(3, []string{"start_command"}, []string{"watch ls"})
	if s.reranAfterEdit {
		t.Fatal("unrelated start_command must NOT count as re-run")
	}
	// start_command re-running the script must discharge.
	s.observeToolCalls(4, []string{"start_command"}, []string{"python3 repro.py"})
	if !s.reranAfterEdit {
		t.Fatal("start_command re-running the reproducer must discharge")
	}
}

func TestIssue2752SnippetOverlapFallback(t *testing.T) {
	s := newReproducerLifecycleState()
	// Reproducer established via script shape.
	s.observeToolCalls(1, []string{"run_command"}, []string{"go run ./cmd/reprogo/main.go"})
	s.observeToolCalls(2, []string{"edit_file"}, []string{"main.go"})
	// Re-run that doesn't match reproducerCommandRe exactly (no file ext)
	// but shares the distinctive token with the snippet.
	s.observeToolCalls(3, []string{"run_command"}, []string{"go run ./cmd/reprogo"})
	if !s.reranAfterEdit {
		t.Fatal("snippet token overlap should discharge the obligation")
	}
}

func TestIssue2752GracePeriodUnchanged(t *testing.T) {
	s := newReproducerLifecycleState()
	s.observeToolCalls(1, []string{"run_command"}, []string{"python3 repro.py"})
	s.observeToolCalls(2, []string{"edit_file"}, []string{"src/main.go"})
	// iteration - editIteration == 1 < grace(2): no warning yet.
	if hint := s.checkIncomplete(3); hint != "" {
		t.Fatalf("grace period should suppress warning, got: %q", hint)
	}
	// Exactly at grace boundary: warning fires (2 >= 2).
	if hint := s.checkIncomplete(4); !strings.Contains(hint, "reproducer-lifecycle") {
		t.Fatalf("expected warning at grace boundary, got: %q", hint)
	}
}
