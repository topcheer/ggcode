package acp

import (
	"context"
	"os"
	"testing"
	"time"
)

func TestClientManagerNew(t *testing.T) {
	// Create manager with empty PATH (no agents found)
	t.Setenv("PATH", t.TempDir())
	mgr := NewClientManager(t.TempDir(), nil)

	available := mgr.Available()
	if len(available) != 0 {
		t.Errorf("expected 0 available agents, got %d", len(available))
	}
}

func TestClientManagerAgentInfoNotFound(t *testing.T) {
	t.Setenv("PATH", t.TempDir())
	mgr := NewClientManager(t.TempDir(), nil)

	_, _, ok := mgr.AgentInfo("nonexistent")
	if ok {
		t.Error("expected ok=false for nonexistent agent")
	}
}

func TestClientManagerGetNonexistent(t *testing.T) {
	t.Setenv("PATH", t.TempDir())
	mgr := NewClientManager(t.TempDir(), nil)

	_, err := mgr.Get(context.Background(), "nonexistent")
	if err == nil {
		t.Error("expected error for nonexistent agent")
	}
}

func TestClientManagerCloseAll(t *testing.T) {
	t.Setenv("PATH", t.TempDir())
	mgr := NewClientManager(t.TempDir(), nil)

	// CloseAll should not panic even with no agents
	mgr.CloseAll()
}

func TestClientManagerDiscoverWithAgents(t *testing.T) {
	// Create fake agent binaries
	dir := t.TempDir()
	for _, name := range []string{"copilot"} {
		path := dir + "/" + name
		writeFakeBin(t, path)
	}

	t.Setenv("PATH", dir)
	mgr := NewClientManager(dir, nil)

	available := mgr.Available()
	if len(available) != 1 {
		t.Fatalf("expected 1 agent, got %d: %v", len(available), available)
	}
	if available[0] != "copilot" {
		t.Errorf("expected agent = %q, got %q", "copilot", available[0])
	}

	title, desc, ok := mgr.AgentInfo("copilot")
	if !ok {
		t.Fatal("expected ok=true for copilot agent")
	}
	if title != "GitHub Copilot" {
		t.Errorf("expected title = %q, got %q", "GitHub Copilot", title)
	}
	if desc == "" {
		t.Error("expected non-empty description")
	}
	client := mgr.clients["copilot"]
	if len(client.mcpServers) != 0 {
		t.Fatalf("expected discovered client to send empty mcpServers, got %+v", client.mcpServers)
	}
}

func TestErrAgentNotFound(t *testing.T) {
	err := ErrAgentNotFound{name: "test-agent"}
	if err.Error() == "" {
		t.Error("expected non-empty error message")
	}
}

func TestClientGetWithoutStart(t *testing.T) {
	agent := DiscoveredAgent{
		Def: AgentDef{
			Name:        "test",
			Title:       "Test",
			Binaries:    []string{"test"},
			ACPCommand:  []string{"--acp"},
			Description: "Test agent",
		},
		Path: "/nonexistent/binary",
	}

	client := NewClient(agent, t.TempDir(), nil, nil)
	if client.Name() != "test" {
		t.Errorf("expected name = %q, got %q", "test", client.Name())
	}
	if client.Title() != "Test" {
		t.Errorf("expected title = %q, got %q", "Test", client.Title())
	}
	if client.Description() != "Test agent" {
		t.Errorf("expected description = %q, got %q", "Test agent", client.Description())
	}

	// Starting a nonexistent binary should fail
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	err := client.Start(ctx)
	if err == nil {
		t.Error("expected error starting nonexistent binary")
	}
}

func writeFakeBin(t *testing.T, path string) {
	t.Helper()
	if err := writeScript(path, "#!/bin/sh\nsleep 3600\n"); err != nil {
		t.Fatal(err)
	}
}

func writeScript(path, content string) error {
	if err := os.WriteFile(path, []byte(content), 0o755); err != nil {
		return err
	}
	return nil
}
