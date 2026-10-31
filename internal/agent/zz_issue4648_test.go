package agent

import (
	"os"
	"strings"
	"testing"
)

// Regression guard for #4648: the workflow-warn and invariant-warn branches
// EXECUTE the real tool (same runTraced as the normal path), so their
// successful write products must reach the invariant engine's created_by_run
// ledger exactly like the normal path. Pre-fix, only the normal path called
// recordProduct - a warn-executed write was invisible to the deletion guard,
// which then blocked the agent from deleting files it had itself just created.
//
// The full warn-branch path needs a configured invariant/workflow engine, so
// this pins the wiring structurally (same convention as zz_issue4634): all
// three execution branches must route through the shared helper.
func TestIssue4648_WarnBranchesRegisterWriteProducts(t *testing.T) {
	raw, err := os.ReadFile("agent_tool.go")
	if err != nil {
		t.Fatalf("read agent_tool.go: %v", err)
	}
	src := string(raw)

	// Exactly three call sites: normal path + workflow-warn + invariant-warn.
	if n := strings.Count(src, "a.recordWriteProducts(tc, res)"); n != 3 {
		t.Errorf("recordWriteProducts call sites = %d, want 3 (normal + both warn branches); #4648 wiring drifted", n)
	}

	// The helper itself must keep the r454/#3254 semantics: all three
	// write-class ops registered, error results skipped, nil engine skipped.
	for _, frag := range []string{
		"func (a *Agent) recordWriteProducts(",
		`if res.IsError {`,
		`e := a.invariantEngineLazy()`,
		`if e == nil {`,
		`ot.Op == "write"`,
		`ot.Op == "mkdir"`,
		`ot.Op == "move"`,
		"e.recordProduct(ot.Target)",
	} {
		if !strings.Contains(src, frag) {
			t.Errorf("recordWriteProducts helper lost required fragment %q", frag)
		}
	}

	// The old inline block must not have survived in the normal path
	// (single source of truth for the registration semantics).
	if strings.Contains(src, "if e := a.invariantEngineLazy(); e != nil && !res.IsError {") {
		t.Error("inline r454 block still present in normal path; should route through recordWriteProducts")
	}
}
