package config

// #3528 probe: saving im.yaml / mcp_servers.yaml must not destroy on-disk
// ${VAR} references when the in-memory value is just the Load-time expansion
// of them (the #608 data-loss class, now covered for these two sections).
// A genuinely changed value must still win.

import (
	"os"
	"strings"
	"testing"
)

const refYAML = "- name: srv\n  env:\n    TOKEN: ${TESTVAR_3528}\n"

func TestIssue3528_MCPSavePreservesEnvRef(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("TESTVAR_3528", "expanded-secret")
	path := MCPServersPath(dir)
	if err := os.MkdirAll(dir, 0700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(refYAML), 0600); err != nil {
		t.Fatal(err)
	}

	// In-memory state mirrors what Load produced: the ${VAR} expanded.
	servers := []MCPServerConfig{{
		Name: "srv",
		Env:  map[string]string{"TOKEN": "expanded-secret"},
	}}
	if err := SaveMCPServers(dir, servers); err != nil {
		t.Fatalf("SaveMCPServers: %v", err)
	}

	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(data), "${TESTVAR_3528}") {
		t.Fatalf("env ref destroyed by save (plaintext persisted):\n%s", string(data))
	}
	if strings.Contains(string(data), "expanded-secret") {
		t.Fatalf("materialized secret written to disk:\n%s", string(data))
	}

	// A genuinely changed value must still win over the stale ref.
	changed := []MCPServerConfig{{
		Name: "srv",
		Env:  map[string]string{"TOKEN": "user-typed-new"},
	}}
	if err := SaveMCPServers(dir, changed); err != nil {
		t.Fatalf("SaveMCPServers(changed): %v", err)
	}
	data2, _ := os.ReadFile(path)
	if !strings.Contains(string(data2), "user-typed-new") {
		t.Fatalf("user-changed value must be kept:\n%s", string(data2))
	}
}

func TestIssue3528_IMSavePreservesEnvRef(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("TESTVAR_3528B", "im-secret")
	path := IMPath(dir)
	if err := os.MkdirAll(dir, 0700); err != nil {
		t.Fatal(err)
	}
	seed := "adapters:\n  qq:\n    env:\n      TOKEN: ${TESTVAR_3528B}\n"
	if err := os.WriteFile(path, []byte(seed), 0600); err != nil {
		t.Fatal(err)
	}

	// In-memory state mirrors the Load-time expansion.
	im := &IMConfig{
		Adapters: map[string]IMAdapterConfig{
			"qq": {Env: map[string]string{"TOKEN": "im-secret"}},
		},
	}
	if err := SaveIMConfig(dir, im); err != nil {
		t.Fatalf("SaveIMConfig: %v", err)
	}

	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(data), "${TESTVAR_3528B}") {
		t.Fatalf("im env ref destroyed by save:\n%s", string(data))
	}
	if strings.Contains(string(data), "im-secret") {
		t.Fatalf("materialized im secret written to disk:\n%s", string(data))
	}
}
