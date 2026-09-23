package tool

// cancel_agent tool coverage (sa-141): manager-less and not-found paths are
// deterministic; spawning real sub-agents is intentionally avoided.

import (
	"context"
	"encoding/json"
	"strings"
	"testing"

	"github.com/topcheer/ggcode/internal/config"
	"github.com/topcheer/ggcode/internal/subagent"
)

func TestCancelAgentNilManagerSa141(t *testing.T) {
	tool := CancelAgentTool{}
	r, err := tool.Execute(context.Background(), json.RawMessage(`{"agent_id":"a1","description":"x"}`))
	if err != nil || !r.IsError || !strings.Contains(r.Content, "not available") {
		t.Fatalf("nil manager -> (%+v,%v)", r, err)
	}
}

func TestCancelAgentInvalidInputSa141(t *testing.T) {
	tool := CancelAgentTool{Manager: subagent.NewManager(config.SubAgentConfig{})}
	r, err := tool.Execute(context.Background(), json.RawMessage(`nope`))
	if err != nil || !r.IsError || !strings.Contains(r.Content, "invalid input") {
		t.Fatalf("invalid JSON -> (%+v,%v)", r, err)
	}
	r, err = tool.Execute(context.Background(), json.RawMessage(`{"description":"x"}`))
	if err != nil || !r.IsError || r.Content != "agent_id is required" {
		t.Fatalf("empty agent_id -> (%+v,%v)", r, err)
	}
}

func TestCancelAgentUnknownIDSa141(t *testing.T) {
	tool := CancelAgentTool{Manager: subagent.NewManager(config.SubAgentConfig{})}
	r, err := tool.Execute(context.Background(), json.RawMessage(`{"agent_id":"ghost","description":"x"}`))
	if err != nil || !r.IsError || !strings.Contains(r.Content, "agent not found: ghost") {
		t.Fatalf("unknown agent -> (%+v,%v)", r, err)
	}
}
