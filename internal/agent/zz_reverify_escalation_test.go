package agent

import (
	"strings"
	"testing"
)

func TestReverifyEscalationAgedMutationFires(t *testing.T) {
	s := newReverifyState()

	s.recordToolResult("grep", `{"pattern":"foo"}`, "No matches found for pattern foo", false, 1)

	s.recordToolResult("edit_file", `{"path":"a.go"}`, "ok", false, 2)

	msg := s.checkStaleTrap("The fix is complete, everything works now.", 8)
	if msg == "" {
		t.Fatal("expected escalation for aged negative + mutation + success claim, got none")
	}
	for _, want := range []string{"[stale-observation-reverify]", "grep", "Re-run"} {
		if !strings.Contains(msg, want) {
			t.Fatalf("message missing %q: %s", want, msg)
		}
	}
	if s.warnings != 1 {
		t.Fatalf("warnings = %d, want 1", s.warnings)
	}

	if again := s.checkStaleTrap("all tests pass", 9); again != "" {
		t.Fatalf("consumed observation re-fired: %s", again)
	}
}

func TestReverifyEscalationFreshObservationNoFire(t *testing.T) {
	s := newReverifyState()
	s.recordToolResult("grep", `{"pattern":"foo"}`, "no matches", false, 5)
	if msg := s.checkStaleTrap("done, everything works", 6); msg != "" {
		t.Fatalf("fresh observation (age 1) must not fire, got: %s", msg)
	}
}

func TestReverifyEscalationNoMutationNoFire(t *testing.T) {
	s := newReverifyState()
	s.recordToolResult("grep", `{"pattern":"foo"}`, "no matches", false, 1)

	s.recordToolResult("read_file", `{"path":"x.go"}`, "contents...", false, 3)
	if msg := s.checkStaleTrap("the task is finished", 9); msg != "" {
		t.Fatalf("aged but un-mutated observation must not fire, got: %s", msg)
	}
}

func TestReverifyEscalationErrorAcknowledgedNoFire(t *testing.T) {
	s := newReverifyState()
	s.recordToolResult("grep", `{"pattern":"foo"}`, "no matches", false, 1)
	s.recordToolResult("edit_file", `{"path":"a.go"}`, "ok", false, 2)
	msg := s.checkStaleTrap("The earlier search returned nothing, but the build still failed; investigating the error.", 8)
	if msg != "" {
		t.Fatalf("error-acknowledging text must not fire, got: %s", msg)
	}
}

func TestReverifyEscalationSupersededByNewerEvidence(t *testing.T) {
	s := newReverifyState()
	s.recordToolResult("grep", `{"pattern":"foo"}`, "no matches", false, 1)
	s.recordToolResult("edit_file", `{"path":"a.go"}`, "ok", false, 2)

	s.recordToolResult("grep", `{"pattern":"foo"}`, "a.go:3: foo found here", false, 4)
	if msg := s.checkStaleTrap("found the reference, everything is fixed", 8); msg != "" {
		t.Fatalf("superseded observation must not fire, got: %s", msg)
	}
}

func TestReverifyEscalationDifferentQueryDoesNotSupersede(t *testing.T) {
	s := newReverifyState()

	s.recordToolResult("grep", `{"pattern":"TODO"}`, "no matches found", false, 1)

	s.recordToolResult("edit_file", `{"path":"a.go"}`, "ok", false, 3)

	s.recordToolResult("grep", `{"pattern":"func main"}`, "a.go:10: func main() {", false, 5)

	msg := s.checkStaleTrap("All TODOs are cleaned up, everything is done now.", 8)
	if msg == "" {
		t.Fatal("different-query success must not supersede the TODO observation; expected escalation, got none")
	}
	if !strings.Contains(msg, "TODO") {
		t.Fatalf("escalation should reference the surviving TODO observation: %s", msg)
	}

	s2 := newReverifyState()
	s2.recordToolResult("tool_x", `{"q":"first"}`, "empty", false, 1)
	s2.recordToolResult("edit_file", `{"path":"a.go"}`, "ok", false, 2)
	s2.recordToolResult("tool_x", "", "real content now", false, 4)
	if m := s2.checkStaleTrap("done, everything works", 8); m != "" {
		t.Fatalf("empty-args success should retain old broad-supersede fallback, got: %s", m)
	}
}

func TestReverifyEscalationNonPositiveTextNoFire(t *testing.T) {
	s := newReverifyState()
	s.recordToolResult("search_files", `{"pattern":"foo"}`, "No results", false, 1)
	s.recordToolResult("write_file", `{"path":"b.go"}`, "ok", false, 2)
	if msg := s.checkStaleTrap("Next I will inspect the parser.", 8); msg != "" {
		t.Fatalf("non-claim text must not fire, got: %s", msg)
	}
}

func TestReverifyEscalationWarningCap(t *testing.T) {
	s := newReverifyState()

	s.recordToolResult("grep", `{"pattern":"a"}`, "no matches", false, 1)
	s.recordToolResult("glob", `{"pattern":"b"}`, "no files found", false, 2)
	s.recordToolResult("search_files", `{"pattern":"c"}`, "no results", false, 3)
	s.recordToolResult("edit_file", `{"path":"a.go"}`, "ok", false, 4)
	if m1 := s.checkStaleTrap("done, everything works", 10); m1 == "" {
		t.Fatal("first escalation expected")
	}
	if m2 := s.checkStaleTrap("all set, the issue is fixed", 11); m2 == "" {
		t.Fatal("second escalation expected")
	}
	if m3 := s.checkStaleTrap("complete, problem resolved", 12); m3 != "" {
		t.Fatalf("warning cap (2) exceeded: %s", m3)
	}
}

func TestReverifyEscalationToolErrorNotObserved(t *testing.T) {
	s := newReverifyState()

	s.recordToolResult("grep", `{"pattern":"foo"}`, "command failed: exit 1", true, 1)
	s.recordToolResult("edit_file", `{"path":"a.go"}`, "ok", false, 2)
	if msg := s.checkStaleTrap("everything is fixed", 8); msg != "" {
		t.Fatalf("tool error must not be recorded as negative observation, got: %s", msg)
	}
}

func TestReverifySummarizeArgsAndFirstLine(t *testing.T) {
	if got := reverifySummarizeArgs(""); got != "" {
		t.Fatalf("empty args summary = %q, want empty", got)
	}
	long := strings.Repeat("x", 120)
	if got := reverifySummarizeArgs(long); len(got) != 83 || !strings.HasSuffix(got, "...") {
		t.Fatalf("long args summary not truncated: %q", got)
	}
	if got := reverifyFirstLine("\n\n  line one  \nline two"); got != "line one" {
		t.Fatalf("firstLine = %q, want %q", got, "line one")
	}
}
