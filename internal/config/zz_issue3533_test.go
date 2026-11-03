package config

import (
	"os"
	"strings"
	"testing"
)

const shiftYAML = "" +
	"- name: search\n" +
	"  env:\n" +
	"    K: v\n" +
	"- name: github\n" +
	"  headers:\n" +
	"    Authorization: Bearer ${TESTVAR_3533}\n"

func TestIssue3533_RemoveLeadingServerKeepsTrailingRef(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("TESTVAR_3533", "shifted-secret")
	path := MCPServersPath(dir)
	if err := os.MkdirAll(dir, 0700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(shiftYAML), 0600); err != nil {
		t.Fatal(err)
	}

	servers := []MCPServerConfig{{
		Name: "github",
		Headers: map[string]string{
			"Authorization": "Bearer shifted-secret",
		},
	}}
	if err := SaveMCPServers(dir, servers); err != nil {
		t.Fatalf("SaveMCPServers: %v", err)
	}

	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(data), "${TESTVAR_3533}") {
		t.Fatalf("index shift destroyed env ref (plaintext persisted):\n%s", string(data))
	}
	if strings.Contains(string(data), "shifted-secret") {
		t.Fatalf("materialized secret written to disk:\n%s", string(data))
	}
}

func TestIssue3533_ReorderKeepsRefsOnOwnServers(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("TESTVAR_3533", "reordered-secret")
	t.Setenv("TESTVAR_3533B", "other-secret")
	path := MCPServersPath(dir)
	if err := os.MkdirAll(dir, 0700); err != nil {
		t.Fatal(err)
	}
	twoRefs := "" +
		"- name: first\n" +
		"  env:\n" +
		"    A: ${TESTVAR_3533}\n" +
		"- name: second\n" +
		"  env:\n" +
		"    B: ${TESTVAR_3533B}\n"
	if err := os.WriteFile(path, []byte(twoRefs), 0600); err != nil {
		t.Fatal(err)
	}

	servers := []MCPServerConfig{
		{Name: "second", Env: map[string]string{"B": "other-secret"}},
		{Name: "first", Env: map[string]string{"A": "reordered-secret"}},
	}
	if err := SaveMCPServers(dir, servers); err != nil {
		t.Fatalf("SaveMCPServers: %v", err)
	}

	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(data), "${TESTVAR_3533}") || !strings.Contains(string(data), "${TESTVAR_3533B}") {
		t.Fatalf("reorder destroyed env refs (plaintext persisted):\n%s", string(data))
	}
	if strings.Contains(string(data), "reordered-secret") || strings.Contains(string(data), "other-secret") {
		t.Fatalf("materialized secrets written to disk:\n%s", string(data))
	}
}

func TestIssue3533_AlignByName(t *testing.T) {
	existing := []interface{}{
		map[string]interface{}{"name": "a", "v": "1"},
		map[string]interface{}{"v": "anon"},
		map[string]interface{}{"name": "b", "v": "2"},
	}
	out := []interface{}{
		map[string]interface{}{"name": "b", "v": "2"},
		map[string]interface{}{"name": "a", "v": "1"},
	}
	aligned := alignByName(existing, out)
	if len(aligned) != 2 {
		t.Fatalf("aligned length: %d", len(aligned))
	}
	first, ok0 := aligned[0].(map[string]interface{})
	if !ok0 || first["name"] != "b" {
		t.Fatalf("first aligned element must be named b, got %v", aligned[0])
	}
	second, ok1 := aligned[1].(map[string]interface{})
	if !ok1 || second["name"] != "a" {
		t.Fatalf("second aligned element must be named a, got %v", aligned[1])
	}
}
