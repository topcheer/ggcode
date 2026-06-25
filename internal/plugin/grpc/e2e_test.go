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
// Returns the absolute path to the built binary.
func buildEchoPlugin(t *testing.T) string {
	t.Helper()

	// Resolve plugin dir relative to the test working directory (package dir).
	repoRoot, _ := os.Getwd()
	repoRoot = filepath.Join(repoRoot, "..", "..", "..")
	pluginDir := filepath.Join(repoRoot, "internal", "plugin", "grpc", "testdata", "echo-plugin")

	binaryName := "echo-plugin"
	if runtime.GOOS == "windows" {
		binaryName += ".exe"
	}
	binaryPath := filepath.Join(pluginDir, binaryName)

	// Check if already built AND compatible with the current platform.
	// A binary committed from another OS/arch (e.g. macOS arm64) will
	// exist but fail with "exec format error" on Linux amd64.
	needBuild := true
	if info, err := os.Stat(binaryPath); err == nil && info.Size() > 0 {
		// Binary exists — verify it's executable on this platform.
		if canExecuteBinary(binaryPath) {
			needBuild = false
		}
	}

	if needBuild {
		// Remove stale binary from another platform.
		_ = os.Remove(binaryPath)

		// Run go mod tidy first to ensure go.sum is complete
		tidyCmd := exec.Command("go", "mod", "tidy")
		tidyCmd.Dir = pluginDir
		if out, err := tidyCmd.CombinedOutput(); err != nil {
			t.Fatalf("tidying echo plugin modules: %v\n%s", err, string(out))
		}

		// Build: use absolute path for -o to avoid cwd confusion
		cmd := exec.Command("go", "build", "-tags", "goolm", "-o", binaryPath, ".")
		cmd.Dir = pluginDir
		if out, err := cmd.CombinedOutput(); err != nil {
			t.Fatalf("building echo plugin: %v\n%s", err, string(out))
		}
	}

	return binaryPath
}

// canExecuteBinary checks whether a binary file can be executed on the
// current platform by examining its header magic bytes.
func canExecuteBinary(path string) bool {
	// On Windows, skip the check — .exe files are assumed compatible.
	if runtime.GOOS == "windows" {
		return true
	}

	f, err := os.Open(path)
	if err != nil {
		return false
	}
	defer f.Close()

	// Read the first 4 bytes to check the magic number.
	header := make([]byte, 4)
	if _, err := f.Read(header); err != nil {
		return false
	}

	// ELF (Linux): 0x7F 45 4C 46
	if header[0] == 0x7F && header[1] == 0x45 && header[2] == 0x4C && header[3] == 0x46 {
		return runtime.GOOS == "linux"
	}
	// Mach-O (macOS): 0xFE 0xED 0xFA 0xCE (32-bit), 0xFE 0xED 0xFA 0xCF (64-bit),
	// or little-endian variants 0xCE 0xFA 0xED 0xFE / 0xCF 0xFA 0xED 0xFE
	if (header[0] == 0xFE && (header[1] == 0xED || header[1] == 0xCF)) ||
		(header[3] == 0xFE && (header[2] == 0xED || header[2] == 0xCF)) {
		return runtime.GOOS == "darwin"
	}
	// PE (Windows): 0x4D 0x5A ("MZ")
	if header[0] == 0x4D && header[1] == 0x5A {
		return runtime.GOOS == "windows"
	}

	// Unknown format — let the caller try anyway.
	return false
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
