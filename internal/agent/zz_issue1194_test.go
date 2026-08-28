package agent

// Issue #1194 characteristic tests: recordSafetySignal must match test/build
// signals at WORD boundaries after stripping the leading '# ' comment, so
// that paths like "releases/latest" ("latest" contains "test") or
// "Makefile" (contains "make") no longer permanently set testsRan/buildRan
// and silently suppress the git_commit/git push reversibility warnings.

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

// False-positive cases: none of these commands ran tests or builds, yet the
// old substring Contains matching flagged them.
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

// True-positive cases: real test/build invocations must still set signals.
func TestIssue1194_RealTestBuildCommandsSetSignals(t *testing.T) {
	cases := []struct {
		name               string
		args               string
		wantTests, wantBld bool
	}{
		{name: "go test", args: "go test ./...", wantTests: true},
		{name: "go test with tags", args: "# Run tests\ngo test -tags goolm ./internal/agent/", wantTests: true},
		{name: "make test", args: "make test", wantTests: true, wantBld: true}, // "make" word still signals build (legacy semantic preserved)
		{name: "npm test", args: "npm test", wantTests: true},
		{name: "pytest", args: "pytest -q scripts/", wantTests: true},
		{name: "make build", args: "make build", wantBld: true},
		{name: "go build", args: "go build ./...", wantBld: true},
		{name: "make alone", args: "make", wantBld: true},
		{name: "npm run build", args: "npm run build", wantBld: true},
		{name: "test subcommand bare", args: "go tool test2json", wantTests: false}, // test2json is a word, not "test"
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

// End-to-end: a benign read command must NOT suppress the git_commit
// reversibility warning, while a real go test must.
func TestIssue1194_WarningSuppressionBehavior(t *testing.T) {
	// Benign read-only command: warning still fires for git_commit.
	r := newReversibilityState()
	r.recordSafetySignal("run_command", "# inspect latest artifacts\nls -la releases/latest/")
	msg := r.checkPreAction("git_commit", "")
	if !strings.Contains(msg, "without tests/build") {
		t.Fatalf("git_commit warning was suppressed by a read-only command containing 'test' substring; got %q", msg)
	}

	// Real test run: warning must not fire.
	r2 := newReversibilityState()
	r2.recordSafetySignal("run_command", "go test ./...")
	if msg := r2.checkPreAction("git_commit", ""); msg != "" {
		t.Fatalf("git_commit warning should be suppressed after real go test; got %q", msg)
	}
}

// isGitPush previously had two identical || branches (duplication cleanup).
func TestIssue1194_IsGitPush(t *testing.T) {
	if !isGitPush("git push origin main --tags") {
		t.Error("expected git push detection with extra args")
	}
	// Containment semantics for the two-word command are legacy-retained:
	// "git pushd" contains "git push" and is still flagged (conservative).
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

// commandTokens: comment stripping behavior.
func TestIssue1194_CommandTokensStripsComment(t *testing.T) {
	if got := commandTokens("# only comment"); len(got) != 0 {
		t.Errorf("comment-only args should yield no tokens, got %v", got)
	}
	got := commandTokens("# Build the project\ngo build ./...")
	if len(got) != 3 || got[0] != "go" || got[1] != "build" || got[2] != "./..." {
		t.Errorf("unexpected tokens: %v", got)
	}
	// No comment: passthrough.
	got = commandTokens("make build")
	if len(got) != 2 || got[0] != "make" {
		t.Errorf("unexpected tokens: %v", got)
	}
}
