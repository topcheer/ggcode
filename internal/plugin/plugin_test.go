package plugin

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/topcheer/ggcode/internal/config"
	"github.com/topcheer/ggcode/internal/tool"
)

// mockPlugin is a test plugin.
type mockPlugin struct {
	name  string
	tools []tool.Tool
}

func (m *mockPlugin) Name() string                          { return m.name }
func (m *mockPlugin) Tools() []tool.Tool                    { return m.tools }
func (m *mockPlugin) Init(cfg map[string]interface{}) error { return nil }

type mockTool struct{}

func (mockTool) Name() string        { return "mock_tool" }
func (mockTool) Description() string { return "A mock tool" }
func (mockTool) Parameters() json.RawMessage {
	return json.RawMessage(`{"type":"object","properties":{}}`)
}
func (mockTool) Execute(ctx context.Context, input json.RawMessage) (tool.Result, error) {
	return tool.Result{Content: "ok"}, nil
}

func TestNewManager(t *testing.T) {
	m := NewManager()
	if m == nil {
		t.Fatal("expected non-nil manager")
	}
	if len(m.Plugins()) != 0 {
		t.Fatalf("expected 0 plugins, got %d", len(m.Plugins()))
	}
	if len(m.Results()) != 0 {
		t.Fatalf("expected 0 results, got %d", len(m.Results()))
	}
}

func TestAddPlugin(t *testing.T) {
	m := NewManager()
	p := &mockPlugin{name: "test", tools: []tool.Tool{mockTool{}}}
	m.AddPlugin(p)

	if len(m.Plugins()) != 1 {
		t.Fatalf("expected 1 plugin, got %d", len(m.Plugins()))
	}
	results := m.Results()
	if len(results) != 1 || !results[0].Success {
		t.Fatal("expected 1 successful result")
	}
	if results[0].Name != "test" {
		t.Fatalf("expected name 'test', got %q", results[0].Name)
	}
	if len(results[0].Tools) != 1 || results[0].Tools[0] != "mock_tool" {
		t.Fatalf("expected tools [mock_tool], got %v", results[0].Tools)
	}
}

func TestRegisterTools(t *testing.T) {
	m := NewManager()
	p := &mockPlugin{name: "test", tools: []tool.Tool{mockTool{}}}
	m.AddPlugin(p)

	reg := tool.NewRegistry()
	if err := m.RegisterTools(reg); err != nil {
		t.Fatalf("RegisterTools failed: %v", err)
	}
	if _, ok := reg.Get("mock_tool"); !ok {
		t.Fatal("mock_tool not registered")
	}
}

func TestRegisterTools_Duplicate(t *testing.T) {
	m := NewManager()
	p1 := &mockPlugin{name: "a", tools: []tool.Tool{mockTool{}}}
	p2 := &mockPlugin{name: "b", tools: []tool.Tool{mockTool{}}}
	m.AddPlugin(p1)
	m.AddPlugin(p2)

	reg := tool.NewRegistry()
	if err := m.RegisterTools(reg); err == nil {
		t.Fatal("expected error for duplicate tool registration")
	}
}

func TestLoadAll_CommandPlugin(t *testing.T) {
	m := NewManager()
	entries := []config.PluginConfigEntry{
		{
			Name: "my-cmds",
			Type: "command",
			Commands: []config.PluginCommandConfig{
				{Name: "deploy", Description: "Deploy", Execute: "deploy.sh"},
				{Name: "test", Description: "Run tests", Execute: "go test"},
			},
		},
	}
	m.LoadAll(entries)

	if len(m.Plugins()) != 1 {
		t.Fatalf("expected 1 plugin, got %d", len(m.Plugins()))
	}
	results := m.Results()
	if len(results) != 1 || !results[0].Success {
		t.Fatal("expected 1 successful result")
	}
	if len(results[0].Tools) != 2 {
		t.Fatalf("expected 2 tools, got %d", len(results[0].Tools))
	}

	// Verify tools are registerable
	reg := tool.NewRegistry()
	if err := m.RegisterTools(reg); err != nil {
		t.Fatalf("RegisterTools failed: %v", err)
	}
	if _, ok := reg.Get("deploy"); !ok {
		t.Fatal("deploy tool not registered")
	}
	if _, ok := reg.Get("test"); !ok {
		t.Fatal("test tool not registered")
	}
}

func TestLoadAll_EmptyCommands(t *testing.T) {
	m := NewManager()
	entries := []config.PluginConfigEntry{
		{Name: "empty", Type: "command"},
	}
	m.LoadAll(entries)

	if len(m.Plugins()) != 0 {
		t.Fatalf("expected 0 plugins, got %d", len(m.Plugins()))
	}
	results := m.Results()
	if len(results) != 1 || results[0].Success {
		t.Fatal("expected 1 failed result for empty commands")
	}
}

func TestLoadAll_MissingGoPlugin(t *testing.T) {
	m := NewManager()
	entries := []config.PluginConfigEntry{
		{Name: "missing", Path: "/nonexistent/plugin.so"},
	}
	m.LoadAll(entries)

	// Should not block — failure recorded
	if len(m.Plugins()) != 0 {
		t.Fatalf("expected 0 plugins, got %d", len(m.Plugins()))
	}
	results := m.Results()
	if len(results) != 1 || results[0].Success {
		t.Fatal("expected 1 failed result")
	}
}

func TestScanDir_YAMLDescriptor(t *testing.T) {
	tmpDir := t.TempDir()

	yamlContent := `name: yaml-plugin
type: command
commands:
  - name: hello
    description: "Say hello"
    execute: "echo hello"
`
	if err := os.WriteFile(filepath.Join(tmpDir, "test.yaml"), []byte(yamlContent), 0644); err != nil {
		t.Fatal(err)
	}

	m := NewManager()
	m.scanDir(tmpDir)

	if len(m.Plugins()) != 1 {
		t.Fatalf("expected 1 plugin, got %d", len(m.Plugins()))
	}
	results := m.Results()
	if len(results) != 1 || !results[0].Success {
		t.Fatal("expected 1 successful result")
	}
	if results[0].Name != "yaml-plugin" {
		t.Fatalf("expected name 'yaml-plugin', got %q", results[0].Name)
	}
}

func TestScanDir_JSONDescriptor(t *testing.T) {
	tmpDir := t.TempDir()

	jsonContent := `{"name": "json-plugin", "type": "command", "commands": [{"name": "greet", "description": "Greet", "execute": "echo hi"}]}`
	if err := os.WriteFile(filepath.Join(tmpDir, "test.json"), []byte(jsonContent), 0644); err != nil {
		t.Fatal(err)
	}

	m := NewManager()
	m.scanDir(tmpDir)

	if len(m.Plugins()) != 1 {
		t.Fatalf("expected 1 plugin, got %d", len(m.Plugins()))
	}
}

func TestScanDir_NonExistentDir(t *testing.T) {
	m := NewManager()
	m.scanDir("/nonexistent/path")
	// Should not panic or error
	if len(m.Results()) != 0 {
		t.Fatalf("expected 0 results for non-existent dir, got %d", len(m.Results()))
	}
}

func TestCommandTool(t *testing.T) {
	ct := NewCommandTool("test", "A test tool", "echo", nil)
	if ct.Name() != "test" {
		t.Fatalf("expected 'test', got %q", ct.Name())
	}
	if ct.Description() != "A test tool" {
		t.Fatalf("expected 'A test tool', got %q", ct.Description())
	}
	params := ct.Parameters()
	if string(params) == "" {
		t.Fatal("expected non-empty parameters")
	}

	result, err := ct.Execute(context.Background(), nil)
	if err != nil {
		t.Fatalf("Execute failed: %v", err)
	}
	if result.IsError {
		t.Fatal("expected no error")
	}
}

func TestCommandTool_Execute_Success(t *testing.T) {
	t.Parallel()

	ct := NewCommandTool("echo-test", "Echo test", "echo", nil)
	result, err := ct.Execute(context.Background(), json.RawMessage(`{}`))
	if err != nil {
		t.Fatalf("Execute returned unexpected error: %v", err)
	}
	if result.IsError {
		t.Fatalf("expected IsError=false, got error content: %s", result.Content)
	}
	// echo with no args prints a newline
	if !strings.Contains(result.Content, "\n") {
		t.Fatalf("expected output to contain newline, got %q", result.Content)
	}
}

func TestCommandTool_Execute_WithArgs(t *testing.T) {
	t.Parallel()

	ct := NewCommandTool("echo-hello", "Echo hello", "echo", []string{"hello", "world"})
	result, err := ct.Execute(context.Background(), json.RawMessage(`{}`))
	if err != nil {
		t.Fatalf("Execute returned unexpected error: %v", err)
	}
	if result.IsError {
		t.Fatalf("expected IsError=false, got error content: %s", result.Content)
	}
	if !strings.Contains(result.Content, "hello world") {
		t.Fatalf("expected output to contain 'hello world', got %q", result.Content)
	}
}

func TestCommandTool_Execute_JSONInputWithArgs(t *testing.T) {
	t.Parallel()

	ct := NewCommandTool("echo-args", "Echo with args", "echo", nil)
	input := json.RawMessage(`{"args": "hello world"}`)
	result, err := ct.Execute(context.Background(), input)
	if err != nil {
		t.Fatalf("Execute returned unexpected error: %v", err)
	}
	if result.IsError {
		t.Fatalf("expected IsError=false, got error content: %s", result.Content)
	}
	if !strings.Contains(result.Content, "hello world") {
		t.Fatalf("expected output to contain 'hello world', got %q", result.Content)
	}
}

func TestCommandTool_Execute_EmptyInput(t *testing.T) {
	t.Parallel()

	ct := NewCommandTool("echo-empty", "Echo empty", "echo", []string{"default"})

	// nil input should work using default args
	result, err := ct.Execute(context.Background(), nil)
	if err != nil {
		t.Fatalf("Execute with nil input returned unexpected error: %v", err)
	}
	if result.IsError {
		t.Fatalf("expected IsError=false, got error content: %s", result.Content)
	}
	if !strings.Contains(result.Content, "default") {
		t.Fatalf("expected output to contain 'default', got %q", result.Content)
	}

	// Empty byte slice should also work
	result2, err := ct.Execute(context.Background(), json.RawMessage{})
	if err != nil {
		t.Fatalf("Execute with empty raw message returned unexpected error: %v", err)
	}
	if result2.IsError {
		t.Fatalf("expected IsError=false, got error content: %s", result2.Content)
	}
}

func TestCommandTool_Execute_InvalidJSON(t *testing.T) {
	t.Parallel()

	ct := NewCommandTool("bad-json", "Bad JSON test", "echo", nil)
	result, err := ct.Execute(context.Background(), json.RawMessage(`{bad`))
	if err != nil {
		t.Fatalf("Execute returned unexpected go error: %v", err)
	}
	if !result.IsError {
		t.Fatal("expected IsError=true for invalid JSON input")
	}
	if !strings.Contains(result.Content, "invalid character") && !strings.Contains(result.Content, "cannot unmarshal") {
		t.Fatalf("expected error message about JSON parsing, got %q", result.Content)
	}
}

func TestCommandTool_Execute_CommandFailure(t *testing.T) {
	t.Parallel()

	ct := NewCommandTool("fail-cmd", "Always fails", "sh", []string{"-c", "echo 'oops' >&2 && exit 1"})
	result, err := ct.Execute(context.Background(), json.RawMessage(`{}`))
	if err != nil {
		t.Fatalf("Execute returned unexpected go error: %v", err)
	}
	if !result.IsError {
		t.Fatal("expected IsError=true for command failure")
	}
	if !strings.Contains(result.Content, "failed") {
		t.Fatalf("expected error message to contain 'failed', got %q", result.Content)
	}
	// stderr output should be captured in the error message
	if !strings.Contains(result.Content, "oops") {
		t.Fatalf("expected error message to contain stderr output 'oops', got %q", result.Content)
	}
}

func TestCommandTool_Execute_ContextCancellation(t *testing.T) {
	t.Parallel()

	ct := NewCommandTool("slow-cmd", "Slow command", "sh", []string{"-c", "sleep 30"})

	ctx, cancel := context.WithCancel(context.Background())
	// Cancel immediately so the command context is already done
	cancel()

	result, err := ct.Execute(ctx, json.RawMessage(`{}`))
	if err != nil {
		t.Fatalf("Execute returned unexpected go error: %v", err)
	}
	if !result.IsError {
		t.Fatal("expected IsError=true for cancelled context")
	}
	if !strings.Contains(result.Content, "failed") {
		t.Fatalf("expected error message to contain 'failed', got %q", result.Content)
	}
}

func TestCommandTool_Execute_ContextCancelDuringRun(t *testing.T) {
	t.Parallel()

	ct := NewCommandTool("slow-cmd2", "Slow command 2", "sh", []string{"-c", "sleep 30"})

	ctx, cancel := context.WithTimeout(context.Background(), 50*time.Millisecond)
	defer cancel()

	resultCh := make(chan tool.Result)
	go func() {
		r, execErr := ct.Execute(ctx, json.RawMessage(`{}`))
		if execErr != nil {
			r = tool.Result{Content: execErr.Error(), IsError: true}
		}
		resultCh <- r
	}()

	select {
	case result := <-resultCh:
		if !result.IsError {
			t.Fatal("expected IsError=true for cancelled context")
		}
		if !strings.Contains(result.Content, "failed") {
			t.Fatalf("expected error message to contain 'failed', got %q", result.Content)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("test timed out waiting for command to be cancelled")
	}
}
