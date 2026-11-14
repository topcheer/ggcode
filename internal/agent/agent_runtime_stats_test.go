package agent

import (
	"strings"
	"testing"

	"github.com/topcheer/ggcode/internal/provider"
	"github.com/topcheer/ggcode/internal/tool"
)

// #5635 P2: the status-face truth source must accumulate iteration and
// token counters across a run and expose a clamped context percentage.
func TestRuntimeStats_AccumulatesAcrossRun(t *testing.T) {
	a := NewAgent(&mockProvider{tokenCount: 7}, tool.NewRegistry(), "", 1)
	a.AddMessage(provider.Message{
		Role:    "user",
		Content: []provider.ContentBlock{{Type: "text", Text: "test"}},
	})

	// Simulate two conversation-loop iterations of counter updates.
	a.runtimeStats.beginRun()
	a.runtimeStats.setIteration(1)
	a.runtimeStats.addTokens(100, 50)
	a.runtimeStats.setIteration(2)
	a.runtimeStats.addTokens(30, 10)

	snap := a.RuntimeStats()
	if snap.Iterations != 2 {
		t.Errorf("Iterations = %d, want 2", snap.Iterations)
	}
	if snap.TokensIn != 130 {
		t.Errorf("TokensIn = %d, want 130", snap.TokensIn)
	}
	if snap.TokensOut != 60 {
		t.Errorf("TokensOut = %d, want 60", snap.TokensOut)
	}
	// One message counted by the mock provider: 7 tokens against the
	// default 128k window budget.
	if snap.ContextPct <= 0 || snap.ContextPct > 100 {
		t.Errorf("ContextPct = %v, want in (0,100]", snap.ContextPct)
	}

	// beginRun must reset so the face always describes the live run.
	a.runtimeStats.beginRun()
	snap = a.RuntimeStats()
	if snap.Iterations != 0 || snap.TokensIn != 0 || snap.TokensOut != 0 {
		t.Errorf("after beginRun: %+v, want zeroed counters", snap)
	}
}

func TestRuntimeStats_ContextPctClampedTo100(t *testing.T) {
	// Token count (7000, mock-counted per message) far above the prompt
	// budget floor: usablePromptBudgetLocked never drops below
	// minSummaryReserve (64), and the window is clamped small so the raw
	// ratio would exceed 100% - the face must clamp.
	a := NewAgent(&mockProvider{tokenCount: 7000}, tool.NewRegistry(), "", 1)
	a.AddMessage(provider.Message{
		Role:    "user",
		Content: []provider.ContentBlock{{Type: "text", Text: "test"}},
	})
	a.ContextManager().SetContextWindow(5)
	if snap := a.RuntimeStats(); snap.ContextPct != 100 {
		t.Errorf("ContextPct = %v, want 100 (clamped)", snap.ContextPct)
	}
}

// #5635 P2: the inflight-face truth source must expose the main
// agent's current tool while a batch runs and clear it afterwards.
func TestCurrentTool_BeginAndClear(t *testing.T) {
	a := NewAgent(&mockProvider{}, tool.NewRegistry(), "", 1)

	if ct := a.CurrentTool(); ct.Tool != "" {
		t.Fatalf("fresh agent CurrentTool = %q, want empty", ct.Tool)
	}

	a.currentTool.begin("read_file", `{"path":"/tmp/x.go"}`)
	ct := a.CurrentTool()
	if ct.Tool != "read_file" {
		t.Errorf("Tool = %q, want read_file", ct.Tool)
	}
	if ct.ParamsHead != `{"path":"/tmp/x.go"}` {
		t.Errorf("ParamsHead = %q, want the full short args", ct.ParamsHead)
	}
	if ct.StartedAt.IsZero() {
		t.Error("StartedAt must be set while the tool is in flight")
	}

	a.currentTool.clear()
	if ct := a.CurrentTool(); ct.Tool != "" || ct.ParamsHead != "" || !ct.StartedAt.IsZero() {
		t.Errorf("after clear: %+v, want zeroed snapshot", ct)
	}
}

func TestCurrentTool_ParamsHeadTruncated(t *testing.T) {
	a := NewAgent(&mockProvider{}, tool.NewRegistry(), "", 1)
	long := `{"pattern":"` + strings.Repeat("x", 500) + `"}`
	a.currentTool.begin("grep", long)
	if got := a.CurrentTool().ParamsHead; len(got) != 120 {
		t.Errorf("ParamsHead len = %d, want 120", len(got))
	}
}
