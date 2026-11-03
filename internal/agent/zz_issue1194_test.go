package agent

import (
	"strings"
	"testing"
)

func recordSignalAndGet(t *testing.T, toolName, args string) (testsRan, buildRan bool) {
	t.Helper()
	r := newReversibilityState()
	r.recordSafetySignal(toolName, args)
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.testsRan, r.buildRan
}

func TestIssue1194_SubstringFalsePositivesDoNotSetSignals(t *testing.T) {
	cases := []struct {
		name string
		args string
	}{
		{
			name: "latest directory contains test substring",
			args: "# List release artifacts\nls -la releases/latest/",
		},
		{
			name: "Makefile contains make substring",
			args: "cat Makefile",
		},
		{
			name: "head Makefile contains make substring",
			args: "head Makefile",
		},
		{
			name: "curl releases/latest URL contains test substring",
			args: "curl -sSL https://example.com/downloads/releases/latest",
		},
		{
			name: "test in comment only",
			args: "# run the test suite later\ngit status",
		},
		{
			name: "build in path not as word",
			args: "ls _build/ latest-build.txt",
		},
		{
			name: "makefile as single word is not make",
			args: "touch Makefile",
		},
		{
			name: "latest as single word is not test",
			args: "cd latest",
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			testsRan, buildRan := recordSignalAndGet(t, "run_command", tc.args)
			if testsRan {
				t.Errorf("testsRan should NOT be set for: %q", tc.args)
			}
			if buildRan {
				t.Errorf("buildRan should NOT be set for: %q", tc.args)
			}
		})
	}
}

func TestIssue1194_RealTestBuildCommandsSetSignals(t *testing.T) {
	cases := []struct {
		name               string
		args               string
		wantTests, wantBld bool
	}{
		{name: "go test", args: "go test ./...", wantTests: true},
		{name: "go test with tags", args: "# Run tests\ngo test -tags goolm ./internal/agent/", wantTests: true},
		{name: "make test", args: "make test", wantTests: true},
		{name: "npm test", args: "npm test", wantTests: true},
		{name: "pytest", args: "pytest -q scripts/", wantTests: true},
		{name: "make build", args: "make build", wantBld: true},
		{name: "go build", args: "go build ./...", wantBld: true},
		{name: "make alone", args: "make", wantBld: true},
		{name: "npm run build", args: "npm run build", wantBld: true},
		{name: "test subcommand bare", args: "go tool test2json", wantTests: false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			testsRan, buildRan := recordSignalAndGet(t, "run_command", tc.args)
			if testsRan != tc.wantTests {
				t.Errorf("testsRan = %v, want %v (args: %q)", testsRan, tc.wantTests, tc.args)
			}
			if buildRan != tc.wantBld {
				t.Errorf("buildRan = %v, want %v (args: %q)", buildRan, tc.wantBld, tc.args)
			}
		})
	}
}

func TestIssue1194_WarningSuppressionBehavior(t *testing.T) {

	r := newReversibilityState()
	r.recordSafetySignal("run_command", "# inspect latest artifacts\nls -la releases/latest/")
	msg := r.checkPreAction("git_commit", "")
	if !strings.Contains(msg, "without tests/build") {
		t.Fatalf("git_commit warning was suppressed by a read-only command containing 'test' substring; got %q", msg)
	}

	r2 := newReversibilityState()
	r2.recordSafetySignal("run_command", "go test ./...")
	if msg := r2.checkPreAction("git_commit", ""); msg != "" {
		t.Fatalf("git_commit warning should be suppressed after real go test; got %q", msg)
	}
}

func TestIssue1194_IsGitPush(t *testing.T) {
	if !isGitPush("git push origin main --tags") {
		t.Error("expected git push detection with extra args")
	}

	if !isGitPush("git pushd") {
		t.Error("legacy containment: 'git pushd' should still be flagged conservatively")
	}
	if isGitPush("ls -la") {
		t.Error("unexpected git push detection")
	}
	if isGitPush("git status") {
		t.Error("git status is not a push")
	}
}

func TestIssue1194_CommandTokensStripsComment(t *testing.T) {
	if got := commandTokens("# only comment"); len(got) != 0 {
		t.Errorf("comment-only args should yield no tokens, got %v", got)
	}
	got := commandTokens("# Build the project\ngo build ./...")
	if len(got) != 3 || got[0] != "go" || got[1] != "build" || got[2] != "./..." {
		t.Errorf("unexpected tokens: %v", got)
	}

	got = commandTokens("make build")
	if len(got) != 2 || got[0] != "make" {
		t.Errorf("unexpected tokens: %v", got)
	}
}
