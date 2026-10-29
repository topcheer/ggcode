package permission

import (
	"encoding/json"
	"testing"
)

// #4261: the two plan-mode floors lost in the two-mode rewrite are back.
func TestIssue4261_PlanFloorsRestored(t *testing.T) {
	p := NewConfigPolicyWithMode(nil, []string{"/tmp/ws"}, PlanMode)
	// #712 hole (a): sensitive out-of-sandbox read via file tool -> Deny.
	if d, _ := p.Check("read_file", json.RawMessage(`{"path":"/Users/x/.ssh/id_rsa"}`)); d != Deny {
		t.Fatalf("sensitive read in plan must Deny, got %v", d)
	}
	// #596-P2 hole: non-whitelisted MCP tool (write op) -> Deny.
	if d, _ := p.Check("mcp__github__create_or_update_file", json.RawMessage(`{}`)); d != Deny {
		t.Fatalf("non-read-only MCP tool in plan must Deny, got %v", d)
	}
	// Whitelisted read-only MCP tool still flows.
	if d, _ := p.Check("mcp__web_reader__webReader", json.RawMessage(`{"url":"https://example.com"}`)); d == Deny {
		t.Fatal("whitelisted read-only MCP tool must not be denied in plan")
	}
	// Ordinary in-sandbox read stays allowed (regression guard).
	if d, _ := p.Check("read_file", json.RawMessage(`{"path":"/tmp/ws/a.go"}`)); d != Allow {
		t.Fatalf("in-sandbox read in plan must Allow, got %v", d)
	}
}
