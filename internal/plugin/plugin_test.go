package plugin

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"testing"

	"github.com/topcheer/ggcode/internal/config"
	"github.com/topcheer/ggcode/internal/tool"
)

// mockPlugin is a test plugin.
type mockPlugin struct {
	name  string
	tools []tool.Tool
}

func (m *mockPlugin) Name() string                   { return m.name }
func (m *mockPlugin) Tools() []tool.Tool              { return m.tools }
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
