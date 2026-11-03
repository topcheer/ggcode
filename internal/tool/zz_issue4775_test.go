package tool

import (
	"context"
	"encoding/json"
	"strings"
	"testing"

	"github.com/topcheer/ggcode/internal/subagent"
)

func TestIssue4775_NamedAgentToolAllowlistValidated(t *testing.T) {
	mgr, tool := newNamedAgentTestEnv(t, clonableFakeProvider{}, nil)

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

	if agents := mgr.List(); len(agents) != 0 {
		t.Fatalf("rejected allowlist left %d zombie sub-agent entries", len(agents))
	}

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
