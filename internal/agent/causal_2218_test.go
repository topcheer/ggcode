package agent

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/topcheer/ggcode/internal/provider"
)

// #2218 case B probes: the causal gate must see the job's real command.
func TestCausalCmdForGateDirectArgsWin(t *testing.T) {
	tc := provider.ToolCallDelta{Name: "run_command", Arguments: json.RawMessage(`{"command":"go test ./..."}`)}
	if got := causalCmdForGate(tc, "irrelevant"); got != "go test ./..." {
		t.Fatalf("direct args must win, got %q", got)
	}
}

func TestCausalCmdForGateJobHeaderFallback(t *testing.T) {
	// wait_command carries only job_id; the tool layer now emits a
	// "Command: " header in the snapshot.
	tc := provider.ToolCallDelta{Name: "wait_command", Arguments: json.RawMessage(`{"job_id":"cmd-1"}`)}
	content := "Job ID: cmd-1\nCommand: grep -rn FAIL ./logs\nStatus: done\nDuration: 3s\n"
	if got := causalCmdForGate(tc, content); got != "grep -rn FAIL ./logs" {
		t.Fatalf("#2218-B: job header not picked up, got %q", got)
	}
	// The read-command probe must then neutralize the gate for a
	// succeeded grep whose output carries FAIL-shaped lines.
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
