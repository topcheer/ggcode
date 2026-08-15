//go:build goolm

package agent

import (
	"strings"
	"testing"
)

func TestAttemptBriefReset(t *testing.T) {
	s := newAttemptBriefState()
	s.recordOutcome("edit_file", "foo.go", false, 1, "not unique")
	s.recordOutcome("grep", "pattern", false, 2, "no match")
	s.reset()
	if len(s.entries) != 0 {
		t.Fatalf("expected empty entries after reset, got %d", len(s.entries))
	}
	if s.firedCount != 0 {
		t.Fatalf("expected firedCount=0, got %d", s.firedCount)
	}
}

func TestAttemptBriefNoFireBelowThreshold(t *testing.T) {
	s := newAttemptBriefState()
	s.recordOutcome("edit_file", "a.go", false, 1, "not unique")
	s.recordOutcome("grep", "pat", false, 2, "no match")
	// only 2 failures, need 3
	if msg := s.maybeBrief(3); msg != "" {
		t.Fatalf("expected no brief below threshold, got: %s", msg)
	}
}

func TestAttemptBriefNoFireSingleTool(t *testing.T) {
	s := newAttemptBriefState()
	s.recordOutcome("edit_file", "a.go", false, 1, "not unique")
	s.recordOutcome("edit_file", "b.go", false, 2, "not unique")
	s.recordOutcome("edit_file", "c.go", false, 3, "not unique")
	// 3 failures but only 1 distinct tool
	if msg := s.maybeBrief(4); msg != "" {
		t.Fatalf("expected no brief for single tool, got: %s", msg)
	}
}

func TestAttemptBriefFiresOnMultipleFailures(t *testing.T) {
	s := newAttemptBriefState()
	s.recordOutcome("edit_file", "a.go", false, 1, "old_text is not unique")
	s.recordOutcome("grep", "TODO", false, 2, "no match found")
	s.recordOutcome("run_command", "go build", false, 3, "exit code 1")
	msg := s.maybeBrief(4)
	if msg == "" {
		t.Fatal("expected brief for 3 failures across 3 tools")
	}
	if !strings.Contains(msg, "3 failed tool call(s)") {
		t.Errorf("expected failure count in brief, got: %s", msg)
	}
	if !strings.Contains(msg, "edit_file") {
		t.Errorf("expected edit_file in brief, got: %s", msg)
	}
	if !strings.Contains(msg, "match_failed") {
		t.Errorf("expected error label match_failed, got: %s", msg)
	}
	if !strings.Contains(msg, "different approach") {
		t.Errorf("expected guidance to try different approach, got: %s", msg)
	}
}

func TestAttemptBriefMaxFireCount(t *testing.T) {
	s := newAttemptBriefState()
	for i := 0; i < 10; i++ {
		s.recordOutcome("edit_file", "a.go", false, i, "not unique")
		s.recordOutcome("grep", "pat", false, i, "no match")
	}
	first := s.maybeBrief(5)
	if first == "" {
		t.Fatal("expected first brief")
	}
	second := s.maybeBrief(10) // after cooldown
	if second == "" {
		t.Fatal("expected second brief")
	}
	third := s.maybeBrief(20) // exceeds maxAttemptBriefFire=2
	if third != "" {
		t.Fatalf("expected no third brief, got: %s", third)
	}
}

func TestAttemptBriefCooldown(t *testing.T) {
	s := newAttemptBriefState()
	s.recordOutcome("edit_file", "a.go", false, 1, "not unique")
	s.recordOutcome("grep", "pat", false, 2, "no match")
	s.recordOutcome("run_command", "go build", false, 3, "exit code 1")
	first := s.maybeBrief(4)
	if first == "" {
		t.Fatal("expected first brief")
	}
	// Within cooldown window - should not fire
	second := s.maybeBrief(5)
	if second != "" {
		t.Fatalf("expected no brief during cooldown, got: %s", second)
	}
}

func TestAttemptBriefExcludesSuccesses(t *testing.T) {
	s := newAttemptBriefState()
	s.recordOutcome("edit_file", "a.go", false, 1, "not unique")
	s.recordOutcome("grep", "pat", false, 2, "no match")
	s.recordOutcome("read_file", "c.go", true, 3, "")
	// Only 2 failures despite 3 calls
	if msg := s.maybeBrief(4); msg != "" {
		t.Fatalf("expected no brief with only 2 failures, got: %s", msg)
	}
}

func TestClassifyError(t *testing.T) {
	tests := []struct {
		err    string
		expect string
	}{
		{"old_text is not unique", "match_failed"},
		{"exit code 1", "exit_1"},
		{"command timed out", "timeout"},
		{"permission denied", "permission"},
		{"syntax error near line 5", "syntax"},
		{"file already exists", "conflict"},
		{"panic: nil pointer dereference", "crash"},
		{"some unknown error", "error"},
	}
	for _, tc := range tests {
		got := classifyError(tc.err)
		if got != tc.expect {
			t.Errorf("classifyError(%q)=%q, want %q", tc.err, got, tc.expect)
		}
	}
}

func TestExtractToolTarget(t *testing.T) {
	tests := []struct {
		args  string
		match string
	}{
		{`{"file_path": "/tmp/foo.go"}`, "/tmp/foo.go"},
		{`{"command": "go build ./..."}`, "go build ./..."},
		{`{"pattern": "TODO"}`, "TODO"},
		{`{"path": "/src/main.go"}`, "/src/main.go"},
		{`{}`, ""},
		{``, ""},
	}
	for _, tc := range tests {
		got := extractToolTarget("", tc.args)
		if !strings.Contains(got, tc.match) && tc.match != "" {
			t.Errorf("extractToolTarget(%q)=%q, expected to contain %q", tc.args, got, tc.match)
		}
		if tc.match == "" && got != "" {
			t.Errorf("extractToolTarget(%q)=%q, expected empty", tc.args, got)
		}
	}
}

func TestTruncateBrief(t *testing.T) {
	short := "hello"
	if got := truncateBrief(short, 10); got != short {
		t.Errorf("truncateBrief short: got %q", got)
	}
	long := strings.Repeat("x", 100)
	got := truncateBrief(long, 10)
	if len(got) != 10 {
		t.Errorf("expected length 10, got %d", len(got))
	}
	if !strings.HasSuffix(got, "...") {
		t.Errorf("expected ... suffix, got %q", got)
	}
}

func TestDedupBrief(t *testing.T) {
	in := []string{"a", "b", "a", "c", "b"}
	out := dedupBrief(in)
	if len(out) != 3 {
		t.Fatalf("expected 3 unique, got %d: %v", len(out), out)
	}
}

// #342: fail→fix→re-run→pass must clear the failure; an unbroken success
// streak must never fire a brief; each fire reports only NEW failures.
func TestAttemptBriefSuccessClearsStaleFailures(t *testing.T) {
	s := newAttemptBriefState()
	// Three one-off failures, each immediately corrected by a successful re-run.
	for i := 0; i < 3; i++ {
		s.recordOutcome("read_file", "/tmp/a.go", false, i*2, "not found")
		s.recordOutcome("read_file", "/tmp/a.go", true, i*2+1, "")
	}
	// Long unbroken success streak.
	for i := 8; i < 20; i++ {
		s.recordOutcome("edit_file", "/tmp/b.go", true, i, "")
	}
	if msg := s.maybeBrief(20); msg != "" {
		t.Fatalf("expected no brief during success streak, got: %s", msg)
	}
}

// #342: the same failure batch must not be reused for a second fire.
func TestAttemptBriefNoBatchReuse(t *testing.T) {
	s := newAttemptBriefState()
	s.recordOutcome("read_file", "/tmp/a.go", false, 1, "not found")
	s.recordOutcome("grep", "pattern-x", false, 2, "no match")
	s.recordOutcome("edit_file", "/tmp/b.go", false, 3, "not unique")
	first := s.maybeBrief(7)
	if first == "" {
		t.Fatal("expected first brief")
	}
	// No new failures since; second fire must not repeat the same batch.
	if msg := s.maybeBrief(12); msg != "" {
		t.Fatalf("second fire reused stale batch: %s", msg)
	}
}
