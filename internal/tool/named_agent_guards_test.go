package tool

// Nil-guard and validation coverage for named_agent.go and
// use_namedagent.go (sa-141). Reuses the isolated-store helpers from
// named_agent_test.go; no sub-agent is ever spawned.

import (
	"context"
	"encoding/json"
	"strings"
	"testing"

	"github.com/topcheer/ggcode/internal/config"
	"github.com/topcheer/ggcode/internal/subagent"
)

func TestNamedAgentNilStoreGuardsSa141(t *testing.T) {
	ctx := context.Background()
	cases := []struct {
		name  string
		tool  Tool
		input string
		want  string
	}{
		{"create", CreateNamedAgentTool{}, `{"name":"x","system_prompt":"p"}`, "create_namedagent: template store not available"},
		{"delete", DeleteNamedAgentTool{}, `{"name":"x"}`, "delete_namedagent: template store not available"},
		{"list", ListNamedAgentTool{}, `{}`, "list_namedagent: template store not available"},
		{"use", UseNamedAgentTool{}, `{"name":"x","task":"y"}`, "use_namedagent: not properly configured"},
	}
	for _, tc := range cases {
		r, err := tc.tool.Execute(ctx, json.RawMessage(tc.input))
		if err != nil || !r.IsError || r.Content != tc.want {
			t.Errorf("%s nil store -> (%+v,%v), want %q", tc.name, r, err, tc.want)
		}
	}
}

func TestNamedAgentValidationSa141(t *testing.T) {
	ctx := context.Background()
	store, _ := setupDeleteTestStore(t)

	// create: invalid JSON / missing name / missing system_prompt.
	create := CreateNamedAgentTool{Store: store}
	r, _ := create.Execute(ctx, json.RawMessage(`{bad`))
	if !r.IsError || !strings.Contains(r.Content, "invalid input") {
		t.Fatalf("create invalid JSON -> %+v", r)
	}
	r, _ = create.Execute(ctx, json.RawMessage(`{"system_prompt":"p"}`))
	if !r.IsError || r.Content != "name is required" {
		t.Fatalf("create missing name -> %+v", r)
	}
	r, _ = create.Execute(ctx, json.RawMessage(`{"name":"n"}`))
	if !r.IsError || r.Content != "system_prompt is required" {
		t.Fatalf("create missing prompt -> %+v", r)
	}

	// delete: invalid JSON / missing name / unknown template.
	deleteTool := DeleteNamedAgentTool{Store: store}
	r, _ = deleteTool.Execute(ctx, json.RawMessage(`{`))
	if !r.IsError || !strings.Contains(r.Content, "invalid input") {
		t.Fatalf("delete invalid JSON -> %+v", r)
	}
	r, _ = deleteTool.Execute(ctx, json.RawMessage(`{"name":"  "}`))
	if !r.IsError || r.Content != "name is required" {
		t.Fatalf("delete missing name -> %+v", r)
	}
	r, _ = deleteTool.Execute(ctx, json.RawMessage(`{"name":"ghost"}`))
	if !r.IsError || !strings.Contains(r.Content, "named subagent 'ghost' not found") {
		t.Fatalf("delete unknown -> %+v", r)
	}

	// use: validation + unknown-template suggestions.
	use := UseNamedAgentTool{Store: store, Manager: subagent.NewManager(config.SubAgentConfig{})}
	r, _ = use.Execute(ctx, json.RawMessage(`{bad`))
	if !r.IsError || !strings.Contains(r.Content, "invalid input") {
		t.Fatalf("use invalid JSON -> %+v", r)
	}
	r, _ = use.Execute(ctx, json.RawMessage(`{"task":"t"}`))
	if !r.IsError || r.Content != "name is required" {
		t.Fatalf("use missing name -> %+v", r)
	}
	r, _ = use.Execute(ctx, json.RawMessage(`{"name":"n"}`))
	if !r.IsError || r.Content != "task is required" {
		t.Fatalf("use missing task -> %+v", r)
	}
	r, _ = use.Execute(ctx, json.RawMessage(`{"name":"ghost","task":"t"}`))
	if !r.IsError || !strings.Contains(r.Content, "not found. Use create_namedagent") {
		t.Fatalf("use unknown (empty store) -> %+v", r)
	}

	createTestTemplate(t, store, "reviewer", "reviews code", "You review code")
	r, _ = use.Execute(ctx, json.RawMessage(`{"name":"ghost","task":"t"}`))
	if !r.IsError || !strings.Contains(r.Content, "Available: reviewer") {
		t.Fatalf("use unknown (with templates) -> %+v", r)
	}

	// Happy-path delete round trip (no sub-agent involved).
	r, _ = deleteTool.Execute(ctx, json.RawMessage(`{"name":"reviewer"}`))
	if r.IsError || !strings.Contains(r.Content, "deleted successfully") {
		t.Fatalf("delete existing -> %+v", r)
	}
	// Second delete now reports not-found.
	r, _ = deleteTool.Execute(ctx, json.RawMessage(`{"name":"reviewer"}`))
	if !r.IsError || !strings.Contains(r.Content, "not found") {
		t.Fatalf("double delete -> %+v", r)
	}
}
