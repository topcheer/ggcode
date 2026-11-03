package agentruntime

import (
	"os"

	"github.com/topcheer/ggcode/internal/config"
	"path/filepath"
	"testing"
)

func TestStartupNoticesCollectedFromMCPGate(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	ws := t.TempDir()
	if err := os.WriteFile(filepath.Join(ws, "ggcode.yaml"), []byte("version: 1\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	if err := os.WriteFile(filepath.Join(ws, ".mcp.json"), []byte(`{"mcpServers":{"unapproved-srv":{"type":"stdio","command":"echo"}}}`), 0o644); err != nil {
		t.Fatal(err)
	}

	core, err := BuildInteractiveRuntimeCore(
		config.DefaultConfig(),
		ws,
		nil,
	)
	if err != nil {
		t.Fatal(err)
	}
	found := false
	for _, n := range core.StartupNotices {
		if len(n) > 0 && n != "" {
			found = true
		}
	}
	if !found {
		t.Fatalf("gate blocked an unapproved server but StartupNotices is empty: %v", core.StartupNotices)
	}
}
