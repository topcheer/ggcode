package tool

import (
	"context"
	"encoding/json"
	"strings"
	"testing"
	"time"

	"github.com/topcheer/ggcode/internal/config"
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
		Provider: nil, // no provider — sub-agent will fail, but spawn itself should succeed
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

	// Give the goroutine a moment to start
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

	// Unknown tool name should be rejected
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

	// Valid tool names should be accepted
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
