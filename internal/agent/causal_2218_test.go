package agent

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/topcheer/ggcode/internal/provider"
)

func TestCausalCmdForGateDirectArgsWin(t *testing.T) {
	tc := provider.ToolCallDelta{Name: "run_command", Arguments: json.RawMessage(`{"command":"go test ./..."}`)}
	if got := causalCmdForGate(tc, "irrelevant"); got != "go test ./..." {
		t.Fatalf("direct args must win, got %q", got)
	}
}

func TestCausalCmdForGateJobHeaderFallback(t *testing.T) {

	tc := provider.ToolCallDelta{Name: "wait_command", Arguments: json.RawMessage(`{"job_id":"cmd-1"}`)}
	content := "Job ID: cmd-1\nCommand: grep -rn FAIL ./logs\nStatus: done\nDuration: 3s\n"
	if got := causalCmdForGate(tc, content); got != "grep -rn FAIL ./logs" {
		t.Fatalf("#2218-B: job header not picked up, got %q", got)
	}

	if !looksLikeReadCommand(causalCmdForGate(tc, content)) {
		t.Fatalf("grep via job channel must be classified as a read command")
	}
}

func TestCausalCmdForGateMissingHeaderStaysEmpty(t *testing.T) {
	tc := provider.ToolCallDelta{Name: "wait_command", Arguments: json.RawMessage(`{"job_id":"cmd-1"}`)}
	if got := causalCmdForGate(tc, "Job ID: cmd-1\nStatus: running\n"); got != "" {
		t.Fatalf("no header -> empty (old behavior), got %q", got)
	}
	if !strings.Contains("ok", "ok") {
		t.Fatal("unreachable")
	}
}
