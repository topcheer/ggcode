package tool

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"testing"

	"github.com/topcheer/ggcode/internal/config"
	"github.com/topcheer/ggcode/internal/subagent"
)

// Tests for the cascade escalation hint (subagent_cascade.go): when a
// sub-agent fails on an alternate model, the parent gets a one-shot hint
// to escalate to its own model before concluding task infeasibility.

func TestCascadeHint_FiresOnFailedAlternateModel(t *testing.T) {
	snap := subagent.Snapshot{
		ID:     "agent-1",
		Status: subagent.StatusFailed,
		Model:  "glm-4.5-air",
		Error:  "boom",
	}
	hint := cascadeEscalationHint(snap, "glm-5.3")
	if hint == "" {
		t.Fatal("expected hint for failed alternate-model run")
	}
	for _, want := range []string{"cascade escalation", "glm-4.5-air", "glm-5.3", "task infeasibility"} {
		if !strings.Contains(hint, want) {
			t.Fatalf("hint missing %q: %s", want, hint)
		}
	}
}

func TestCascadeHint_SkipsNonApplicable(t *testing.T) {
	cases := []struct {
		name string
		snap subagent.Snapshot
		par  string
	}{
		{"completed run", subagent.Snapshot{ID: "a", Status: subagent.StatusCompleted, Model: "glm-4.5-air"}, "glm-5.3"},
		{"cancelled run", subagent.Snapshot{ID: "a", Status: subagent.StatusCancelled, Model: "glm-4.5-air"}, "glm-5.3"},
		{"running run", subagent.Snapshot{ID: "a", Status: subagent.StatusRunning, Model: "glm-4.5-air"}, "glm-5.3"},
		{"same model", subagent.Snapshot{ID: "a", Status: subagent.StatusFailed, Model: "glm-5.3"}, "glm-5.3"},
		{"same model different case", subagent.Snapshot{ID: "a", Status: subagent.StatusFailed, Model: "GLM-5.3"}, "glm-5.3"},
		{"inherited model unknown", subagent.Snapshot{ID: "a", Status: subagent.StatusFailed}, "glm-5.3"},
		{"parent model unknown", subagent.Snapshot{ID: "a", Status: subagent.StatusFailed, Model: "glm-4.5-air"}, ""},
	}
	for _, tc := range cases {
		if got := cascadeEscalationHint(tc.snap, tc.par); got != "" {
			t.Fatalf("%s: expected no hint, got %s", tc.name, got)
		}
	}
}

func TestCascadeHintTracker_Dedup(t *testing.T) {
	tr := NewCascadeHintTracker()
	if !tr.Mark("a") {
		t.Fatal("first Mark should return true")
	}
	if tr.Mark("a") {
		t.Fatal("second Mark for same id should return false")
	}
	if !tr.Mark("b") {
		t.Fatal("Mark for a different id should return true")
	}
}

func TestWaitAgentCascadeHint_OneShot(t *testing.T) {
	mgr := subagent.NewManager(config.SubAgentConfig{})
	id := mgr.Spawn("cheap", "do work", "do work", nil, context.Background())
	sa, _ := mgr.Get(id)
	sa.Model = "glm-4.5-air"
	mgr.Complete(id, "", errors.New("sub-agent exhausted its steps"))

	tracker := NewCascadeHintTracker()
	tool := WaitAgentTool{
		Manager:      mgr,
		ParentModel:  func() string { return "glm-5.3" },
		CascadeHints: tracker,
	}
	input, _ := json.Marshal(map[string]any{"agent_id": id, "wait_seconds": 1})

	res, err := tool.Execute(context.Background(), input)
	if err != nil || res.IsError {
		t.Fatalf("Execute failed: err=%v result=%+v", err, res)
	}
	if !strings.Contains(res.Content, "cascade escalation") {
		t.Fatalf("first wait should carry the escalation hint: %s", res.Content)
	}

	// Re-polling a terminal run must NOT repeat the hint (one-shot budget).
	res2, err := tool.Execute(context.Background(), input)
	if err != nil || res2.IsError {
		t.Fatalf("second Execute failed: err=%v result=%+v", err, res2)
	}
	if strings.Contains(res2.Content, "cascade escalation") {
		t.Fatalf("escalation hint must fire only once per run: %s", res2.Content)
	}
}

func TestWaitAgent_NoHintWithoutTracker(t *testing.T) {
	// Hints are opt-in wiring: a zero-value tool must behave exactly as before.
	mgr := subagent.NewManager(config.SubAgentConfig{})
	id := mgr.Spawn("cheap", "do work", "do work", nil, context.Background())
	sa, _ := mgr.Get(id)
	sa.Model = "glm-4.5-air"
	mgr.Complete(id, "", errors.New("boom"))

	tool := WaitAgentTool{Manager: mgr, ParentModel: func() string { return "glm-5.3" }}
	input, _ := json.Marshal(map[string]any{"agent_id": id, "wait_seconds": 1})
	res, err := tool.Execute(context.Background(), input)
	if err != nil || res.IsError {
		t.Fatalf("Execute failed: err=%v result=%+v", err, res)
	}
	if strings.Contains(res.Content, "cascade escalation") {
		t.Fatal("no hint expected without a tracker")
	}
}

func TestListAgentsCascadeHint_OneShot(t *testing.T) {
	mgr := subagent.NewManager(config.SubAgentConfig{})
	id := mgr.Spawn("cheap", "do work", "do work", nil, context.Background())
	sa, _ := mgr.Get(id)
	sa.Model = "glm-4.5-air"
	mgr.Complete(id, "", errors.New("boom"))

	tracker := NewCascadeHintTracker()
	tool := ListAgentsTool{
		Manager:      mgr,
		ParentModel:  func() string { return "glm-5.3" },
		CascadeHints: tracker,
	}
	res, err := tool.Execute(context.Background(), nil)
	if err != nil || res.IsError {
		t.Fatalf("Execute failed: err=%v result=%+v", err, res)
	}
	if !strings.Contains(res.Content, "cascade escalation") {
		t.Fatalf("list_agents should surface the escalation hint: %s", res.Content)
	}

	// Same tracker shared with wait_agent: the budget is already spent.
	wa := WaitAgentTool{
		Manager:      mgr,
		ParentModel:  func() string { return "glm-5.3" },
		CascadeHints: tracker,
	}
	input, _ := json.Marshal(map[string]any{"agent_id": id, "wait_seconds": 1})
	res2, err := wa.Execute(context.Background(), input)
	if err != nil || res2.IsError {
		t.Fatalf("wait Execute failed: err=%v result=%+v", err, res2)
	}
	if strings.Contains(res2.Content, "cascade escalation") {
		t.Fatal("hint budget must be shared across list_agents and wait_agent")
	}
}

func TestUseNamedAgent_InvocationModelOverride(t *testing.T) {
	// Template pins a stale/unknown model, but the invocation overrides it
	// with a whitelisted one: invocation must take precedence (#cascade).
	mgr, tool := newNamedAgentTestEnv(t, clonableFakeProvider{}, []string{"glm-5.3", "glm-5.2"})
	if err := tool.Store.Save(subagent.NamedAgentTemplate{
		Name:  "escalatable",
		Model: "nonexistent-model",
	}); err != nil {
		t.Fatalf("Save template: %v", err)
	}

	res, err := tool.Execute(context.Background(), json.RawMessage(
		`{"name":"escalatable","task":"do thing","model":"glm-5.3"}`))
	if err != nil {
		t.Fatalf("Execute error: %v", err)
	}
	if res.IsError {
		t.Fatalf("invocation model override should supersede the template model, got: %s", res.Content)
	}
	if agents := mgr.List(); len(agents) != 1 {
		t.Fatalf("expected 1 spawned agent, got %d", len(agents))
	}

	// Unknown invocation model is rejected by the same whitelist gate.
	res2, err := tool.Execute(context.Background(), json.RawMessage(
		`{"name":"escalatable","task":"do thing","model":"nonexistent-model"}`))
	if err != nil {
		t.Fatalf("Execute error: %v", err)
	}
	if !res2.IsError || !strings.Contains(res2.Content, "not available") {
		t.Fatalf("unknown invocation model must be rejected: %+v", res2)
	}
}

func TestUseNamedAgent_InvocationModelOverrideProviderCapability(t *testing.T) {
	// An invocation-level override must not be silently ignored when the
	// provider cannot clone itself with a model (#551-C semantics).
	_, tool := newNamedAgentTestEnv(t, fakeNamedAgentProvider{}, []string{"glm-5.3"})
	if err := tool.Store.Save(subagent.NamedAgentTemplate{Name: "plain"}); err != nil {
		t.Fatalf("Save template: %v", err)
	}

	res, err := tool.Execute(context.Background(), json.RawMessage(
		`{"name":"plain","task":"do thing","model":"glm-5.3"}`))
	if err != nil {
		t.Fatalf("Execute error: %v", err)
	}
	if !res.IsError || !strings.Contains(res.Content, "does not support model overrides") {
		t.Fatalf("invocation override must fail loudly on non-clonable provider: %+v", res)
	}
}
