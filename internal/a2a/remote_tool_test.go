package a2a

import (
	"strings"
	"testing"
)

// #5340: identical instance sets in different discovery orders must render
// byte-identical text (prompt-cache stability for the Remote Agents section).
func TestFormatRemoteAgents_DeterministicOrder(t *testing.T) {
	inst := []InstanceInfo{
		{ID: "i-2", Workspace: "/w/charlie", Status: "online"},
		{ID: "i-1", Workspace: "/w/bravo", Status: "online"},
		{ID: "i-3", Workspace: "/w/alpha", Status: "online"},
	}
	meta := map[string]RemoteAgentMeta{
		"i-1": {Team: "ggcode", Role: "reviewer", Languages: []string{"go"}},
	}
	a := FormatRemoteAgents(inst, meta)
	b := FormatRemoteAgents([]InstanceInfo{inst[1], inst[2], inst[0]}, meta)
	if a == "" || a != b {
		t.Fatalf("rendering not order-stable:\n---\n%s\n---\n%s", a, b)
	}
	if i, j := strings.Index(a, "alpha"), strings.Index(a, "bravo"); !(i < j) {
		t.Fatalf("not name-sorted:\n%s", a)
	}
}
