package tool

import (
	"context"
	"encoding/json"
	"runtime"
	"strings"
	"testing"
)

// mallocsPerGateCheck measures heap allocation events per
// NewCommandGate()+Check(cmd) iteration, after warmup.
func mallocsPerGateCheck(cmd string, iters int) uint64 {
	g := NewCommandGate()
	for i := 0; i < 10; i++ {
		_ = g.Check(cmd)
	}
	var before, after runtime.MemStats
	runtime.ReadMemStats(&before)
	for i := 0; i < iters; i++ {
		gate := NewCommandGate()
		_ = gate.Check(cmd)
	}
	runtime.ReadMemStats(&after)
	return (after.Mallocs - before.Mallocs) / uint64(iters)
}

// TestIssue5831CommandGateDoesNotRecompilePerCall: NewCommandGate used to
// compile ~40 rule regexes on every construction (run_command and every
// command-job tool call) and Check recompiled 15 more inside
// destructiveWarnings, keeping regexp/syntax.(*compiler).inst at the top of
// the fleet heap profiles (~10MB/instance). With the tables compiled once,
// a full gate pass costs a few dozen allocations, not thousands.
func TestIssue5831CommandGateDoesNotRecompilePerCall(t *testing.T) {
	for _, cmd := range []string{
		"echo hello",
		"git push --force origin main",
		"rm -rf /",
		"sudo dd if=/dev/zero of=/dev/disk0 bs=1m count=10",
		"ls -la /tmp && grep -r pattern .",
	} {
		if per := mallocsPerGateCheck(cmd, 200); per > 400 {
			t.Errorf("cmd %q: %d mallocs per NewCommandGate+Check (budget 400) - per-call regex compilation is back", cmd, per)
		}
	}
}

// TestIssue5831CommandGateBehaviorUnchanged pins gate semantics across the
// table hoist: block/ask/warn/clean decisions must be byte-identical.
func TestIssue5831CommandGateBehaviorUnchanged(t *testing.T) {
	g := NewCommandGate()

	if r := g.Check("echo hello"); !r.Allowed() {
		t.Errorf("plain echo must stay allowed, got %+v", r)
	}

	r := g.Check("rm -rf /")
	if !r.IsBlocked() || !strings.Contains(r.Reason, "recursive force delete") {
		t.Errorf("rm -rf / must stay blocked with the catastrophic reason, got %+v", r)
	}

	r = g.Check("git push --force origin main")
	if !r.NeedsConfirmation() {
		t.Errorf("force push must stay Ask, got %+v", r)
	}
	found := false
	for _, w := range r.Warnings {
		if strings.Contains(w, "may overwrite remote history") {
			found = true
		}
	}
	if !found {
		t.Errorf("force push must keep its destructive warning, got %v", r.Warnings)
	}

	r = g.Check("git reset --hard")
	found = false
	for _, w := range r.Warnings {
		if strings.Contains(w, "may discard uncommitted changes") {
			found = true
		}
	}
	if !found {
		t.Errorf("git reset --hard must keep its warning, got %v", r.Warnings)
	}

	r = g.Check("GIT_PAGER=cat git log")
	if got := strings.TrimSpace(r.CleanedCmd); got != "git log" {
		t.Errorf("GIT_PAGER override clean rule drifted: cleaned=%q", got)
	}
}

// TestIssue5831GrepInvalidRegexErrorPreserved: grep now compiles through the
// process-wide cache; invalid user patterns must keep surfacing the same
// error shape.
func TestIssue5831GrepInvalidRegexErrorPreserved(t *testing.T) {
	var g Grep
	res, err := g.Execute(context.Background(), json.RawMessage(`{"pattern":"(unclosed"}`))
	if err != nil {
		t.Fatalf("Execute returned error: %v", err)
	}
	if !res.IsError || !strings.Contains(res.Content, "invalid regex") {
		t.Fatalf("invalid pattern must keep the invalid-regex error content, got %+v", res)
	}
}
