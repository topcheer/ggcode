package agent

import (
	"context"
	"encoding/json"
	"strings"
	"testing"

	"github.com/topcheer/ggcode/internal/tool"
)

type stubToolForHarness struct{}

func (stubToolForHarness) Name() string        { return "test_tool" }
func (stubToolForHarness) Description() string { return "stub" }
func (stubToolForHarness) Parameters() json.RawMessage {
	return json.RawMessage(`{"type":"object"}`)
}
func (stubToolForHarness) Execute(ctx context.Context, input json.RawMessage) (tool.Result, error) {
	return tool.Result{}, nil
}

func TestHarnessFingerprint_StableAndSensitive(t *testing.T) {
	a := &Agent{}
	a.baseSystemPrompt = "base prompt v1"

	fp1 := a.ComputeHarnessFingerprint()
	fp2 := a.ComputeHarnessFingerprint()
	if fp1.Sum() != fp2.Sum() {
		t.Fatalf("fingerprint unstable for unchanged harness: %s vs %s", fp1.Sum(), fp2.Sum())
	}
	if !strings.Contains(fp1.Sum(), "sp=") || !strings.Contains(fp1.Sum(), "checks=") || !strings.Contains(fp1.Sum(), "tools=") {
		t.Fatalf("Sum() missing components: %s", fp1.Sum())
	}

	a.baseSystemPrompt = "base prompt v2"
	if a.ComputeHarnessFingerprint().Sum() == fp1.Sum() {
		t.Fatal("fingerprint did not change after system prompt edit")
	}

	a.tools = tool.NewRegistry()
	if err := a.tools.Register(stubToolForHarness{}); err != nil {
		t.Fatalf("Register failed: %v", err)
	}
	fpTools := a.ComputeHarnessFingerprint()
	if fpTools.Sum() == fp1.Sum() {
		t.Fatal("fingerprint did not change after tool registration")
	}
	if fpTools.ToolsCount != 1 {
		t.Fatalf("ToolsCount = %d, want 1", fpTools.ToolsCount)
	}

	harnessToolNames(nil)
}

func TestHashNames_OrderInsensitive(t *testing.T) {
	h1, n1 := hashNames([]string{"b", "a", "c"})
	h2, n2 := hashNames([]string{"c", "a", "b"})
	if h1 != h2 || n1 != n2 {
		t.Fatalf("hashNames not order-insensitive: (%s,%d) vs (%s,%d)", h1, n1, h2, n2)
	}
	h3, n3 := hashNames([]string{"b", "a", "c", "d"})
	if h3 == h1 || n3 == n1 {
		t.Fatal("hashNames failed to distinguish different sets")
	}
}

func TestLogHarnessFingerprint_ChangeDetection(t *testing.T) {
	a := &Agent{}
	a.baseSystemPrompt = "p1"

	a.logHarnessFingerprint()
	first := a.harnessFPLast
	if first == "" {
		t.Fatal("harnessFPLast not populated after first log")
	}

	a.logHarnessFingerprint()
	if a.harnessFPLast != first {
		t.Fatal("unchanged harness altered fingerprint state")
	}

	a.baseSystemPrompt = "p2"
	a.logHarnessFingerprint()
	if a.harnessFPLast == first {
		t.Fatal("changed harness did not update fingerprint state")
	}
}
