package agent

// #2486: the dedup ledger's workspace epoch must also advance when a shell
// command rewrites workspace files. The epoch protects the classic verify
// loop (edit file -> re-run same test command); before this fix only the
// builtin file tools bumped it, so "fix with sed -i -> re-run tests" had
// its re-run suppressed, replaying the pre-fix (stale) test result.

import (
	"testing"
	"time"

	"github.com/topcheer/ggcode/internal/tool"
)

func epochOf(l *toolDedupLedger) uint64 {
	l.mu.Lock()
	defer l.mu.Unlock()
	return l.epoch
}

func TestCommandMayRewriteWorkspace(t *testing.T) {
	cases := []struct {
		name    string
		tool    string
		args    string
		rewrite bool
	}{
		// #2486 original repro: sed -i is a file mutation the epoch missed.
		{"sed -i", "run_command", `{"command":"sed -i 's/old/new/' main.go"}`, true},
		{"output redirect", "run_command", `{"command":"echo hi > out.txt"}`, true},
		{"append redirect", "run_command", `{"command":"echo hi >> log.txt"}`, true},
		{"git checkout", "run_command", `{"command":"git checkout main"}`, true},
		{"git stash pop", "run_command", `{"command":"git stash pop"}`, true},
		{"git reset hard", "run_command", `{"command":"git reset --hard HEAD~1"}`, true},
		{"cp word", "run_command", `{"command":"cp a.go b.go"}`, true},
		{"rm word", "run_command", `{"command":"rm -rf tmpdir"}`, true},
		{"tee", "run_command", `{"command":"echo x | tee f.txt"}`, true},
		{"go fmt", "run_command", `{"command":"gofmt -w ."}`, true},

		// Reads/verification must NOT bump: these keep suppression protection.
		{"go test", "run_command", `{"command":"go test ./..."}`, false},
		{"go build", "run_command", `{"command":"go build ./..."}`, false},
		{"grep", "run_command", `{"command":"grep -rn foo internal/"}`, false},
		{"cat with pipe", "run_command", `{"command":"cat a.go | grep x | wc -l"}`, false},
		{"git status", "run_command", `{"command":"git status"}`, false},
		{"git log", "run_command", `{"command":"git log --oneline -5"}`, false},
		// Sink-neutralized: 2>&1 pipelines are not file writes.
		{"stderr merge only", "run_command", `{"command":"go test ./... 2>&1 | tail -5"}`, false},
		{"devnull sink", "run_command", `{"command":"git fetch origin main 2>/dev/null"}`, false},

		// Non-shell tools: decided by fileMutatingTools, not this helper.
		{"write_file tool", "write_file", `{"path":"/x","content":"y"}`, false},
		{"edit_file tool", "edit_file", `{"file_path":"/x"}`, false},
	}
	for _, tc := range cases {
		if got := commandMayRewriteWorkspace(tc.tool, tc.args); got != tc.rewrite {
			t.Errorf("%s: commandMayRewriteWorkspace(%s, %s) = %v, want %v", tc.name, tc.tool, tc.args, got, tc.rewrite)
		}
	}
}

// The #2486 end-to-end repro: a successful shell file-mutation must bump the
// epoch so a subsequent IDENTICAL test command is not suppressed.
func TestDedupEpochBumpsOnShellFileMutation(t *testing.T) {
	l := newToolDedupLedger()
	l.ttl = 10 * time.Second

	// 1. Verify command runs and succeeds.
	testArgs := `{"command":"go test ./internal/agent/"}`
	l.record("run_command", testArgs, tool.Result{Content: "ok"})

	// 2. Fix the bug with sed -i (the exact #2486 repro path).
	before := epochOf(l)
	l.record("run_command", `{"command":"sed -i 's/bug/fix/' agent.go"}`, tool.Result{Content: ""})
	if epochOf(l) <= before {
		t.Fatalf("epoch must bump on successful shell file mutation (#2486): before=%d after=%d", before, epochOf(l))
	}

	// 3. The identical test command must NOT be suppressed anymore.
	if suppressed := l.suppressDuplicate("run_command", testArgs); suppressed != nil {
		t.Fatalf("post-mutation verify re-run must not be suppressed (#2486): %q", suppressed.Content)
	}
}

// Reads-only shell commands keep the suppression: an uninterrupted duplicate
// retry of a read-only mutating-classified command is still suppressed.
func TestDedupStillSuppressesUninterruptedReadRetries(t *testing.T) {
	l := newToolDedupLedger()
	l.ttl = 10 * time.Second
	args := `{"command":"go test ./internal/agent/"}`
	l.record("run_command", args, tool.Result{Content: "ok 3.2s"})
	if suppressed := l.suppressDuplicate("run_command", args); suppressed == nil {
		t.Fatal("reads-only duplicate retry must still be suppressed (protection not lost)")
	}
}

// git_checkout / git_stash / git_reset / git_revert tool successes bump the
// epoch directly (they are all in fileMutatingTools - git_revert per #2492:
// the native tool applies the inverse patch to the working tree in both
// --no-commit and commit modes, exactly like its shell counterpart which the
// frag list already covered).
func TestDedupEpochBumpsOnGitTreeTools(t *testing.T) {
	l := newToolDedupLedger()
	for _, name := range []string{"git_checkout", "git_stash", "git_reset", "git_revert"} {
		before := epochOf(l)
		l.record(name, `{}`, tool.Result{Content: ""})
		if epochOf(l) <= before {
			t.Errorf("%s success must bump the epoch (#2486/#2492)", name)
		}
	}
}

// The #2492 end-to-end path: a verify loop that reverts a bad commit with the
// NATIVE git_revert tool must not have its identical re-verify suppressed.
func TestDedupEpochBumpsOnNativeGitRevertReverify(t *testing.T) {
	l := newToolDedupLedger()
	l.ttl = 10 * time.Second

	verifyArgs := `{"command":"go test ./internal/agent/"}`
	l.record("run_command", verifyArgs, tool.Result{Content: "FAIL pre-revert"})

	// Revert the bad commit with the native tool (not the shell).
	l.record("git_revert", `{"commit":"abc123"}`, tool.Result{Content: ""})

	// The identical re-verify must NOT replay the pre-revert FAIL result.
	if suppressed := l.suppressDuplicate("run_command", verifyArgs); suppressed != nil {
		t.Fatalf("post-revert re-verify must not be suppressed (#2492): %q", suppressed.Content)
	}
}
