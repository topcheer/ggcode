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
			tr := BestOfNTool{Manager: mgr, Run: func(context.Context, string, int, []string, string, string) string { return "x" }}
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
	var gotTask string
	var gotN int
	var gotIso, gotName string
	tr := BestOfNTool{Manager: mgr, Run: func(_ context.Context, task string, n int, _ []string, iso, name string) string {
		gotTask, gotN, gotIso, gotName = task, n, iso, name
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
	if gotTask != "do the thing" || gotN != 3 || gotIso != "" || gotName != "lbl" {
		t.Fatalf("passthrough mismatch: task=%q n=%d iso=%q name=%q", gotTask, gotN, gotIso, gotName)
	}
}

func TestBestOfNTool_CloneKeepsWiring(t *testing.T) {
	mgr := subagent.NewManager(config.SubAgentConfig{})
	run := func(context.Context, string, int, []string, string, string) string { return "x" }
	c := BestOfNTool{Manager: mgr, Run: run}.Clone()
	if _, ok := c.(BestOfNTool); !ok {
		t.Fatalf("clone must stay a BestOfNTool, got %T", c)
	}
	bn := c.(BestOfNTool)
	if bn.Manager != mgr || bn.Run == nil {
		t.Fatal("clone must keep Manager and the injected runner")
	}
}
