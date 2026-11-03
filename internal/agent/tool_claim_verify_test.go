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

	s := newClaimVerifyState()
	g := s.check("run_command", "rg foo ./src\n0 matches", false, "")
	if g == "" {
		t.Fatal("expected guidance for 0 matches")
	}

	g = s.check("grep", "No matches found.", false, "")
	if g == "" {
		t.Fatal("expected guidance for grep zero-result meta-status")
	}
}

func TestClaimVerify_NotFound(t *testing.T) {

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

	g := s.check("run_command", "exit code: 1", false, "")
	if g != "" {
		t.Fatalf("expected no guidance after cap reached, got: %s", g)
	}
}

func TestClaimVerify_Reset(t *testing.T) {
	s := newClaimVerifyState()

	for i := 0; i < claimVerifyMaxInjections; i++ {
		s.check("run_command", "exit code: 1", false, "")
	}

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

	padding := strings.Repeat("a", 5000)
	g := s.check("run_command", padding, false, "")
	if g != "" {
		t.Fatal("expected no guidance for padding-only output within scan window")
	}
}

func TestClaimVerify_TruncateFunction(t *testing.T) {

	input := "normal text"
	if got := trimNonPrint(input); got != input {
		t.Fatalf("trimNonPrint corrupted input: got %q", got)
	}

	control := "text\x00\x01"
	if got := trimNonPrint(control); got != "text" {
		t.Fatalf("trimNonPrint failed to strip control chars: got %q", got)
	}
}

func TestIssue1207_ContentRetrievalCommandBoundary(t *testing.T) {
	s := newClaimVerifyState()

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

	g := s.check("run_command", "running...\nexit code: 1", false, "grep foo bar && go test ./...")
	if g == "" {
		t.Error("expected guidance for compound command containing go test with exit code 1")
	}

	g = s.check("run_command", "building...\nexit code: 1", false, "go build ./...")
	if g == "" {
		t.Error("expected guidance for go build with exit code 1")
	}
}

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
		"grep foo | go run x.go",
	}
	for _, cmd := range no {
		if isContentRetrievalCommand(cmd) {
			t.Errorf("isContentRetrievalCommand(%q) = true, want false", cmd)
		}
	}
	yesNeutral := []string{

		"echo hi",
		`grep -rn "build failed" internal/ || echo "no matches"`,
		"grep -c FAIL x.log || true",
	}
	for _, cmd := range yesNeutral {
		if !isContentRetrievalCommand(cmd) {
			t.Errorf("isContentRetrievalCommand(%q) = false, want true (neutral fail-silencer segments)", cmd)
		}
	}
}

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

	c := newClaimVerifyState()
	if got := c.check("grep", "the log line said: no matches found somewhere", false, ""); got != "" {
		t.Errorf("mid-text mention must not trigger: %q", got)
	}
}

func TestSA30MixedCompoundCanonicalOnly(t *testing.T) {
	chain := `go build -tags goolm ./... && go test -tags goolm -p 1 ./internal/agent && grep -c "build failed" internal/agent/tool_claim_verify.go || echo "no matches"`
	payload := "ok  \tgithub.com/topcheer/ggcode/internal/agent  23.794s\n" +
		"tool_claim_verify.go:174:{\"build failed\", \"Build failed. Do not claim the build passed.\"}\n" +
		"docs/releases/v1.3.132.md:9:avoiding redundant run-go-build reminders."

	s := newClaimVerifyState()
	if g := s.check("run_command", payload, false, chain); g != "" {
		t.Fatalf("sa-30 regression: successful mixed chain with payload free text must not fire, got %q", g)
	}

	s2 := newClaimVerifyState()
	if g := s2.check("run_command", payload, false, `grep -n "build failed" internal/agent/tool_claim_verify.go || echo "no matches"`); g != "" {
		t.Fatalf("data-only chain must not fire on payload text, got %q", g)
	}

	s3 := newClaimVerifyState()
	if g := s3.check("run_command", "build failed", false, `echo "build failed"`); g != "" {
		t.Fatalf("pure echo output is agent-authored data, must not fire, got %q", g)
	}

	for _, out := range []string{
		"ok  \texample.com/pkg  0.123s\nexit status 1",
		"FAIL\texample.com/pkg [build failed]",
	} {
		sn := newClaimVerifyState()
		if g := sn.check("run_command", out, false, chain); g == "" {
			t.Errorf("canonical status %q in mixed chain must still fire", out)
		}
	}

	s4 := newClaimVerifyState()
	if g := s4.check("run_command", "--- FAIL: TestX (0.00s)", false, "go test ./... || true"); g == "" {
		t.Fatal("go test || true remains status-bearing: FAIL summary must fire")
	}
}

func TestClaimVerifyWindowAndPatterns(t *testing.T) {
	tail := strings.Repeat("x", 9000) + "--- FAIL: TestX"
	if !claimVerifyMatch(strings.ToLower(tail), "--- fail:") {
		t.Fatal("canonical form must still match")
	}
	if claimVerifyMatch("pass:120 fail:0 skipped:0", "re:fail:[1-9]") {
		t.Fatal("fail:0 must NOT match the non-zero regex")
	}
	if !claimVerifyMatch("pass:12 fail:3 skipped:0", "re:fail:[1-9]") {
		t.Fatal("fail:3 must match")
	}

	s := newClaimVerifyState()
	bigTail := strings.Repeat("x", 9000) + "\npass:12 fail:3 skipped:0"
	if g := s.check("run_command", bigTail, false, "go test ./..."); g == "" {
		t.Fatal("check() must fire on a tail-window fail count (head-only window would scroll it out)")
	}
	s2 := newClaimVerifyState()
	if g := s2.check("run_command", "ok all\npass:120 fail:0 skipped:0", false, "go test ./..."); g != "" {
		t.Fatalf("check() must NOT fire on fail:0, got %q", g)
	}
}
