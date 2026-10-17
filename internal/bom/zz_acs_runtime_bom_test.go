package bom

// Runtime-axis (ACS AgBOM, r553 / OWASP Agent Control Standard 2026-09)
// companion tests for BuildWithRuntime: models/mcp-servers/plugins/config
// entries, dedup, tombstone Enabled=false, config-missing degradation, and
// canonical sort/counts after extension.

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// fakeTree creates <root>/ws/.ggcode/{skills,memory} with minimal artifacts.
func fakeTree(t *testing.T) (ws, home string) {
	t.Helper()
	root := t.TempDir()
	ws = filepath.Join(root, "ws")
	home = filepath.Join(root, "home")
	for _, d := range []string{
		filepath.Join(ws, ".ggcode", "skills", "alpha"),
		filepath.Join(home, ".ggcode", "memory"),
	} {
		if err := os.MkdirAll(d, 0o755); err != nil {
			t.Fatal(err)
		}
	}
	if err := os.WriteFile(filepath.Join(ws, ".ggcode", "skills", "alpha", "SKILL.md"), []byte("# alpha\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(home, ".ggcode", "memory", "build-x.md"), []byte("x\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	return ws, home
}

func writeConfig(t *testing.T, ws string, body string) string {
	t.Helper()
	path := filepath.Join(filepath.Dir(filepath.Join(ws, ".ggcode")), "ggcode.yaml")
	if err := os.WriteFile(path, []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
	return path
}

func TestBuildWithRuntimeCountsAndGates(t *testing.T) {
	ws, home := fakeTree(t)
	cfg := writeConfig(t, ws, "model: glm-5\n")
	rc := &RuntimeConfig{
		ConfigPath: cfg,
		Models:     []string{"glm-5", "glm-5-air"},
		MCPServers: []RuntimeMCPServer{{Name: "github", Enabled: true}, {Name: "dead", Enabled: false}},
		Plugins:    []RuntimePlugin{{Name: "myplug", Path: "/tmp/p.so"}},
	}
	rep, err := BuildWithRuntime(ws, home, rc)
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []struct {
		typ string
		n   int
	}{
		{"model", 2}, {"mcp-server", 2}, {"plugin", 1}, {"config", 1}, {"skill", 1}, {"memory", 1},
	} {
		if rep.Counts[want.typ] != want.n {
			t.Errorf("counts[%s] = %d, want %d", want.typ, rep.Counts[want.typ], want.n)
		}
	}
	// Every runtime entry carries gate=config and the whole-config hash.
	var cfgEntry *Entry
	for i := range rep.Entries {
		e := &rep.Entries[i]
		if e.Type == TypeConfig {
			cfgEntry = e
			continue
		}
		switch e.Type {
		case TypeModel, TypeMCPServer, TypePlugin:
			if e.PromotionGate != GateConfig {
				t.Errorf("%s/%s gate = %q, want config", e.Type, e.Name, e.PromotionGate)
			}
			if e.SHA256 == "" {
				t.Errorf("%s/%s sha256 empty", e.Type, e.Name)
			}
		}
	}
	if cfgEntry == nil {
		t.Fatal("no config entry")
	}
	if cfgEntry.Name != "ggcode.yaml" || cfgEntry.SHA256 == "" {
		t.Errorf("config entry malformed: %+v", cfgEntry)
	}
}

func TestBuildWithRuntimeModelDedup(t *testing.T) {
	ws, home := fakeTree(t)
	cfg := writeConfig(t, ws, "model: m\n")
	rep, err := BuildWithRuntime(ws, home, &RuntimeConfig{
		ConfigPath: cfg,
		Models:     []string{"glm-5", "glm-5 ", "GLM-5"},
	})
	if err != nil {
		t.Fatal(err)
	}
	if rep.Counts[TypeModel] != 1 {
		t.Errorf("model count = %d, want 1 (case/space dedup)", rep.Counts[TypeModel])
	}
}

func TestBuildWithRuntimeTombstoneDisabled(t *testing.T) {
	ws, home := fakeTree(t)
	cfg := writeConfig(t, ws, "model: m\n")
	rep, err := BuildWithRuntime(ws, home, &RuntimeConfig{
		ConfigPath: cfg,
		MCPServers: []RuntimeMCPServer{{Name: "resurrected", Enabled: false}},
	})
	if err != nil {
		t.Fatal(err)
	}
	for _, e := range rep.Entries {
		if e.Type == TypeMCPServer && e.Name == "resurrected" && e.Enabled {
			t.Error("tombstoned MCP server must be Enabled=false")
		}
	}
}

func TestBuildWithRuntimeConfigMissingDegrades(t *testing.T) {
	ws, home := fakeTree(t)
	rep, err := BuildWithRuntime(ws, home, &RuntimeConfig{
		ConfigPath: filepath.Join(ws, "nope.yaml"),
		Models:     []string{"glm-5"},
	})
	if err != nil {
		t.Fatal(err)
	}
	if len(rep.Warnings) == 0 || !strings.Contains(rep.Warnings[0], "config unreadable") {
		t.Errorf("expected config-unreadable warning, got %v", rep.Warnings)
	}
	if rep.Counts[TypeModel] != 1 || rep.Counts[TypeConfig] != 0 {
		t.Errorf("missing config should degrade, not drop runtime entries: counts=%v", rep.Counts)
	}
}

func TestBuildWithRuntimeNilRCMatchesBuild(t *testing.T) {
	ws, home := fakeTree(t)
	base, err := Build(ws, home)
	if err != nil {
		t.Fatal(err)
	}
	extended, err := BuildWithRuntime(ws, home, nil)
	if err != nil {
		t.Fatal(err)
	}
	// GeneratedAt differs between the two calls by construction; zero it.
	base.GeneratedAt = time.Time{}
	extended.GeneratedAt = time.Time{}
	a, _ := json.Marshal(base)
	b, _ := json.Marshal(extended)
	if string(a) != string(b) {
		t.Error("nil RuntimeConfig must behave exactly like Build")
	}
}

func TestBuildWithRuntimeSortCanonical(t *testing.T) {
	ws, home := fakeTree(t)
	cfg := writeConfig(t, ws, "model: m\n")
	rep, err := BuildWithRuntime(ws, home, &RuntimeConfig{
		ConfigPath: cfg,
		Models:     []string{"zeta", "alpha"},
	})
	if err != nil {
		t.Fatal(err)
	}
	last := ""
	for _, e := range rep.Entries {
		key := e.Type + "/" + e.Scope + "/" + e.Name
		if last != "" && key < last {
			t.Errorf("entries out of canonical order: %q after %q", key, last)
		}
		last = key
	}
}
