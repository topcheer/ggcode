//go:build goolm

package agent

import (
	"os"
	"strings"
	"testing"

	"github.com/topcheer/ggcode/internal/tool"
)

func plans3853(n int) []tool.PlannedFileEdit {
	plans := make([]tool.PlannedFileEdit, 0, n)
	for i := 0; i < n; i++ {
		plans = append(plans, tool.PlannedFileEdit{
			Path:       "/tmp/fake" + string(rune('a'+i)) + ".go",
			OldContent: "package a\n",
			NewContent: "package a\nfunc f() {}\n",
		})
	}
	return plans
}

func TestIssue3853_WrittenSetSkipsUnwrittenPlans(t *testing.T) {
	a := NewAgent(nil, nil, "sys", 5)
	called := map[string]bool{}
	var res tool.Result
	plans := plans3853(3)
	written := map[string]bool{plans[0].Path: true}
	a.runPostWriteWarnings(&res, plans, func(path, oldC, newC string) string {
		called[path] = true
		return ""
	}, written)
	if len(called) != 1 || !called[plans[0].Path] {
		t.Fatalf("only the written path must reach the checker, got %v", called)
	}
}

func TestIssue3853_NilWrittenSetRunsAll(t *testing.T) {
	a := NewAgent(nil, nil, "sys", 5)
	called := 0
	var res tool.Result
	a.runPostWriteWarnings(&res, plans3853(3), func(path, oldC, newC string) string {
		called++
		return ""
	}, nil)
	if called != 3 {
		t.Fatalf("nil written-set (non-partial tools) must run all plans, got %d", called)
	}
}

func TestIssue3853_CallSitesWired(t *testing.T) {
	src, err := readAgentToolSource()
	if err != nil {
		t.Skipf("source unavailable: %v", err)
	}
	for _, needle := range []string{
		"runPostWriteWarnings(&result, plans, CheckMissingTestCompanionWithFS, writtenSet)",
		"runPostWriteWarnings(&result, plans, checkDebugStmts, writtenSet)",
		"}, writtenSet)",
		"!result.IsError && !isDryRun {\n\t\ta.runPostWriteWarnings",
	} {
		if !strings.Contains(src, needle) {
			t.Fatalf("call-site wiring missing %q", needle)
		}
	}
}

func readAgentToolSource() (string, error) {
	b, err := os.ReadFile("agent_tool.go")
	if err != nil {
		return "", err
	}
	return string(b), nil
}
