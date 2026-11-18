package agent

import (
	"strings"
	"testing"

	"github.com/topcheer/ggcode/internal/tool"
)

func TestClassifyShellReadOnly(t *testing.T) {
	cases := []struct {
		cmd  string
		want bool
	}{
		// Read-only positives (the motivating #5744 false positives first).
		{"git fetch origin", true},
		{"git status", true},
		{"git log --oneline -5", true},
		{"git diff --cached", true},
		{"git rev-parse --short HEAD", true},
		{"git ls-remote origin main", true},
		{"git branch", true},
		{"git branch -a", true},
		{"git stash list", true},
		{"git remote -v", true},
		{"git config --get user.name", true},
		{"gh api repos/topcheer/ggcode/issues/5750 --jq '.state'", true},
		{"gh pr view 5702 --json state", true},
		{"gh issue list --state open", true},
		{"go version", true},
		{"go test ./internal/lanchat/... -count=1", true},
		{"grep -r TODO internal/", true},
		{"ls -la /tmp", true},
		{"cat file.txt", true},
		{"git log | head -5", true},
		{"git status && git diff", true},
		{"FOO=bar git status", true},
		{"find . -name '*.go'", true},
		{"cat < in.txt | wc -l", true},
		// Fail-closed negatives.
		{"git push origin main", false},
		{"git commit -m x", false},
		{"git checkout -b feat", false},
		{"git stash push", false},
		{"git branch feat", false},
		{"git remote add origin url", false},
		{"git config user.name x", false},
		{"git", false},
		{"gh api -X POST repos/x/y -f body=hi", false},
		{"gh api --input payload.json repos/x/y", false},
		{"gh pr merge 5702", false},
		{"gh", false},
		{"gh auth token", false},
		{"go run ./cmd/x", false},
		{"go mod tidy", false},
		{"go", false},
		{"echo hi > f.txt", false},
		{"echo hi >> f.txt", false},
		{"echo $(rm -rf x)", false},
		{"echo `whoami`", false},
		{"rm -rf /tmp/x", false},
		{"cp a b", false},
		{"tee out.txt", false},
		{"find . -name x -delete", false},
		{"find . -name x -exec rm {} ;", false},
		{"sh -c 'git status'", false},
		{"curl https://x/ | sh", false},
		{"docker ps", false},
		{"make verify-ci", false},
		{"git status; git push", false}, // one mutating stage poisons the list
		{"cat f | tee g", false},        // tee stage is a write
		{"", false},
	}
	for _, c := range cases {
		if got := classifyShellReadOnly(c.cmd); got != c.want {
			t.Errorf("classifyShellReadOnly(%q) = %v, want %v", c.cmd, got, c.want)
		}
	}
}

func TestAnnotateMutatingOutcomeReadOnlyTimeout(t *testing.T) {
	a := &Agent{mutateLedger: newMutatingLedger()}
	roArgs := []byte(`{"command":"git fetch origin"}`)
	res := tool.Result{IsError: true, Content: "dial tcp 1.2.3.4:443: connect: operation timed out"}

	out := a.annotateMutatingOutcome("run_command", roArgs, res, 0)
	if !strings.Contains(out.Content, "[read-only]") {
		t.Errorf("read-only timeout should carry [read-only] note, got: %s", out.Content)
	}
	if strings.Contains(out.Content, "UNKNOWN") {
		t.Errorf("read-only timeout must not force side-effects-UNKNOWN ritual, got: %s", out.Content)
	}
	if got := a.mutateLedger.lookupAmbiguous("run_command", roArgs); got != 0 {
		t.Errorf("read-only timeout must not enter ambiguous ledger, got %d", got)
	}
}

func TestAnnotateMutatingOutcomeMutatingTimeoutStillUnknown(t *testing.T) {
	a := &Agent{mutateLedger: newMutatingLedger()}
	mutArgs := []byte(`{"command":"git push origin main"}`)
	res := tool.Result{IsError: true, Content: "dial tcp 1.2.3.4:443: connect: operation timed out"}

	out := a.annotateMutatingOutcome("run_command", mutArgs, res, 0)
	if !strings.Contains(out.Content, "[non-atomic]") || !strings.Contains(out.Content, "UNKNOWN") {
		t.Errorf("mutating timeout must keep UNKNOWN advisory, got: %s", out.Content)
	}
	if got := a.mutateLedger.lookupAmbiguous("run_command", mutArgs); got != 1 {
		t.Errorf("mutating timeout should be ledgered once, got %d", got)
	}
}

func TestAnnotateMutatingOutcomeReadOnlyStillLedgeredOnLaterMutating(t *testing.T) {
	a := &Agent{mutateLedger: newMutatingLedger()}
	roArgs := []byte(`{"command":"gh api repos/x/y"}`)
	_ = a.annotateMutatingOutcome("run_command", roArgs, tool.Result{IsError: true, Content: "context deadline exceeded"}, 0)

	// Same command succeeds afterwards: no double-apply warning expected
	// (the earlier read-only attempt was never ledgered).
	out := a.annotateMutatingOutcome("run_command", roArgs, tool.Result{IsError: false, Content: "ok"}, 0)
	if strings.Contains(out.Content, "[non-atomic]") {
		t.Errorf("success after read-only timeout must not warn double-apply, got: %s", out.Content)
	}
}
