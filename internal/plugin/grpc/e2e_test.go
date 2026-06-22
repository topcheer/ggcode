package grpcplugin_test

// Integration test: builds a real plugin binary and loads it via the Manager.
// This validates the full go-plugin handshake → gRPC → ListTools → Execute flow.

import (
	"context"
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"testing"

	grpcplugin "github.com/topcheer/ggcode/internal/plugin/grpc"
	"github.com/topcheer/ggcode/internal/tool"
)

// buildEchoPlugin compiles the test plugin binary.
// Returns the path to the built binary.
func buildEchoPlugin(t *testing.T) string {
	t.Helper()

	pluginDir := filepath.Join("..", "..", "..", "internal", "plugin", "grpc", "testdata", "echo-plugin")
	binaryPath := filepath.Join(pluginDir, "echo-plugin")
	if runtime.GOOS == "windows" {
		binaryPath += ".exe"
	}

	// Check if already built; if not, build it
	if _, err := os.Stat(binaryPath); os.IsNotExist(err) {
		cmd := exec.Command("go", "build", "-tags", "goolm", "-o", binaryPath, ".")
		cmd.Dir = pluginDir
		if out, err := cmd.CombinedOutput(); err != nil {
			t.Fatalf("building echo plugin: %v\n%s", err, string(out))
		}
	}

	abs, _ := filepath.Abs(binaryPath)
	return abs
}

func TestEndToEnd_PluginLoadAndExecute(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping integration test in short mode")
	}

	binaryPath := buildEchoPlugin(t)

	// Load via Manager
	registry := tool.NewRegistry()
	mgr := grpcplugin.NewManager(t.TempDir())

	errs := mgr.LoadAll([]grpcplugin.GRPCPluginConfig{{
		Name:    "echo-plugin",
		Command: []string{binaryPath},
	}}, registry)

	if len(errs) > 0 {
		t.Fatalf("LoadAll errors: %v", errs)
	}

	// Verify tool was registered
	greetTool := lookupTool(registry, "greet")
	if greetTool == nil {
		tools := registry.List()
		names := make([]string, len(tools))
		for i, tl := range tools {
			names[i] = tl.Name()
		}
		t.Fatalf("expected 'greet' tool to be registered, have: %v", names)
	}

	// Verify metadata
	if greetTool.Description() != "Greets a person by name" {
		t.Errorf("unexpected description: %q", greetTool.Description())
	}

	// Verify parameters schema
	var schema map[string]interface{}
	if err := json.Unmarshal(greetTool.Parameters(), &schema); err != nil {
		t.Errorf("invalid parameters JSON: %v", err)
	}
	if schema["type"] != "object" {
		t.Errorf("expected type=object, got %v", schema["type"])
	}

	// Execute the tool
	result, err := greetTool.Execute(context.Background(), json.RawMessage(`{"name":"World"}`))
	if err != nil {
		t.Fatalf("Execute failed: %v", err)
	}

	if result.Content != "Hello, World!" {
		t.Errorf("expected 'Hello, World!', got %q", result.Content)
	}
	if result.IsError {
		t.Error("expected IsError=false")
	}

	// Shutdown
	mgr.Shutdown()
}

func TestEndToEnd_PluginStatus(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping integration test in short mode")
	}

	binaryPath := buildEchoPlugin(t)

	registry := tool.NewRegistry()
	mgr := grpcplugin.NewManager(t.TempDir())

	_ = mgr.LoadAll([]grpcplugin.GRPCPluginConfig{{
		Name:    "echo-plugin",
		Command: []string{binaryPath},
	}}, registry)

	// Check status
	statuses := mgr.Status()
	if len(statuses) != 1 {
		t.Fatalf("expected 1 status, got %d", len(statuses))
	}
	if statuses[0].Name != "echo-plugin" {
		t.Errorf("expected name 'echo-plugin', got %q", statuses[0].Name)
	}
	if len(statuses[0].Tools) == 0 {
		t.Error("expected at least 1 tool in status")
	}
	if !strings.Contains(strings.Join(statuses[0].Tools, ","), "greet") {
		t.Errorf("expected 'greet' in tools, got %v", statuses[0].Tools)
	}

	mgr.Shutdown()
}

func lookupTool(registry *tool.Registry, name string) tool.Tool {
	for _, t := range registry.List() {
		if t.Name() == name {
			return t
		}
	}
	return nil
}
