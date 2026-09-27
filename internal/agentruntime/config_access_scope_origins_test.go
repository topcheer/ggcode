package agentruntime

import (
	"encoding/json"
	"os"
	"strings"
	"testing"

	"github.com/topcheer/ggcode/internal/config"
)

// ============================================================================
// Effective-config scope provenance (r153): Codex /debug-config analog
// (upstream openai/codex#26255). Get("scope.origins") exposes which top-level
// config keys are sourced from the instance layer plus the save scope and
// instance paths; Get("scope.origin.<key>") answers per-key provenance.
// Both are read-only and never include config values (redaction-safe).
// ============================================================================

func writeInstanceConfig(t *testing.T, ws, content string) {
	t.Helper()
	if err := os.MkdirAll(config.InstanceDir(ws), 0o755); err != nil {
		t.Fatalf("mkdir instance dir: %v", err)
	}
	if err := os.WriteFile(config.InstanceConfigPath(ws), []byte(content), 0o600); err != nil {
		t.Fatalf("write instance config: %v", err)
	}
}

func TestConfigAccess_ScopeOriginsNotAttached(t *testing.T) {
	cfg := &config.Config{Language: "en"}
	access := NewConfigAccess(cfg, t.TempDir())

	out, err := access.Get("scope.origins")
	if err != nil {
		t.Fatalf("get scope.origins: %v", err)
	}
	var payload map[string]interface{}
	if err := json.Unmarshal([]byte(out), &payload); err != nil {
		t.Fatalf("scope.origins is not valid JSON: %v\nraw: %s", err, out)
	}
	if attached, _ := payload["instance_attached"].(bool); attached {
		t.Fatalf("instance_attached = true, want false")
	}
	if fields, _ := payload["instance_fields"].([]interface{}); len(fields) != 0 {
		t.Fatalf("instance_fields = %v, want empty", fields)
	}
	if scope, _ := payload["save_scope"].(string); scope != "global" {
		t.Fatalf("save_scope = %q, want global", scope)
	}
	if _, has := payload["instance_workspace"]; has {
		t.Fatalf("instance_workspace must be absent when no instance config is attached, got %v", out)
	}
}

func TestConfigAccess_ScopeOriginsWithInstanceMerge(t *testing.T) {
	ws := t.TempDir()
	writeInstanceConfig(t, ws, "default_mode: plan\nmax_iterations: 30\n")

	global := &config.Config{Language: "en"}
	instance := config.LoadInstanceConfig(ws)
	config.MergeInstance(global, instance)
	global.SetInstancePaths(ws)
	access := NewConfigAccess(global, t.TempDir())

	out, err := access.Get("scope.origins")
	if err != nil {
		t.Fatalf("get scope.origins: %v", err)
	}
	var payload struct {
		SaveScope         string   `json:"save_scope"`
		InstanceAttached  bool     `json:"instance_attached"`
		InstanceFields    []string `json:"instance_fields"`
		InstanceWorkspace string   `json:"instance_workspace"`
		InstanceCfgFile   string   `json:"instance_config_file"`
	}
	if err := json.Unmarshal([]byte(out), &payload); err != nil {
		t.Fatalf("scope.origins is not valid JSON: %v\nraw: %s", err, out)
	}
	if !payload.InstanceAttached {
		t.Fatalf("instance_attached = false, want true")
	}
	if payload.InstanceWorkspace != ws {
		t.Fatalf("instance_workspace = %q, want %q", payload.InstanceWorkspace, ws)
	}
	if payload.InstanceCfgFile != config.InstanceConfigPath(ws) {
		t.Fatalf("instance_config_file = %q, want %q", payload.InstanceCfgFile, config.InstanceConfigPath(ws))
	}
	got := strings.Join(payload.InstanceFields, ",")
	for _, want := range []string{"default_mode", "max_iterations"} {
		if !strings.Contains(got, want) {
			t.Fatalf("instance_fields = [%s], want to contain %q", got, want)
		}
	}

	// Per-key provenance: merged instance keys report "instance"; keys that
	// resolved from the global config or defaults report "global".
	for key, want := range map[string]string{
		"scope.origin.default_mode":   "instance",
		"scope.origin.max_iterations": "instance",
		"scope.origin.language":       "global",
		"scope.origin.vendor":         "global",
		"scope.origin.unknown_key":    "global",
	} {
		v, err := access.Get(key)
		if err != nil {
			t.Fatalf("get %s: %v", key, err)
		}
		if v != want {
			t.Fatalf("%s = %q, want %q", key, v, want)
		}
	}
}

func TestConfigAccess_ScopeOriginEmptySuffixRejected(t *testing.T) {
	access := NewConfigAccess(&config.Config{}, t.TempDir())
	if _, err := access.Get("scope.origin."); err == nil {
		t.Fatal("scope.origin. with empty suffix must return an error")
	}
}

func TestConfigAccess_ScopeKeyUnchanged(t *testing.T) {
	cfg := &config.Config{}
	if err := cfg.SetSaveScope("global"); err != nil {
		t.Fatalf("set save scope: %v", err)
	}
	access := NewConfigAccess(cfg, t.TempDir())
	v, err := access.Get("scope")
	if err != nil {
		t.Fatalf("get scope: %v", err)
	}
	if v != "global" {
		t.Fatalf("scope = %q, want global (existing write-scope contract)", v)
	}
}
