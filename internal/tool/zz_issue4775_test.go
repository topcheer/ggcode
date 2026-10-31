package tool

import (
	"context"
	"encoding/json"
	"strings"
	"testing"

	"github.com/topcheer/ggcode/internal/subagent"
)

// #4775 (S-03): create_namedagent's tools allowlist is validated against
// the session registry at spawn (use_namedagent) time. A declared-but-
// unavailable tool (lanchat declared where the registry never registered
// it) previously spawned anyway and the sub-agent only discovered the
// absence mid-run ("no lanchat in this session") - now it fails loudly
// before Spawn, mirroring the #551-C model gate.
func TestIssue4775_NamedAgentToolAllowlistValidated(t *testing.T) {
	mgr, tool := newNamedAgentTestEnv(t, clonableFakeProvider{}, nil)

	// Template declares a tool the registry does not have.
	if err := tool.Store.Save(subagent.NamedAgentTemplate{
		Name:  "sa-1",
		Tools: []string{"lanchat"},
	}); err != nil {
		t.Fatalf("Save template: %v", err)
	}

	res, err := tool.Execute(context.Background(), json.RawMessage(`{"name":"sa-1","task":"probe"}`))
	if err != nil {
		t.Fatalf("Execute error: %v", err)
	}
	if !res.IsError {
		t.Fatalf("declared-but-missing tool must fail loudly, got: %s", res.Content)
	}
	for _, want := range []string{"lanchat", "not available in this session's toolset", "create_namedagent"} {
		if !strings.Contains(res.Content, want) {
			t.Errorf("error message missing %q: %s", want, res.Content)
		}
	}
	// Rejected before Spawn: no zombie sub-agent entry (same invariant as #551-C).
	if agents := mgr.List(); len(agents) != 0 {
		t.Fatalf("rejected allowlist left %d zombie sub-agent entries", len(agents))
	}

	// An empty allowlist (all tools) keeps passing the gate.
	if err := tool.Store.Save(subagent.NamedAgentTemplate{
		Name: "sa-open",
	}); err != nil {
		t.Fatalf("Save template: %v", err)
	}
	res2, err2 := tool.Execute(context.Background(), json.RawMessage(`{"name":"sa-open","task":"probe"}`))
	if err2 != nil {
		t.Fatalf("Execute error: %v", err2)
	}
	if res2.IsError {
		t.Fatalf("empty allowlist must not be rejected: %s", res2.Content)
	}
}
