package acp

import (
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"testing"
)

func TestDiscoverEmpty(t *testing.T) {
	// With an empty PATH, nothing should be found
	t.Setenv("PATH", t.TempDir())
	agents := Discover()
	if len(agents) != 0 {
		t.Errorf("expected 0 agents with empty PATH, got %d", len(agents))
	}
}

func TestDiscoverKnownAgents(t *testing.T) {
	// Create a temp dir with fake agent binaries
	dir := t.TempDir()

	// Create fake "copilot" binary
	for _, name := range []string{"copilot", "droid", "opencode"} {
		path := filepath.Join(dir, name)
		if err := os.WriteFile(path, []byte("#!/bin/sh\necho ok"), 0o755); err != nil {
			t.Fatal(err)
		}
	}

	// Add an unrelated binary
	if err := os.WriteFile(filepath.Join(dir, "unrelated"), []byte("#!/bin/sh"), 0o755); err != nil {
		t.Fatal(err)
	}

	// Set PATH to our temp dir
	origPath := os.Getenv("PATH")
	t.Setenv("PATH", dir)
	defer t.Setenv("PATH", origPath)

	// Clear exec.LookPath cache by using the environment directly
	agents := Discover()

	names := make([]string, len(agents))
	for i, a := range agents {
		names[i] = a.Def.Name
	}
	sort.Strings(names)

	expected := []string{"copilot", "droid", "opencode"}
	if len(names) != len(expected) {
		t.Fatalf("expected %d agents, got %d: %v", len(expected), len(names), names)
	}
	for i, name := range expected {
		if names[i] != name {
			t.Errorf("expected agent[%d] = %q, got %q", i, name, names[i])
		}
	}

	// Verify paths are correct
	for _, a := range agents {
		if filepath.Dir(a.Path) != dir {
			t.Errorf("agent %q path = %q, expected in %q", a.Def.Name, a.Path, dir)
		}
	}
}

func TestDiscoverPartial(t *testing.T) {
	// Only create "droid"
	dir := t.TempDir()
	path := filepath.Join(dir, "droid")
	if err := os.WriteFile(path, []byte("#!/bin/sh"), 0o755); err != nil {
		t.Fatal(err)
	}

	origPath := os.Getenv("PATH")
	t.Setenv("PATH", dir)
	defer t.Setenv("PATH", origPath)

	agents := Discover()
	if len(agents) != 1 {
		t.Fatalf("expected 1 agent, got %d", len(agents))
	}
	if agents[0].Def.Name != "droid" {
		t.Errorf("expected agent name = %q, got %q", "droid", agents[0].Def.Name)
	}
}

func TestFindBinaryNotFound(t *testing.T) {
	_, err := findBinary([]string{"nonexistent_binary_xyz_12345"})
	if err == nil {
		t.Error("expected error for nonexistent binary")
	}
	if !os.IsNotExist(err) && err != exec.ErrNotFound {
		// exec.LookPath returns exec.ErrNotFound on some platforms
		t.Logf("got error: %v (acceptable)", err)
	}
}

func TestKnownAgentsStructure(t *testing.T) {
	// Verify KnownAgents has expected entries
	names := make(map[string]bool)
	for _, def := range KnownAgents {
		if def.Name == "" {
			t.Error("AgentDef has empty Name")
		}
		if def.Title == "" {
			t.Errorf("AgentDef %q has empty Title", def.Name)
		}
		if len(def.Binaries) == 0 {
			t.Errorf("AgentDef %q has no Binaries", def.Name)
		}
		if len(def.ACPCommand) == 0 {
			t.Errorf("AgentDef %q has no ACPCommand", def.Name)
		}
		if names[def.Name] {
			t.Errorf("duplicate agent name: %q", def.Name)
		}
		names[def.Name] = true
	}

	expected := []string{"copilot", "droid", "opencode"}
	for _, name := range expected {
		if !names[name] {
			t.Errorf("expected agent %q in KnownAgents", name)
		}
	}
}
