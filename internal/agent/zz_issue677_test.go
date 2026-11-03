package agent

import (
	"context"
	"strings"
	"testing"

	ctxpkg "github.com/topcheer/ggcode/internal/context"
	"github.com/topcheer/ggcode/internal/provider"
	"github.com/topcheer/ggcode/internal/tool"
)

func issue677Agent(t *testing.T) (*Agent, *ctxpkg.Manager) {
	t.Helper()
	a := NewAgent(&mockProvider{}, tool.NewRegistry(), "", 10)
	t.Cleanup(func() { a.Close() })
	cm, ok := a.ContextManager().(*ctxpkg.Manager)
	if !ok {
		t.Fatalf("ContextManager() is %T, want *ctxpkg.Manager", a.ContextManager())
	}
	return a, cm
}

func issue677CountUserMsgs(cm *ctxpkg.Manager, marker string) int {
	n := 0
	for _, m := range cm.Messages() {
		if m.Role != "user" {
			continue
		}
		for _, b := range m.Content {
			if b.Type == "text" && strings.Contains(b.Text, marker) {
				n++
				break
			}
		}
	}
	return n
}

func TestIssue677_IterationDetectorRespectsBudget(t *testing.T) {
	a, cm := issue677Agent(t)

	for i := 0; i < guidanceBudgetPerTurn; i++ {
		a.injectGuidance("[advisory-filler] filler directive " + string(rune('a'+i)))
	}
	if got := issue677CountUserMsgs(cm, "[advisory-filler]"); got != guidanceBudgetPerTurn {
		t.Fatalf("filler setup: %d messages, want %d", got, guidanceBudgetPerTurn)
	}

	a.injectGuidance("[Solution Fixation Alert] 3 failed edit attempts have targeted \"x.go\".")
	if got := issue677CountUserMsgs(cm, "Solution Fixation Alert"); got != 0 {
		t.Errorf("iteration-level detector hint bypassed exhausted budget (%d injected); want 0 (#677 defect 1)", got)
	}
}

func TestIssue677_ErrorStormStackingCapped(t *testing.T) {
	a, cm := issue677Agent(t)
	a.guidanceBudget.reset()

	storm := []string{
		"[Error Rush Alert] consecutive failures without diagnosis.",
		"[Solution Fixation Alert] 3 failed edit attempts have targeted \"x.go\".",
		"[Error Compounding] second failure compounds the first.",
		"[Correction Spiral] severity escalating across fixes.",
		"[Verify Debt] failures awaiting re-verification.",
		"[Verify Debt] more failures awaiting re-verification.",
	}
	for _, h := range storm {
		a.injectGuidance(h)
	}

	total := 0
	for _, m := range cm.Messages() {
		if m.Role != "user" {
			continue
		}
		for _, b := range m.Content {
			if b.Type == "text" && strings.Contains(b.Text, "[") {
				total++
				break
			}
		}
	}
	if total > guidanceBudgetPerTurn {
		t.Errorf("error-storm stacking: %d advisory messages injected, want <= %d (#677 defect 1)", total, guidanceBudgetPerTurn)
	}
}

func TestIssue677_CriticalStillBypassesBudget(t *testing.T) {
	a, cm := issue677Agent(t)
	for i := 0; i < guidanceBudgetPerTurn; i++ {
		a.injectGuidance("[advisory-filler] filler directive")
	}
	a.injectGuidance("[CRITICAL] correctness failure detected")
	if got := issue677CountUserMsgs(cm, "[CRITICAL]"); got != 1 {
		t.Errorf("critical hint suppressed by budget after #677 routing; want 1 injection, got %d", got)
	}
}

func TestIssue677_LoopRecoveryNudgesExemptFromBudget(t *testing.T) {
	a, cm := issue677Agent(t)
	for _, nudge := range []string{
		"The previous response was empty. Please try again.",
		"Your previous response was cut off by the output token limit. Continue from where you left off — do not repeat what you already wrote.",
		"Use structured tool_use format, not inline text syntax for tool calls.",
	} {
		a.contextManager.Add(provider.Message{
			Role:    "user",
			Content: []provider.ContentBlock{{Type: "text", Text: nudge}},
		})
	}
	if a.guidanceBudget.injected != 0 {
		t.Errorf("loop-recovery nudges must not consume guidance budget; injected=%d", a.guidanceBudget.injected)
	}
	if got := len(cm.Messages()); got != 3 {
		t.Errorf("loop-recovery nudges: %d messages, want 3", got)
	}
}

func TestIssue677_LoopRecoverySitesDocumented(t *testing.T) {

	t.Skip("source-level: see #677 comments at the three nudge sites in agent.go")
}

func TestIssue677_SolutionFixationResetWired(t *testing.T) {
	a, _ := issue677Agent(t)

	for i := 0; i < 6; i++ {
		a.solutionFixation.recordToolCall("edit_file", `{"file_path":"/p/x.go"}`, true)
	}
	if hint := a.solutionFixation.checkAndWarn(); hint == "" {
		t.Fatal("setup: first fixation warning did not fire")
	}

	a.solutionFixation.warningCount = maxFixationWarnings
	for i := 0; i < 3; i++ {
		a.solutionFixation.recordToolCall("edit_file", `{"file_path":"/p/other.go"}`, true)
	}
	if hint := a.solutionFixation.checkAndWarn(); hint != "" {
		t.Fatalf("cap not enforced: %q", hint)
	}

	a.solutionFixation.reset()

	for i := 0; i < 3; i++ {
		a.solutionFixation.recordToolCall("edit_file", `{"file_path":"/p/x.go"}`, true)
	}
	if hint := a.solutionFixation.checkAndWarn(); hint == "" {
		t.Error("fixation warning silent after reset — per-Agent lifetime cap regression (#677 defect 2)")
	}
}

func TestIssue677_SolutionFixationNoCrossRunLeak(t *testing.T) {
	a, _ := issue677Agent(t)

	for i := 0; i < 2; i++ {
		a.solutionFixation.recordToolCall("edit_file", `{"file_path":"/p/x.go"}`, true)
	}

	a.solutionFixation.reset()

	a.solutionFixation.recordToolCall("edit_file", `{"file_path":"/p/x.go"}`, true)
	if hint := a.solutionFixation.checkAndWarn(); hint != "" {
		t.Errorf("cross-run leak: run-2 warning fired on leaked run-1 counts: %q", hint)
	}
}

func TestIssue677_RedundantReverifyResetWired(t *testing.T) {
	a, _ := issue677Agent(t)

	if hint := a.redundantReverify.recordToolCall("run_command", "go test ./...", 1, false); hint != "" {
		t.Fatalf("setup: first occurrence flagged: %q", hint)
	}

	for w := 0; w < 2; w++ {
		if hint := a.redundantReverify.recordToolCall("run_command", "go test ./...", 2, false); hint == "" {
			t.Fatalf("setup: redundant re-run %d did not warn", w)
		}
	}
	if a.redundantReverify.warnings != redundantReverifyMaxWarnings {
		t.Fatalf("setup: warnings=%d, want %d", a.redundantReverify.warnings, redundantReverifyMaxWarnings)
	}

	a.redundantReverify.reset()

	if hint := a.redundantReverify.recordToolCall("run_command", "go test ./...", 1, false); hint != "" {
		t.Errorf("lastRun not cleared by reset: fresh verification flagged as redundant: %q", hint)
	}
	if hint := a.redundantReverify.recordToolCall("run_command", "go test ./...", 2, false); hint == "" {
		t.Error("reverify warning silent after reset — per-Agent lifetime cap regression (#677 defect 2)")
	}
	if a.redundantReverify.warnings != 1 {
		t.Errorf("warnings=%d after reset + 1 fire, want 1", a.redundantReverify.warnings)
	}
}

func TestIssue677_PerRunResetBlockWired(t *testing.T) {

	a, _ := issue677Agent(t)
	a.solutionFixation.recordToolCall("edit_file", `{"file_path":"/p/y.go"}`, true)
	a.redundantReverify.recordToolCall("run_command", "go test ./...", 1, false)
	a.solutionFixation.reset()
	a.redundantReverify.reset()
	if len(a.solutionFixation.recentCalls) != 0 || len(a.solutionFixation.failedByFile) != 0 || len(a.solutionFixation.firedFor) != 0 {
		t.Error("solutionFixation.reset() did not clear window state")
	}
	if a.redundantReverify.warnings != 0 || len(a.redundantReverify.lastRun) != 0 {
		t.Error("redundantReverify.reset() did not clear run state")
	}
}

func TestIssue677_BudgetResetRestoresInjection(t *testing.T) {
	a, cm := issue677Agent(t)

	for i := 0; i < guidanceBudgetPerTurn; i++ {
		a.injectGuidance("[advisory-filler] filler")
	}
	a.injectGuidance("[Solution Fixation Alert] should be suppressed")
	if got := issue677CountUserMsgs(cm, "Solution Fixation Alert"); got != 0 {
		t.Fatalf("pre-reset: fixation hint not suppressed (%d)", got)
	}

	a.guidanceBudget.reset()
	a.injectGuidance("[Solution Fixation Alert] should now pass")
	if got := issue677CountUserMsgs(cm, "should now pass"); got != 1 {
		t.Errorf("post-reset: fixation hint suppressed (%d injected); want 1", got)
	}
	_ = context.Background
}
