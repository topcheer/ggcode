package agent

import (
	"strings"
	"testing"

	"github.com/topcheer/ggcode/internal/tool"
)

// #5744: a timed-out run_command that static write-surface analysis proves
// read-only must say "safe to re-run" instead of the unconditional
// side-effects-UNKNOWN banner, and must not poison the ambiguous-attempt
// ledger. Write-surface timeouts keep the full UNKNOWN ritual (the
// coordinator's gh pr merge incident is the canonical example of why).

func TestIssue5744ReadOnlyTimeoutSaysSafeToRerun(t *testing.T) {
	a := &Agent{mutateLedger: newMutatingLedger()}
	a.guidanceBudget.reset()
	args := []byte(`{"command":"gh api repos/topcheer/ggcode/pulls/5744"}`)
	res := tool.Result{Content: "tool error: command timed out after 120s", IsError: true}
	out := a.annotateMutatingOutcome("run_command", args, res, 0)
	if !strings.Contains(out.Content, "safe to re-run") {
		t.Fatalf("missing safe-to-re-run guidance:\n%s", out.Content)
	}
	if strings.Contains(out.Content, "UNKNOWN") || strings.Contains(out.Content, "Do NOT blindly re-issue") {
		t.Fatalf("read-only timeout must not carry UNKNOWN guidance:\n%s", out.Content)
	}
	if got := a.mutateLedger.lookupAmbiguous("run_command", args); got != 0 {
		t.Fatalf("read-only timeout must not record an ambiguous ledger entry: %d", got)
	}
}

func TestIssue5744ReadOnlyGrepTimeoutSaysSafeToRerun(t *testing.T) {
	a := &Agent{mutateLedger: newMutatingLedger()}
	a.guidanceBudget.reset()
	args := []byte(`{"command":"grep -rn 'non-atomic' internal/ | head -20"}`)
	res := tool.Result{Content: "tool error: command timed out after 1800s", IsError: true}
	out := a.annotateMutatingOutcome("run_command", args, res, 0)
	if !strings.Contains(out.Content, "READ-ONLY") || !strings.Contains(out.Content, "safe to re-run") {
		t.Fatalf("missing read-only safe-to-re-run guidance:\n%s", out.Content)
	}
	if got := a.mutateLedger.lookupAmbiguous("run_command", args); got != 0 {
		t.Fatalf("read-only timeout must not record an ambiguous ledger entry: %d", got)
	}
}

func TestIssue5744WriteTimeoutKeepsUnknownRitualAndLedger(t *testing.T) {
	a := &Agent{mutateLedger: newMutatingLedger()}
	a.guidanceBudget.reset()
	// The incident from the issue body: gh pr merge interrupted by a
	// graphql dial-timeout had to be hand-verified before re-issue.
	args := []byte(`{"command":"gh pr merge 4934 --merge"}`)
	res := tool.Result{Content: "tool error: command timed out after 120s", IsError: true}
	out := a.annotateMutatingOutcome("run_command", args, res, 0)
	if !strings.Contains(out.Content, "UNKNOWN") || !strings.Contains(out.Content, "Do NOT blindly re-issue") {
		t.Fatalf("write-surface timeout must keep the UNKNOWN guidance:\n%s", out.Content)
	}
	if got := a.mutateLedger.lookupAmbiguous("run_command", args); got != 1 {
		t.Fatalf("write timeout must record an ambiguous ledger entry: %d", got)
	}
}

func TestIssue5744GarbledArgsFallBackToUnknown(t *testing.T) {
	a := &Agent{mutateLedger: newMutatingLedger()}
	a.guidanceBudget.reset()
	res := tool.Result{Content: "tool error: timeout", IsError: true}
	out := a.annotateMutatingOutcome("run_command", []byte(`{"command":"grep -rn x .`), res, 0)
	if !strings.Contains(out.Content, "UNKNOWN") {
		t.Fatalf("unparseable args must fall back to the UNKNOWN banner:\n%s", out.Content)
	}
}

func TestIssue5744EmptyCommandFallsBackToUnknown(t *testing.T) {
	a := &Agent{mutateLedger: newMutatingLedger()}
	a.guidanceBudget.reset()
	res := tool.Result{Content: "tool error: timeout", IsError: true}
	out := a.annotateMutatingOutcome("run_command", []byte(`{"command":"  "}`), res, 0)
	if !strings.Contains(out.Content, "UNKNOWN") {
		t.Fatalf("empty command must fall back to the UNKNOWN banner:\n%s", out.Content)
	}
}

func TestIssue5744OtherShellToolsNotReclassified(t *testing.T) {
	// start_command spawns a background job: re-running is not free even
	// for a read-only program, so it keeps the UNKNOWN banner.
	a := &Agent{mutateLedger: newMutatingLedger()}
	a.guidanceBudget.reset()
	args := []byte(`{"command":"grep -rn x ."}`)
	res := tool.Result{Content: "tool error: timeout", IsError: true}
	out := a.annotateMutatingOutcome("start_command", args, res, 0)
	if !strings.Contains(out.Content, "UNKNOWN") {
		t.Fatalf("start_command must keep the UNKNOWN banner:\n%s", out.Content)
	}
}
