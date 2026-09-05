package agent

import (
	"strings"
	"testing"
)

func TestClaimVerify_ExitCodeFailure(t *testing.T) {
	s := newClaimVerifyState()
	g := s.check("run_command", "building...\nexit code: 1", false, "")
	if g == "" {
		t.Fatal("expected guidance for exit code 1")
	}
	if !strings.Contains(g, "exited with code 1") {
		t.Fatalf("unexpected guidance: %s", g)
	}
}

func TestClaimVerify_PanicInOutput(t *testing.T) {
	s := newClaimVerifyState()
	g := s.check("run_command", "running tests...\ngoroutine 1 [running]:\npanic: runtime error: nil pointer", false, "")
	if g == "" {
		t.Fatal("expected guidance for panic")
	}
	if !strings.Contains(g, "crash") {
		t.Fatalf("unexpected guidance: %s", g)
	}
}

func TestClaimVerify_NoResults(t *testing.T) {
	// Command output containing a zero-match status line (true positive).
	s := newClaimVerifyState()
	g := s.check("run_command", "rg foo ./src\n0 matches", false, "")
	if g == "" {
		t.Fatal("expected guidance for 0 matches")
	}
	// grep's own zero-result meta-status line (true positive, issue #739 path).
	g = s.check("grep", "No matches found.", false, "")
	if g == "" {
		t.Fatal("expected guidance for grep zero-result meta-status")
	}
}

func TestClaimVerify_NotFound(t *testing.T) {
	// Path-not-found status in command output (true positive). read_file with
	// such text is now a content-bearing false positive — see zz_issue739_test.go.
	s := newClaimVerifyState()
	g := s.check("run_command", "cat foo.txt\nError: no such file or directory", false, "")
	if g == "" {
		t.Fatal("expected guidance for file not found")
	}
}

func TestClaimVerify_BuildFailed(t *testing.T) {
	s := newClaimVerifyState()
	g := s.check("run_command", "go build ./...\nbuild failed: undefined symbol", false, "")
	if g == "" {
		t.Fatal("expected guidance for build failure")
	}
}

func TestClaimVerify_TestFail(t *testing.T) {
	s := newClaimVerifyState()
	g := s.check("run_command", "=== RUN   TestFoo\n--- FAIL: TestFoo (0.00s)\nexit code: 1", false, "")
	if g == "" {
		t.Fatal("expected guidance for test failure")
	}
}

func TestClaimVerify_NoIssueOnSuccess(t *testing.T) {
	s := newClaimVerifyState()
	g := s.check("run_command", "PASS\nok\texample.com/pkg\t0.123s", false, "")
	if g != "" {
		t.Fatalf("expected no guidance for successful output, got: %s", g)
	}
}

func TestClaimVerify_SkipsErrorResults(t *testing.T) {
	s := newClaimVerifyState()
	g := s.check("run_command", "exit code: 1", true, "")
	if g != "" {
		t.Fatalf("expected no guidance for IsError=true result, got: %s", g)
	}
}

func TestClaimVerify_SkipsUnknownTools(t *testing.T) {
	s := newClaimVerifyState()
	g := s.check("edit_file", "exit code: 1", false, "")
	if g != "" {
		t.Fatalf("expected no guidance for non-tracked tool, got: %s", g)
	}
}

func TestClaimVerify_InjectionCap(t *testing.T) {
	s := newClaimVerifyState()
	for i := 0; i < claimVerifyMaxInjections; i++ {
		g := s.check("run_command", "exit code: 1", false, "")
		if g == "" {
			t.Fatalf("expected guidance on injection %d", i)
		}
	}
	// Should be capped now
	g := s.check("run_command", "exit code: 1", false, "")
	if g != "" {
		t.Fatalf("expected no guidance after cap reached, got: %s", g)
	}
}

func TestClaimVerify_Reset(t *testing.T) {
	s := newClaimVerifyState()
	// Use up all injections
	for i := 0; i < claimVerifyMaxInjections; i++ {
		s.check("run_command", "exit code: 1", false, "")
	}
	// Reset should clear
	s.reset()
	g := s.check("run_command", "exit code: 1", false, "")
	if g == "" {
		t.Fatal("expected guidance after reset")
	}
}

func TestClaimVerify_EmptyContent(t *testing.T) {
	s := newClaimVerifyState()
	g := s.check("run_command", "", false, "")
	if g != "" {
		t.Fatalf("expected no guidance for empty content, got: %s", g)
	}
}

func TestClaimVerify_LargeOutputScanned(t *testing.T) {
	s := newClaimVerifyState()
	// 5KB of padding + failure signal at the end (should still be caught since we scan 4KB)
	padding := strings.Repeat("a", 5000)
	g := s.check("run_command", padding, false, "")
	if g != "" {
		t.Fatal("expected no guidance for padding-only output within scan window")
	}
}

func TestClaimVerify_TruncateFunction(t *testing.T) {
	// Verify trimNonPrint doesn't corrupt normal strings
	input := "normal text"
	if got := trimNonPrint(input); got != input {
		t.Fatalf("trimNonPrint corrupted input: got %q", got)
	}
	// Verify it strips control chars
	control := "text\x00\x01"
	if got := trimNonPrint(control); got != "text" {
		t.Fatalf("trimNonPrint failed to strip control chars: got %q", got)
	}
}

// TestIssue1207_ContentRetrievalCommandBoundary verifies that command tools
// running content-retrieval pipelines (grep/cat/rg/head/...) do NOT fire
// status-pattern advisories when their stdout is file content: a successful
// `grep -n 'fail:' foo_test.go` prints source lines containing "fail:" and
// that is payload, not a failure signal. Compound commands with any
// status-bearing stage still fire.
func TestIssue1207_ContentRetrievalCommandBoundary(t *testing.T) {
	s := newClaimVerifyState()

	// grep/cat-style successes whose output IS file content - no guidance.
	contentCmds := []struct{ cmd, out string }{
		{"grep -n 'fail:' foo_test.go", "foo_test.go:62:--- FAIL: TestBar (0.00s)"},
		{"grep -rn 'panic:' internal/", "handler.go:40:\tif err != nil { panic: covered by test }"},
		{"cat server.log", "2026-01-01 error: no such file or directory (logged event)"},
		{"rg '0 matches' docs/", "docs/usage.md: search shows 0 matches when disabled"},
		{"cat build.log | head -20", "fatal error: recompiled (see note)\nbuild failed: archived ticket"},
	}
	for _, c := range contentCmds {
		if g := s.check("run_command", c.out, false, c.cmd); g != "" {
			t.Errorf("check(run_command, %q, cmd=%q) = %q, want \"\" (stdout is file content)", c.out, c.cmd, g)
		}
	}

	// Compound with a status-bearing stage - still scans (true positive kept).
	g := s.check("run_command", "running...\nexit code: 1", false, "grep foo bar && go test ./...")
	if g == "" {
		t.Error("expected guidance for compound command containing go test with exit code 1")
	}
	// Pure status command - still scans.
	g = s.check("run_command", "building...\nexit code: 1", false, "go build ./...")
	if g == "" {
		t.Error("expected guidance for go build with exit code 1")
	}
}

// TestIssue1207_IsContentRetrievalCommand covers the pipeline classifier.
func TestIssue1207_IsContentRetrievalCommand(t *testing.T) {
	yes := []string{
		"grep foo file",
		"cat a.txt",
		"FOO=1 /usr/bin/rg pattern .",
		"cat log | grep err | head -5",
		"sed -n '1,20p' file",
	}
	for _, cmd := range yes {
		if !isContentRetrievalCommand(cmd) {
			t.Errorf("isContentRetrievalCommand(%q) = false, want true", cmd)
		}
	}
	no := []string{
		"",
		"go test ./...",
		"grep foo && make test",
		"cat f; rm f",
		"echo hi",
		"grep foo | go run x.go",
	}
	for _, cmd := range no {
		if isContentRetrievalCommand(cmd) {
			t.Errorf("isContentRetrievalCommand(%q) = true, want false", cmd)
		}
	}
}

// Regression for #1506: content-retrieval wrappers (git grep/xargs/find)
// were absent from the exemption chain, so a successful 'git grep -n
// "fail:"' search was condemned as a test failure; and the zero-result
// meta-status prefix matched only grep's exact wording.
func TestIsContentRetrievalCommandGitGrep(t *testing.T) {
	for _, cmd := range []string{
		`git grep -n "fail:" -- '*_test.go'`,
		`git log -S "removed" --oneline`,
		`cat foo_test.go | xargs grep -n "does not exist"`,
		`grep -rn "expected" src`,
	} {
		if !isContentRetrievalCommand(cmd) {
			t.Errorf("isContentRetrievalCommand(%q) = false, want true (content-bearing)", cmd)
		}
	}
	for _, cmd := range []string{
		`go test ./...`,
		`make test && grep -n "x" f`,
	} {
		if isContentRetrievalCommand(cmd) {
			t.Errorf("isContentRetrievalCommand(%q) = true, want false (status-bearing)", cmd)
		}
	}
}

func TestClaimVerifyZeroResultPrefixVariants(t *testing.T) {
	// search_files / glob wordings must trigger the found-claim check now.
	for _, status := range []string{
		"no matches found.",
		"No matches found for pattern \".*\"",
		"No files matched pattern **/*.foo",
	} {
		c := newClaimVerifyState()
		if got := c.check("grep", status, false, ""); got == "" {
			t.Errorf("zero-result status %q must trigger the found-claim check", status)
		}
	}
	// Payload that merely contains the phrase must stay inert (#739).
	c := newClaimVerifyState()
	if got := c.check("grep", "the log line said: no matches found somewhere", false, ""); got != "" {
		t.Errorf("mid-text mention must not trigger: %q", got)
	}
}
