package agent

import (
	"os"
	"strings"
	"testing"
)

func TestIssue4648_WarnBranchesRegisterWriteProducts(t *testing.T) {
	raw, err := os.ReadFile("agent_tool.go")
	if err != nil {
		t.Fatalf("read agent_tool.go: %v", err)
	}
	src := string(raw)

	if n := strings.Count(src, "a.recordWriteProducts(tc, res)"); n != 3 {
		t.Errorf("recordWriteProducts call sites = %d, want 3 (normal + both warn branches); #4648 wiring drifted", n)
	}

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

	if strings.Contains(src, "if e := a.invariantEngineLazy(); e != nil && !res.IsError {") {
		t.Error("inline r454 block still present in normal path; should route through recordWriteProducts")
	}
}
