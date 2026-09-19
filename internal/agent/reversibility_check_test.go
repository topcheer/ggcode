package agent

import (
	"strings"
	"testing"
)

func TestReversibilityReset(t *testing.T) {
	r := newReversibilityState()
	r.testsRan = true
	r.buildRan = true
	r.warnCount = 1
	r.reset()
	if r.testsRan || r.buildRan || r.warnCount != 0 {
		t.Fatal("reset did not clear state")
	}
}

func TestReversibilityRecordSafetySignal(t *testing.T) {
	r := newReversibilityState()
	r.recordSafetySignal("run_command", `{"command":"go test ./..."}`)
	if !r.testsRan {
		t.Error("expected testsRan=true after go test")
	}
	r.reset()
	r.recordSafetySignal("run_command", `{"command":"go build ./..."}`)
	if !r.buildRan {
		t.Error("expected buildRan=true after go build")
	}
	r.reset()
	r.recordSafetySignal("run_command", `{"command":"make verify-ci"}`)
	if !r.buildRan {
		t.Error("expected buildRan=true after make")
	}
	r.reset()
	r.recordSafetySignal("git_add", `{"files":["a.go"]}`)
	if !r.stagingSeen {
		t.Error("expected stagingSeen=true after git_add")
	}
}

func TestReversibilityCheckCommitWithoutVerification(t *testing.T) {
	r := newReversibilityState()
	guidance := r.checkPreAction("git_commit", `{"message":"feat: add X"}`)
	if guidance == "" {
		t.Fatal("expected reversibility warning for commit without tests/build")
	}
	if !strings.Contains(guidance, "reversibility") {
		t.Errorf("unexpected guidance: %s", guidance)
	}
}

func TestReversibilityCheckCommitAfterTestNoWarn(t *testing.T) {
	r := newReversibilityState()
	r.recordSafetySignal("run_command", `{"command":"go test ./..."}`)
	guidance := r.checkPreAction("git_commit", `{"message":"feat: add X"}`)
	if guidance != "" {
		t.Fatalf("expected no warning after test verification, got: %s", guidance)
	}
}

func TestReversibilityCheckCommitAfterBuildNoWarn(t *testing.T) {
	r := newReversibilityState()
	r.recordSafetySignal("run_command", `{"command":"go build ./..."}`)
	guidance := r.checkPreAction("git_commit", `{"message":"feat: add X"}`)
	if guidance != "" {
		t.Fatalf("expected no warning after build verification, got: %s", guidance)
	}
}

func TestReversibilityCheckPushWithoutVerification(t *testing.T) {
	r := newReversibilityState()
	guidance := r.checkPreAction("run_command", `{"command":"git push origin main"}`)
	if guidance == "" {
		t.Fatal("expected reversibility warning for push without tests/build")
	}
}

func TestReversibilityCheckPushAfterTestNoWarn(t *testing.T) {
	r := newReversibilityState()
	r.recordSafetySignal("run_command", `{"command":"go test ./..."}`)
	guidance := r.checkPreAction("run_command", `{"command":"git push origin main"}`)
	if guidance != "" {
		t.Fatalf("expected no warning for push after test, got: %s", guidance)
	}
}

func TestReversibilityCheckDestructiveGit(t *testing.T) {
	r := newReversibilityState()
	guidance := r.checkPreAction("run_command", `{"command":"git reset --hard HEAD~1"}`)
	if guidance == "" {
		t.Fatal("expected reversibility warning for reset --hard")
	}
	if !strings.Contains(strings.ToLower(guidance), "destructive") {
		t.Errorf("expected 'not reversible' in guidance: %s", guidance)
	}
}

func TestReversibilityCheckCleanForce(t *testing.T) {
	r := newReversibilityState()
	guidance := r.checkPreAction("run_command", `{"command":"git clean -f"}`)
	if guidance == "" {
		t.Fatal("expected reversibility warning for clean -f")
	}
}

func TestReversibilityCheckFileOpsDelete(t *testing.T) {
	r := newReversibilityState()
	guidance := r.checkPreAction("file_ops", `{"operations":[{"action":"delete","source":"/tmp/test.go"}]}`)
	if guidance == "" {
		t.Fatal("expected reversibility warning for file deletion")
	}
}

func TestReversibilityCheckFileOpsMoveNoWarn(t *testing.T) {
	r := newReversibilityState()
	guidance := r.checkPreAction("file_ops", `{"operations":[{"action":"move","source":"a.go","destination":"b.go"}]}`)
	if guidance != "" {
		t.Fatalf("expected no warning for file move, got: %s", guidance)
	}
}

func TestReversibilityCheckSafeToolNoWarn(t *testing.T) {
	r := newReversibilityState()
	tools := []string{"read_file", "grep", "edit_file", "write_file", "glob"}
	for _, tool := range tools {
		guidance := r.checkPreAction(tool, `{}`)
		if guidance != "" {
			t.Fatalf("expected no warning for safe tool %s, got: %s", tool, guidance)
		}
	}
}

func TestReversibilityMaxWarnings(t *testing.T) {
	r := newReversibilityState()
	r.maxWarnings = 2
	// First two should warn
	g1 := r.checkPreAction("git_commit", `{"message":"a"}`)
	g2 := r.checkPreAction("run_command", `{"command":"git reset --hard"}`)
	if g1 == "" || g2 == "" {
		t.Fatal("expected first two warnings")
	}
	// Third should be suppressed (maxWarnings reached)
	g3 := r.checkPreAction("file_ops", `{"operations":[{"action":"delete","source":"x"}]}`)
	if g3 != "" {
		t.Fatalf("expected third warning to be suppressed, got: %s", g3)
	}
}

func TestReversibilityCheckCheckoutSpecificFile(t *testing.T) {
	r := newReversibilityState()
	guidance := r.checkPreAction("run_command", `{"command":"git checkout -- src/foo.go"}`)
	if guidance == "" {
		t.Fatal("expected reversibility warning for checkout -- specific file")
	}
}

func TestReversibilityCheckoutBranchNotDestructive(t *testing.T) {
	if isDestructiveGit("git checkout main") {
		t.Error("git checkout <branch> should not be destructive")
	}
	if isDestructiveGit("git checkout -b feature") {
		t.Error("git checkout -b should not be destructive")
	}
}

func TestReversibilityCommitAfterStagingNoWarn(t *testing.T) {
	r := newReversibilityState()
	r.recordSafetySignal("git_add", `{"files":["a.go"]}`)
	guidance := r.checkPreAction("git_commit", `{"message":"feat: add X"}`)
	if guidance != "" {
		t.Fatalf("expected no warning after staging, got: %s", guidance)
	}
}

// TestReversibilityMakeTargetSensitivity pins #2552: non-build make targets
// (clean/fmt/tidy/install-*) must NOT flip buildRan - they used to disarm the
// commit/push "verify first" guidance without any real compilation.
func TestReversibilityMakeTargetSensitivity(t *testing.T) {
	for _, tc := range []struct {
		cmd   string
		build bool
		tests bool
	}{
		{`make clean`, false, false},
		{`make fmt`, false, false},
		{`make tidy`, false, false},
		{`make install-git-hooks`, false, false},
		{`make`, true, false},               // bare make: default target is conventionally all/build
		{`{"command":"make"}`, true, false}, // bare make via JSON envelope
		{`make build`, true, false},
		{`make all`, true, false},
		{`make verify-ci`, true, false},
		{`make test`, false, true},
		{`make check`, false, true},
	} {
		r := newReversibilityState()
		r.recordSafetySignal("run_command", tc.cmd)
		if r.buildRan != tc.build {
			t.Errorf("%s: buildRan=%v, want %v", tc.cmd, r.buildRan, tc.build)
		}
		if r.testsRan != tc.tests {
			t.Errorf("%s: testsRan=%v, want %v", tc.cmd, r.testsRan, tc.tests)
		}
	}
}

// TestReversibilityMultiGitGlobalFlagStripping pins #2560: the range
// snapshot in stripGitGlobalFlagTokens left later git segments unstripped
// after the first rebuild - the subcommand anchored on "-C" and the
// destructive bigram was silently missed.
func TestReversibilityMultiGitGlobalFlagStripping(t *testing.T) {
	cases := []struct {
		name string
		cmd  string
		want bool
	}{
		{"second git clean -fd missed pre-fix", "git -C a status && git -C b clean -fd", true},
		{"second git reset --hard missed pre-fix", "git -C a log && git -C b reset --hard", true},
		{"single command with -C still detected", "git -C b clean -fd", true},
		{"first git flagless second with -C detected", "git status && git -C b clean -fd", true},
		{"benign multi-git no false positive", "git -C a status && git -C b log", false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := isDestructiveGit(tc.cmd); got != tc.want {
				t.Errorf("isDestructiveGit(%q) = %v, want %v", tc.cmd, got, tc.want)
			}
		})
	}
}
