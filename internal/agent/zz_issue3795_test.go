package agent

import "testing"

func rlState3795(s *reproducerLifecycleState) (established bool, snippet string, stamp int) {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.hasReproducer, s.reproducerSnippet, s.intentSeenIter
}

func TestIssue3795_FirstLineSkipsComments(t *testing.T) {
	cmd := "# sync mobile versions\ncd mobile/flutter && bash scripts/version_sync.sh 1.2.3"
	if got := firstLine(cmd); got != "cd mobile/flutter && bash scripts/version_sync.sh 1.2.3" {
		t.Fatalf("comment/blank leading lines must be skipped, got %q", got)
	}
	if got := firstLine("\n\n  \nplain one-liner"); got != "plain one-liner" {
		t.Fatalf("blank leading lines must be skipped, got %q", got)
	}
	if got := firstLine("# only a comment\n"); got != "" {
		t.Fatalf("comment-only input should yield empty, got %q", got)
	}
}

func TestIssue3795_TestRunnerDischargesEmptySnippet(t *testing.T) {
	if !reproducerRerunMatches(`{"command":"go test ./pkg/ -run TestRepro"}`, "") {
		t.Fatal("go test re-run must discharge a text-established (empty-snippet) reproducer")
	}
	if !reproducerRerunMatches(`{"command":"bash repro.sh"}`, "") {
		t.Fatal("script re-run must keep discharging empty-snippet reproducers")
	}
}

func TestIssue3795_CommandPathRequiresIntent(t *testing.T) {

	s := newReproducerLifecycleState()
	s.observeText(1, "Bumping versions for the release now.", true, "")
	s.observeToolCalls(1, []string{"run_command"}, []string{`{"command":"bash scripts/version_sync.sh 1.2.3"}`})
	if est, _, _ := rlState3795(s); est {
		t.Fatal("one-shot script without reproduce intent must NOT establish a reproducer")
	}

	s2 := newReproducerLifecycleState()
	s2.observeText(2, "Writing a minimal repro script to demonstrate the crash.", false, "")
	s2.observeToolCalls(2, []string{"run_command"}, []string{"# run the repro\nbash repro.sh"})
	est, snippet, _ := rlState3795(s2)
	if !est {
		t.Fatal("intent + script run in the same iteration must establish a reproducer")
	}
	if snippet != "bash repro.sh" {
		t.Fatalf("snippet must be the command line (comments skipped), got %q", snippet)
	}
}

func TestIssue3795_IntentStampWithoutRunTool(t *testing.T) {
	s := newReproducerLifecycleState()
	s.observeText(3, "Let me build a reproducer for this.", false, "")
	if _, _, stamp := rlState3795(s); stamp != 3 {
		t.Fatalf("intent must be stamped even without a run tool, got %d", stamp)
	}
}
