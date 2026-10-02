package tool

import (
	"context"
	"encoding/json"
	"strings"
	"testing"

	"github.com/topcheer/ggcode/internal/config"
	"github.com/topcheer/ggcode/internal/subagent"
)

func TestBestOfNTool_Validation(t *testing.T) {
	mgr := subagent.NewManager(config.SubAgentConfig{})
	cases := []struct {
		name  string
		input string
		want  string
	}{
		{"missing task", `{"n":3,"description":"d"}`, "task is required"},
		{"bad isolation", `{"task":"t","isolation":"docker","description":"d"}`, "invalid isolation"},
		{"n too small", `{"task":"t","n":1,"description":"d"}`, "between 2 and 4"},
		{"n too big", `{"task":"t","n":5,"description":"d"}`, "between 2 and 4"},
		{"bad json", `{`, "invalid input"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			tr := BestOfNTool{Manager: mgr, Run: func(context.Context, BestOfNRequest) string { return "x" }}
			res, err := tr.Execute(context.Background(), json.RawMessage(tc.input))
			if err != nil {
				t.Fatalf("Execute must not return go errors: %v", err)
			}
			if !res.IsError || !strings.Contains(res.Content, tc.want) {
				t.Fatalf("expected error containing %q, got: %q", tc.want, res.Content)
			}
		})
	}
}

func TestBestOfNTool_Unwired(t *testing.T) {
	tr := BestOfNTool{Manager: subagent.NewManager(config.SubAgentConfig{})}
	res, _ := tr.Execute(context.Background(), json.RawMessage(`{"task":"t","description":"d"}`))
	if !res.IsError || !strings.Contains(res.Content, "orchestrator not wired") {
		t.Fatalf("expected unwired error, got %q", res.Content)
	}
}

func TestBestOfNTool_PassesThroughAndDefaults(t *testing.T) {
	mgr := subagent.NewManager(config.SubAgentConfig{})
	var got BestOfNRequest
	tr := BestOfNTool{Manager: mgr, Run: func(_ context.Context, req BestOfNRequest) string {
		got = req
		return "REPORT"
	}}
	res, err := tr.Execute(context.Background(), json.RawMessage(`{"task":"do the thing","description":"lbl"}`))
	if err != nil {
		t.Fatalf("Execute error: %v", err)
	}
	if res.IsError {
		t.Fatalf("unexpected error: %q", res.Content)
	}
	if res.Content != "REPORT" {
		t.Fatalf("shell must return the orchestrator report verbatim, got %q", res.Content)
	}
	if got.Task != "do the thing" || got.N != 3 || got.Isolation != "" || got.Name != "lbl" || len(got.Models) != 0 {
		t.Fatalf("passthrough mismatch: %+v", got)
	}
}

func TestBestOfNTool_CloneKeepsWiring(t *testing.T) {
	mgr := subagent.NewManager(config.SubAgentConfig{})
	run := func(context.Context, BestOfNRequest) string { return "x" }
	c := BestOfNTool{Manager: mgr, Run: run}.Clone()
	if _, ok := c.(BestOfNTool); !ok {
		t.Fatalf("clone must stay a BestOfNTool, got %T", c)
	}
	bn := c.(BestOfNTool)
	if bn.Manager != mgr || bn.Run == nil {
		t.Fatal("clone must keep Manager and the injected runner")
	}
}

// r380: per-candidate model list drives n and is validated against the
// current endpoint's available models.
func TestBestOfNTool_Models(t *testing.T) {
	mgr := subagent.NewManager(config.SubAgentConfig{})
	available := []string{"glm-5.3-flash", "glm-5.2", "glm-4.7"}

	t.Run("models set n and pass through", func(t *testing.T) {
		var got BestOfNRequest
		tr := BestOfNTool{Manager: mgr, AvailableModels: func() []string { return available },
			Run: func(_ context.Context, req BestOfNRequest) string { got = req; return "R" }}
		res, _ := tr.Execute(context.Background(), json.RawMessage(`{"task":"t","description":"d","models":["glm-5.3-flash","glm-5.2"]}`))
		if res.IsError {
			t.Fatalf("unexpected error: %q", res.Content)
		}
		if got.N != 2 || len(got.Models) != 2 || got.Models[0] != "glm-5.3-flash" || got.Models[1] != "glm-5.2" {
			t.Fatalf("models mismatch: %+v", got)
		}
	})

	t.Run("n conflict refused", func(t *testing.T) {
		tr := BestOfNTool{Manager: mgr, Run: func(context.Context, BestOfNRequest) string { return "x" }}
		res, _ := tr.Execute(context.Background(), json.RawMessage(`{"task":"t","description":"d","n":3,"models":["a","b"]}`))
		if !res.IsError || !strings.Contains(res.Content, "conflicts with models") {
			t.Fatalf("expected conflict error, got %q", res.Content)
		}
	})

	t.Run("too few models refused", func(t *testing.T) {
		tr := BestOfNTool{Manager: mgr, Run: func(context.Context, BestOfNRequest) string { return "x" }}
		res, _ := tr.Execute(context.Background(), json.RawMessage(`{"task":"t","description":"d","models":["a"]}`))
		if !res.IsError || !strings.Contains(res.Content, "2-4 per-candidate models") {
			t.Fatalf("expected size error, got %q", res.Content)
		}
	})

	t.Run("unavailable model refused", func(t *testing.T) {
		tr := BestOfNTool{Manager: mgr, AvailableModels: func() []string { return available },
			Run: func(context.Context, BestOfNRequest) string { return "x" }}
		res, _ := tr.Execute(context.Background(), json.RawMessage(`{"task":"t","description":"d","models":["glm-5.2","gpt-9"]}`))
		if !res.IsError || !strings.Contains(res.Content, "not available on the current endpoint") {
			t.Fatalf("expected availability error, got %q", res.Content)
		}
	})
}
