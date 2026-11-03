package grpcplugin_test

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

func buildEchoPlugin(t *testing.T) string {
	t.Helper()

	repoRoot, _ := os.Getwd()
	repoRoot = filepath.Join(repoRoot, "..", "..", "..")
	pluginDir := filepath.Join(repoRoot, "internal", "plugin", "grpc", "testdata", "echo-plugin")

	binaryName := "echo-plugin"
	if runtime.GOOS == "windows" {
		binaryName += ".exe"
	}
	binaryPath := filepath.Join(pluginDir, binaryName)

	needBuild := true
	if info, err := os.Stat(binaryPath); err == nil && info.Size() > 0 {

		if canExecuteBinary(binaryPath) {
			needBuild = false
		}
	}

	if needBuild {

		_ = os.Remove(binaryPath)

		tidyCmd := exec.Command("go", "mod", "tidy")
		tidyCmd.Dir = pluginDir
		if out, err := tidyCmd.CombinedOutput(); err != nil {
			t.Fatalf("tidying echo plugin modules: %v\n%s", err, string(out))
		}

		cmd := exec.Command("go", "build", "-tags", "goolm", "-o", binaryPath, ".")
		cmd.Dir = pluginDir
		if out, err := cmd.CombinedOutput(); err != nil {
			t.Fatalf("building echo plugin: %v\n%s", err, string(out))
		}
	}

	return binaryPath
}

func canExecuteBinary(path string) bool {

	if runtime.GOOS == "windows" {
		return true
	}

	f, err := os.Open(path)
	if err != nil {
		return false
	}
	defer f.Close()

	header := make([]byte, 4)
	if _, err := f.Read(header); err != nil {
		return false
	}

	if header[0] == 0x7F && header[1] == 0x45 && header[2] == 0x4C && header[3] == 0x46 {
		return runtime.GOOS == "linux"
	}

	if (header[0] == 0xFE && (header[1] == 0xED || header[1] == 0xCF)) ||
		(header[3] == 0xFE && (header[2] == 0xED || header[2] == 0xCF)) {
		return runtime.GOOS == "darwin"
	}

	if header[0] == 0x4D && header[1] == 0x5A {
		return runtime.GOOS == "windows"
	}

	return false
}

func TestEndToEnd_PluginLoadAndExecute(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping integration test in short mode")
	}

	binaryPath := buildEchoPlugin(t)

	registry := tool.NewRegistry()
	mgr := grpcplugin.NewManager(t.TempDir())

	errs := mgr.LoadAll([]grpcplugin.GRPCPluginConfig{{
		Name:    "echo-plugin",
		Command: []string{binaryPath},
	}}, registry)

	if len(errs) > 0 {
		t.Fatalf("LoadAll errors: %v", errs)
	}

	greetTool := lookupTool(registry, "greet")
	if greetTool == nil {
		tools := registry.List()
		names := make([]string, len(tools))
		for i, tl := range tools {
			names[i] = tl.Name()
		}
		t.Fatalf("expected 'greet' tool to be registered, have: %v", names)
	}

	if greetTool.Description() != "Greets a person by name" {
		t.Errorf("unexpected description: %q", greetTool.Description())
	}

	var schema map[string]interface{}
	if err := json.Unmarshal(greetTool.Parameters(), &schema); err != nil {
		t.Errorf("invalid parameters JSON: %v", err)
	}
	if schema["type"] != "object" {
		t.Errorf("expected type=object, got %v", schema["type"])
	}

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

	if !statuses[0].Alive {
		t.Error("expected Alive=true for a freshly loaded, running plugin")
	}

	mgr.Shutdown()

	after := mgr.Status()
	if len(after) != 0 {
		t.Errorf("expected 0 statuses after Shutdown (map cleared), got %d", len(after))
	}
}

func lookupTool(registry *tool.Registry, name string) tool.Tool {
	for _, t := range registry.List() {
		if t.Name() == name {
			return t
		}
	}
	return nil
}
