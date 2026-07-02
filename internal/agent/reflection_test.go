package agent

import (
	"encoding/json"
	"errors"
	"testing"
	"time"
)

func TestRunStatsRecordToolCall(t *testing.T) {
	s := newRunStats("test prompt")
	s.recordToolCall("read_file")
	s.recordToolCall("read_file")
	s.recordToolCall("write_file")

	if s.ToolCalls["read_file"] != 2 {
		t.Errorf("expected read_file count 2, got %d", s.ToolCalls["read_file"])
	}
	if s.ToolCalls["write_file"] != 1 {
		t.Errorf("expected write_file count 1, got %d", s.ToolCalls["write_file"])
	}
}

func TestRunStatsRecordFileEdit(t *testing.T) {
	s := newRunStats("test")
	s.recordFileEdit("/path/to/file.go")
	s.recordFileEdit("/path/to/other.go")
	s.recordFileEdit("/path/to/file.go") // duplicate

	if len(s.FilesEdited) != 2 {
		t.Fatalf("expected 2 files, got %d", len(s.FilesEdited))
	}
}

func TestRunStatsRecordCommand(t *testing.T) {
	s := newRunStats("test")
	s.recordCommand("go build ./...")
	s.recordCommand("go test ./...")

	if len(s.CommandsRun) != 2 {
		t.Fatalf("expected 2 commands, got %d", len(s.CommandsRun))
	}
}

func TestRunStatsRecordToolErrorMaxEntries(t *testing.T) {
	s := newRunStats("test")
	for i := 0; i < 15; i++ {
		s.recordToolError("run_command", "error")
	}
	if len(s.Errors) != 10 {
		t.Errorf("expected max 10 errors, got %d", len(s.Errors))
	}
}

func TestRunStatsFinalize(t *testing.T) {
	s := newRunStats("test")
	s.Iterations = 5
	time.Sleep(10 * time.Millisecond)
	s.finalize(nil)

	if !s.Success {
		t.Error("expected success=true for nil error")
	}
	if s.Duration <= 0 {
		t.Error("expected positive duration")
	}
}

func TestRunStatsFinalizeWithError(t *testing.T) {
	s := newRunStats("test")
	s.finalize(errors.New("mock error"))

	if s.Success {
		t.Error("expected success=false for error")
	}
	// Agent loop errors are NOT recorded — only tool errors are collected.
	if len(s.Errors) != 0 {
		t.Errorf("expected 0 errors (agent loop errors not recorded), got %d", len(s.Errors))
	}
}

func TestExtractPathsFromToolCall(t *testing.T) {
	s := newRunStats("test")

	args, _ := json.Marshal(map[string]string{"path": "/src/main.go"})
	extractPathsFromToolCall("write_file", args, s)

	args, _ = json.Marshal(map[string]string{"file_path": "/src/util.go"})
	extractPathsFromToolCall("edit_file", args, s)

	args, _ = json.Marshal(map[string]string{"command": "go build ./..."})
	extractPathsFromToolCall("run_command", args, s)

	if len(s.FilesEdited) != 2 {
		t.Errorf("expected 2 files, got %d: %v", len(s.FilesEdited), s.FilesEdited)
	}
	if len(s.CommandsRun) != 1 {
		t.Errorf("expected 1 command, got %d", len(s.CommandsRun))
	}
}

func TestGenerateInsightsEmpty(t *testing.T) {
	stats := RunStats{
		ToolCalls: map[string]int{},
	}
	result := GenerateInsights(stats)
	if result != "" {
		t.Errorf("expected empty string for trivial stats, got: %s", result)
	}
}

func TestGenerateInsightsWithTools(t *testing.T) {
	stats := RunStats{
		ToolCalls: map[string]int{
			"read_file":   3,
			"write_file":  1,
			"run_command": 2,
		},
		FilesEdited: []string{"/src/main.go", "/src/util.go"},
		CommandsRun: []string{"go build -tags goolm ./..."},
		Errors:      []string{"build failed"},
		Iterations:  5,
		Success:     true,
		Duration:    30 * time.Second,
		UserPrompt:  "fix the bug",
	}

	result := GenerateInsights(stats)
	if result == "" {
		t.Fatal("expected non-empty insights")
	}

	checks := []string{
		"Run Reflection",
		"completed",
		"fix the bug",
		"read_file",
		"write_file",
		"/src/main.go",
		"build failed",
	}
	for _, check := range checks {
		if !contains(result, check) {
			t.Errorf("insights missing %q:\n%s", check, result)
		}
	}
}

func TestGenerateInsightsBuildCommands(t *testing.T) {
	stats := RunStats{
		ToolCalls: map[string]int{"run_command": 1},
		CommandsRun: []string{
			"go build -tags goolm ./cmd/ggcode",
			"echo hello",
			"go test ./internal/agent/...",
		},
	}

	result := GenerateInsights(stats)
	if !contains(result, "Build/test commands used") {
		t.Errorf("expected build commands section:\n%s", result)
	}
	if contains(result, "echo hello") {
		t.Errorf("echo should not be in build commands:\n%s", result)
	}
}

func TestStripCommandComment(t *testing.T) {
	result := stripCommandComment("# Run tests\ngo test ./...")
	if result != "go test ./..." {
		t.Errorf("expected 'go test ./...', got %q", result)
	}
}

func TestSetReflectionFunc(t *testing.T) {
	a := &Agent{}
	var called bool
	var receivedStats RunStats

	a.SetReflectionFunc(func(stats RunStats) {
		called = true
		receivedStats = stats
	})

	stats := &RunStats{
		ToolCalls:  map[string]int{"read_file": 1},
		Iterations: 3,
		Success:    true,
		startTime:  time.Now(),
	}
	stats.finalize(nil)
	a.maybeReflect(stats)

	time.Sleep(50 * time.Millisecond)

	if !called {
		t.Error("reflection function was not called")
	}
	if receivedStats.Iterations != 3 {
		t.Errorf("expected 3 iterations, got %d", receivedStats.Iterations)
	}
}

func TestSetReflectionFuncNil(t *testing.T) {
	a := &Agent{}
	a.SetReflectionFunc(nil)
	stats := &RunStats{
		ToolCalls: map[string]int{},
		startTime: time.Now(),
	}
	a.maybeReflect(stats) // should not panic
}

func TestMaybeReflectNilStats(t *testing.T) {
	a := &Agent{}
	a.SetReflectionFunc(func(stats RunStats) {
		t.Error("should not be called with nil stats")
	})
	a.maybeReflect(nil)
}

func contains(s, substr string) bool {
	return len(s) >= len(substr) && (s == substr || len(s) > 0 && containsStr(s, substr))
}

func containsStr(s, substr string) bool {
	for i := 0; i <= len(s)-len(substr); i++ {
		if s[i:i+len(substr)] == substr {
			return true
		}
	}
	return false
}
