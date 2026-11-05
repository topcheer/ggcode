package tool

import (
	"context"
	"encoding/json"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/topcheer/ggcode/internal/config"
	"github.com/topcheer/ggcode/internal/provider"
	"github.com/topcheer/ggcode/internal/subagent"
)

func TestSpawnAgentInvalidInput(t *testing.T) {
	s := SpawnAgentTool{}
	result, err := s.Execute(context.Background(), json.RawMessage(`bad json`))
	if err != nil {
		t.Fatal(err)
	}
	if !result.IsError {
		t.Error("expected error for invalid input")
	}
}

func TestSpawnAgentMissingTask(t *testing.T) {
	s := SpawnAgentTool{}
	input, _ := json.Marshal(map[string]string{"task": ""})
	result, err := s.Execute(context.Background(), input)
	if err != nil {
		t.Fatal(err)
	}
	if !result.IsError {
		t.Error("expected error for empty task")
	}
}

func TestSpawnAgentWithManager(t *testing.T) {
	mgr := subagent.NewManager(config.SubAgentConfig{
		MaxConcurrent: 2,
		Timeout:       5 * time.Second,
	})
	defer mgr.Shutdown()

	s := SpawnAgentTool{
		Manager:  mgr,
		Provider: nil,
	}

	input, _ := json.Marshal(map[string]interface{}{
		"task":    "test task",
		"context": "additional context",
	})
	result, err := s.Execute(context.Background(), input)
	if err != nil {
		t.Fatal(err)
	}
	if result.IsError {
		t.Fatalf("unexpected error: %s", result.Content)
	}
	if !containsStr(result.Content, "Sub-agent spawned") {
		t.Errorf("expected spawn message: %s", result.Content)
	}

	time.Sleep(100 * time.Millisecond)
}

func TestSpawnAgentWithModel(t *testing.T) {
	mgr := subagent.NewManager(config.SubAgentConfig{
		MaxConcurrent: 2,
		Timeout:       5 * time.Second,
	})
	defer mgr.Shutdown()

	s := SpawnAgentTool{
		Manager:  mgr,
		Provider: nil,
	}

	input, _ := json.Marshal(map[string]interface{}{
		"task":  "test task with model",
		"model": "sonnet",
	})
	result, err := s.Execute(context.Background(), input)
	if err != nil {
		t.Fatal(err)
	}
	if result.IsError {
		t.Fatalf("unexpected error: %s", result.Content)
	}
	if !containsStr(result.Content, "Sub-agent spawned") {
		t.Errorf("expected spawn message: %s", result.Content)
	}
}

func TestSpawnAgentWithSubagentType(t *testing.T) {
	mgr := subagent.NewManager(config.SubAgentConfig{
		MaxConcurrent: 2,
		Timeout:       5 * time.Second,
	})
	defer mgr.Shutdown()

	s := SpawnAgentTool{
		Manager:  mgr,
		Provider: nil,
	}

	input, _ := json.Marshal(map[string]interface{}{
		"task":          "explore task",
		"subagent_type": "Explore",
	})
	result, err := s.Execute(context.Background(), input)
	if err != nil {
		t.Fatal(err)
	}
	if result.IsError {
		t.Fatalf("unexpected error: %s", result.Content)
	}
}

func TestSpawnAgentWithAllowedTools(t *testing.T) {
	mgr := subagent.NewManager(config.SubAgentConfig{
		MaxConcurrent: 2,
		Timeout:       5 * time.Second,
	})
	defer mgr.Shutdown()

	reg := NewRegistry()
	reg.Register(ReadFile{})
	reg.Register(WriteFile{})

	s := SpawnAgentTool{
		Manager:  mgr,
		Provider: nil,
		Tools:    reg,
	}

	input, _ := json.Marshal(map[string]interface{}{
		"task":  "limited tools task",
		"tools": []string{"read_file"},
	})
	result, err := s.Execute(context.Background(), input)
	if err != nil {
		t.Fatal(err)
	}
	if result.IsError {
		t.Fatalf("unexpected error: %s", result.Content)
	}

	time.Sleep(100 * time.Millisecond)
}

func TestSpawnAgentRejectsUnknownTools(t *testing.T) {
	mgr := subagent.NewManager(config.SubAgentConfig{
		MaxConcurrent: 2,
		Timeout:       5 * time.Second,
	})
	defer mgr.Shutdown()

	reg := NewRegistry()
	reg.Register(ReadFile{})
	reg.Register(WriteFile{})

	s := SpawnAgentTool{
		Manager:  mgr,
		Provider: nil,
		Tools:    reg,
	}

	input, _ := json.Marshal(map[string]interface{}{
		"task":  "test task",
		"tools": []string{"read_file", "nonexistent_tool"},
	})
	result, err := s.Execute(context.Background(), input)
	if err != nil {
		t.Fatal(err)
	}
	if !result.IsError {
		t.Fatal("expected error for unknown tool name")
	}
	if !strings.Contains(result.Content, "nonexistent_tool") {
		t.Errorf("expected 'nonexistent_tool' in error message, got: %s", result.Content)
	}

	input2, _ := json.Marshal(map[string]interface{}{
		"task":  "test task",
		"tools": []string{"read_file", "write_file"},
	})
	result2, err := s.Execute(context.Background(), input2)
	if err != nil {
		t.Fatal(err)
	}
	if result2.IsError {
		t.Fatalf("expected success for valid tools, got: %s", result2.Content)
	}

	time.Sleep(100 * time.Millisecond)
}

func TestSpawnAgentDescriptionDocumentsLimit(t *testing.T) {
	desc := SpawnAgentTool{}.Description()
	if !strings.Contains(desc, "16 sub-agents") {
		t.Errorf("spawn_agent description must document the 16-concurrent limit, got: %s", desc)
	}

	if !strings.Contains(desc, "context-copy token cost") {
		t.Errorf("spawn_agent description must document the delegation cost, got: %s", desc)
	}
	if !strings.Contains(desc, "protect your main context") {
		t.Errorf("spawn_agent description must document the delegation benefit (context protection), got: %s", desc)
	}
}

func TestSpawnAgentTaskParameterDocumentsContract(t *testing.T) {
	params := string(SpawnAgentTool{}.Parameters())
	if !strings.Contains(params, "acceptance criteria") {
		t.Fatalf("spawn_agent task parameter must guide a task contract (acceptance criteria), got: %s", params)
	}
	desc := WaitAgentTool{}.Description()
	if !strings.Contains(desc, "acceptance criteria") {
		t.Fatalf("wait_agent description must remind acceptance-criteria verification, got: %s", desc)
	}
}

func TestSpawnAgentLaunchDirectCompanion(t *testing.T) {
	mgr := subagent.NewManager(config.SubAgentConfig{
		MaxConcurrent: 2,
		Timeout:       5 * time.Second,
	})
	defer mgr.Shutdown()

	s := SpawnAgentTool{Manager: mgr, Provider: nil}
	id, worktree, err := s.Launch(context.Background(), LaunchOptions{
		Task:      "launch companion task",
		Name:      "direct launch",
		Isolation: "",
	})
	if err != nil {
		t.Fatalf("Launch error: %v", err)
	}
	if id == "" {
		t.Fatal("Launch must return a non-empty sub-agent id")
	}
	if worktree != "" {
		t.Fatalf("shared-cwd launch must not report a worktree, got %q", worktree)
	}
	if _, ok := mgr.Snapshot(id); !ok {
		t.Fatal("launched run must be visible via mgr.Snapshot(id)")
	}
}

func TestSpawnAgentMaxToolCallsAccepted(t *testing.T) {
	mgr := subagent.NewManager(config.SubAgentConfig{
		MaxConcurrent: 2,
		Timeout:       5 * time.Second,
	})
	defer mgr.Shutdown()

	s := SpawnAgentTool{Manager: mgr, Provider: nil}

	input, _ := json.Marshal(map[string]interface{}{
		"task":           "test task with budget",
		"max_tool_calls": 12,
	})
	result, err := s.Execute(context.Background(), input)
	if err != nil {
		t.Fatal(err)
	}
	if result.IsError {
		t.Fatalf("unexpected error: %s", result.Content)
	}
	if !containsStr(result.Content, "Sub-agent spawned") {
		t.Errorf("expected spawn message: %s", result.Content)
	}
}

// #5013 same-day recurrence (sa-16): a spawn that omitted max_tool_calls ran
// unbounded for 33 minutes / 290 tool calls until manual cancel. These tests
// pin the new contract: omitted budget => conservative default (bounded),
// explicit budget => respected verbatim.

func TestNormalizeMaxToolCallsDefaults(t *testing.T) {
	cases := []struct {
		in            int
		wantEffective int
		wantDefaulted bool
	}{
		{0, DefaultSubAgentMaxToolCalls, true},
		{-7, DefaultSubAgentMaxToolCalls, true},
		{1, 1, false},
		{42, 42, false},
	}
	for _, c := range cases {
		eff, defaulted := normalizeMaxToolCalls(c.in)
		if eff != c.wantEffective || defaulted != c.wantDefaulted {
			t.Errorf("normalizeMaxToolCalls(%d) = (%d, %v), want (%d, %v)",
				c.in, eff, defaulted, c.wantEffective, c.wantDefaulted)
		}
	}
}

func TestSpawnAgentDefaultBudgetBounded(t *testing.T) {
	mgr := subagent.NewManager(config.SubAgentConfig{
		MaxConcurrent: 2,
		Timeout:       5 * time.Second,
	})
	defer mgr.Shutdown()

	s := SpawnAgentTool{Manager: mgr, Provider: nil}

	// Omitted max_tool_calls: spawn result must disclose the conservative
	// default so the caller knows the run is bounded.
	input, _ := json.Marshal(map[string]interface{}{"task": "default budget task"})
	result, err := s.Execute(context.Background(), input)
	if err != nil {
		t.Fatal(err)
	}
	if result.IsError {
		t.Fatalf("unexpected error: %s", result.Content)
	}
	if !containsStr(result.Content, "conservative default") {
		t.Errorf("spawn without max_tool_calls must report the conservative default budget, got: %s", result.Content)
	}
	if !containsStr(result.Content, "Budget: 60 LLM turns AND 60 tool calls") {
		t.Errorf("spawn without max_tool_calls must report Budget: 60 LLM turns AND 60 tool calls, got: %s", result.Content)
	}
}

func TestSpawnAgentExplicitBudgetRespected(t *testing.T) {
	mgr := subagent.NewManager(config.SubAgentConfig{
		MaxConcurrent: 2,
		Timeout:       5 * time.Second,
	})
	defer mgr.Shutdown()

	s := SpawnAgentTool{Manager: mgr, Provider: nil}

	input, _ := json.Marshal(map[string]interface{}{
		"task":           "explicit budget task",
		"max_tool_calls": 12,
	})
	result, err := s.Execute(context.Background(), input)
	if err != nil {
		t.Fatal(err)
	}
	if result.IsError {
		t.Fatalf("unexpected error: %s", result.Content)
	}
	if !containsStr(result.Content, "Budget: 12 LLM turns AND 12 tool calls") {
		t.Errorf("explicit max_tool_calls=12 must be respected verbatim, got: %s", result.Content)
	}
	if containsStr(result.Content, "conservative default") {
		t.Errorf("explicit budget must not be reported as defaulted, got: %s", result.Content)
	}
}

type pinnedBudgetRunner struct{}

func (r *pinnedBudgetRunner) RunStream(ctx context.Context, prompt string, onEvent func(provider.StreamEvent), origin ...string) error {
	onEvent(provider.StreamEvent{Type: provider.StreamEventText, Text: "done"})
	onEvent(provider.StreamEvent{Type: provider.StreamEventDone})
	return nil
}

// Launch is the defense-in-depth layer: even a direct Launch caller that
// leaves MaxToolCalls at 0 must not produce an unbounded run. The normalized
// default must reach the AgentFactory maxTurns argument.
func TestSpawnAgentLaunchDefaultBudgetReachesFactory(t *testing.T) {
	mgr := subagent.NewManager(config.SubAgentConfig{
		MaxConcurrent: 2,
		Timeout:       5 * time.Second,
	})
	defer mgr.Shutdown()

	var mu sync.Mutex
	captured := []int{}
	reg := NewRegistry()
	reg.Register(ReadFile{})
	reg.Register(WriteFile{})
	s := SpawnAgentTool{
		Manager:  mgr,
		Provider: nil,
		Tools:    reg,
		AgentFactory: func(prov provider.Provider, tools interface{}, systemPrompt string, maxTurns int) subagent.AgentRunner {
			mu.Lock()
			captured = append(captured, maxTurns)
			mu.Unlock()
			return &pinnedBudgetRunner{}
		},
	}

	if _, _, err := s.Launch(context.Background(), LaunchOptions{
		Task: "launch default budget pin",
		Name: "default budget launch",
	}); err != nil {
		t.Fatalf("Launch error: %v", err)
	}

	deadline := time.Now().Add(3 * time.Second)
	for {
		mu.Lock()
		n := len(captured)
		var got int
		if n > 0 {
			got = captured[0]
		}
		mu.Unlock()
		if n > 0 {
			if got != DefaultSubAgentMaxToolCalls {
				t.Fatalf("AgentFactory maxTurns = %d, want %d (Launch must normalize 0 to the conservative default, #5013 recurrence)", got, DefaultSubAgentMaxToolCalls)
			}
			return
		}
		if time.Now().After(deadline) {
			t.Fatal("AgentFactory was not called within 3s of Launch")
		}
		time.Sleep(10 * time.Millisecond)
	}
}
