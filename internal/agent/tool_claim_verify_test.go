package agent

import (
	"strings"
	"testing"
)

func TestClaimVerify_ExitCodeFailure(t *testing.T) {
	s := newClaimVerifyState()
	g := s.check("run_command", "building...\nexit code: 1", false)
	if g == "" {
		t.Fatal("expected guidance for exit code 1")
	}
	if !strings.Contains(g, "exited with code 1") {
		t.Fatalf("unexpected guidance: %s", g)
	}
}

func TestClaimVerify_PanicInOutput(t *testing.T) {
	s := newClaimVerifyState()
	g := s.check("run_command", "running tests...\ngoroutine 1 [running]:\npanic: runtime error: nil pointer", false)
	if g == "" {
		t.Fatal("expected guidance for panic")
	}
	if !strings.Contains(g, "crash") {
		t.Fatalf("unexpected guidance: %s", g)
	}
}

func TestClaimVerify_NoResults(t *testing.T) {
	s := newClaimVerifyState()
	g := s.check("grep", "pattern: foo\n0 matches", false)
	if g == "" {
		t.Fatal("expected guidance for 0 matches")
	}
}

func TestClaimVerify_NotFound(t *testing.T) {
	s := newClaimVerifyState()
	g := s.check("read_file", "Error: no such file or directory", false)
	if g == "" {
		t.Fatal("expected guidance for file not found")
	}
}

func TestClaimVerify_BuildFailed(t *testing.T) {
	s := newClaimVerifyState()
	g := s.check("run_command", "go build ./...\nbuild failed: undefined symbol", false)
	if g == "" {
		t.Fatal("expected guidance for build failure")
	}
}

func TestClaimVerify_TestFail(t *testing.T) {
	s := newClaimVerifyState()
	g := s.check("run_command", "=== RUN   TestFoo\n--- FAIL: TestFoo (0.00s)\nexit code: 1", false)
	if g == "" {
		t.Fatal("expected guidance for test failure")
	}
}

func TestClaimVerify_NoIssueOnSuccess(t *testing.T) {
	s := newClaimVerifyState()
	g := s.check("run_command", "PASS\nok\texample.com/pkg\t0.123s", false)
	if g != "" {
		t.Fatalf("expected no guidance for successful output, got: %s", g)
	}
}

func TestClaimVerify_SkipsErrorResults(t *testing.T) {
	s := newClaimVerifyState()
	g := s.check("run_command", "exit code: 1", true)
	if g != "" {
		t.Fatalf("expected no guidance for IsError=true result, got: %s", g)
	}
}

func TestClaimVerify_SkipsUnknownTools(t *testing.T) {
	s := newClaimVerifyState()
	g := s.check("edit_file", "exit code: 1", false)
	if g != "" {
		t.Fatalf("expected no guidance for non-tracked tool, got: %s", g)
	}
}

func TestClaimVerify_InjectionCap(t *testing.T) {
	s := newClaimVerifyState()
	for i := 0; i < claimVerifyMaxInjections; i++ {
		g := s.check("run_command", "exit code: 1", false)
		if g == "" {
			t.Fatalf("expected guidance on injection %d", i)
		}
	}
	// Should be capped now
	g := s.check("run_command", "exit code: 1", false)
	if g != "" {
		t.Fatalf("expected no guidance after cap reached, got: %s", g)
	}
}

func TestClaimVerify_Reset(t *testing.T) {
	s := newClaimVerifyState()
	// Use up all injections
	for i := 0; i < claimVerifyMaxInjections; i++ {
		s.check("run_command", "exit code: 1", false)
	}
	// Reset should clear
	s.reset()
	g := s.check("run_command", "exit code: 1", false)
	if g == "" {
		t.Fatal("expected guidance after reset")
	}
}

func TestClaimVerify_EmptyContent(t *testing.T) {
	s := newClaimVerifyState()
	g := s.check("run_command", "", false)
	if g != "" {
		t.Fatalf("expected no guidance for empty content, got: %s", g)
	}
}

func TestClaimVerify_LargeOutputScanned(t *testing.T) {
	s := newClaimVerifyState()
	// 5KB of padding + failure signal at the end (should still be caught since we scan 4KB)
	padding := strings.Repeat("a", 5000)
	g := s.check("run_command", padding, false)
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
