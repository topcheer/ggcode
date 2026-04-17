package main

import (
	"bytes"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/topcheer/ggcode/internal/config"
	"github.com/topcheer/ggcode/internal/im"
)

// ============================================================
// im config add / remove / show / set
// ============================================================

func TestIMConfigAdd(t *testing.T) {
	dir := t.TempDir()
	cfgPath := filepath.Join(dir, "ggcode.yaml")

	// Create minimal config
	writeTestConfig(t, cfgPath, nil)

	buf := &bytes.Buffer{}
	cmd := newIMConfigCmd(ptr(cfgPath))
	cmd.SetOut(buf)
	cmd.SetArgs([]string{"add", "my-qq", "--platform", "qq", "--extra", "app_id=cli_123", "--extra", "app_secret=sss"})
	if err := cmd.Execute(); err != nil {
		t.Fatalf("add: %v", err)
	}
	if !strings.Contains(buf.String(), "Added IM adapter") {
		t.Errorf("unexpected output: %s", buf.String())
	}

	// Verify it was saved
	cfg, err := config.Load(cfgPath)
	if err != nil {
		t.Fatalf("reload: %v", err)
	}
	if cfg.IM.Adapters == nil {
		t.Fatal("adapters is nil")
	}
	a, ok := cfg.IM.Adapters["my-qq"]
	if !ok {
		t.Fatal("my-qq not found")
	}
	if a.Platform != "qq" {
		t.Errorf("platform = %q", a.Platform)
	}
	if !a.Enabled {
		t.Error("should be enabled by default")
	}
	if v, _ := a.Extra["app_id"].(string); v != "cli_123" {
		t.Errorf("extra.app_id = %q", v)
	}
}

func TestIMConfigAddDuplicateFails(t *testing.T) {
	dir := t.TempDir()
	cfgPath := filepath.Join(dir, "ggcode.yaml")
	writeTestConfig(t, cfgPath, map[string]config.IMAdapterConfig{
		"my-qq": {Enabled: true, Platform: "qq"},
	})

	cmd := newIMConfigCmd(ptr(cfgPath))
	cmd.SetArgs([]string{"add", "my-qq", "--platform", "qq"})
	if err := cmd.Execute(); err == nil {
		t.Error("expected error for duplicate adapter")
	}
}

func TestIMConfigAddMissingPlatformFails(t *testing.T) {
	dir := t.TempDir()
	cfgPath := filepath.Join(dir, "ggcode.yaml")
	writeTestConfig(t, cfgPath, nil)

	cmd := newIMConfigCmd(ptr(cfgPath))
	cmd.SetArgs([]string{"add", "my-tg"})
	if err := cmd.Execute(); err == nil {
		t.Error("expected error for missing platform")
	}
}

func TestIMConfigRemove(t *testing.T) {
	dir := t.TempDir()
	cfgPath := filepath.Join(dir, "ggcode.yaml")
	writeTestConfig(t, cfgPath, map[string]config.IMAdapterConfig{
		"my-qq": {Enabled: true, Platform: "qq"},
		"my-tg": {Enabled: true, Platform: "telegram"},
	})

	buf := &bytes.Buffer{}
	cmd := newIMConfigCmd(ptr(cfgPath))
	cmd.SetOut(buf)
	cmd.SetArgs([]string{"remove", "my-qq"})
	if err := cmd.Execute(); err != nil {
		t.Fatalf("remove: %v", err)
	}
	if !strings.Contains(buf.String(), "Removed") {
		t.Errorf("unexpected output: %s", buf.String())
	}

	cfg, _ := config.Load(cfgPath)
	if _, ok := cfg.IM.Adapters["my-qq"]; ok {
		t.Error("my-qq should be removed")
	}
	if _, ok := cfg.IM.Adapters["my-tg"]; !ok {
		t.Error("my-tg should still exist")
	}
}

func TestIMConfigRemoveNotFound(t *testing.T) {
	dir := t.TempDir()
	cfgPath := filepath.Join(dir, "ggcode.yaml")
	writeTestConfig(t, cfgPath, nil)

	cmd := newIMConfigCmd(ptr(cfgPath))
	cmd.SetArgs([]string{"remove", "nonexistent"})
	if err := cmd.Execute(); err == nil {
		t.Error("expected error for nonexistent adapter")
	}
}

func TestIMConfigShow(t *testing.T) {
	dir := t.TempDir()
	cfgPath := filepath.Join(dir, "ggcode.yaml")
	writeTestConfig(t, cfgPath, map[string]config.IMAdapterConfig{
		"my-feishu": {
			Enabled:  true,
			Platform: "feishu",
			Extra: map[string]interface{}{
				"app_id":     "cli_abc",
				"app_secret": "secret123",
			},
		},
	})

	buf := &bytes.Buffer{}
	cmd := newIMConfigCmd(ptr(cfgPath))
	cmd.SetOut(buf)
	cmd.SetArgs([]string{"show", "my-feishu"})
	if err := cmd.Execute(); err != nil {
		t.Fatalf("show: %v", err)
	}
	output := buf.String()
	if !strings.Contains(output, "feishu") {
		t.Errorf("missing platform in output: %s", output)
	}
	if !strings.Contains(output, "cli_abc") {
		t.Errorf("missing app_id in output: %s", output)
	}
	// Secret should be masked
	if strings.Contains(output, "secret123") {
		t.Errorf("secret should be masked in output: %s", output)
	}
}

func TestIMConfigShowJSON(t *testing.T) {
	dir := t.TempDir()
	cfgPath := filepath.Join(dir, "ggcode.yaml")
	writeTestConfig(t, cfgPath, map[string]config.IMAdapterConfig{
		"my-qq": {Enabled: true, Platform: "qq"},
	})

	buf := &bytes.Buffer{}
	cmd := newIMConfigCmd(ptr(cfgPath))
	cmd.SetOut(buf)
	cmd.SetArgs([]string{"show", "my-qq", "--json"})
	if err := cmd.Execute(); err != nil {
		t.Fatalf("show --json: %v", err)
	}

	var result map[string]interface{}
	if err := json.Unmarshal(buf.Bytes(), &result); err != nil {
		t.Fatalf("invalid JSON: %v", err)
	}
	// YAML may decode into struct fields with uppercase keys
	if fmt.Sprintf("%v", result["Platform"]) != "qq" {
		t.Errorf("Platform = %v, want qq (full result: %v)", result["Platform"], result)
	}
}

func TestIMConfigSetEnabled(t *testing.T) {
	dir := t.TempDir()
	cfgPath := filepath.Join(dir, "ggcode.yaml")
	writeTestConfig(t, cfgPath, map[string]config.IMAdapterConfig{
		"my-qq": {Enabled: true, Platform: "qq"},
	})

	buf := &bytes.Buffer{}
	cmd := newIMConfigCmd(ptr(cfgPath))
	cmd.SetOut(buf)
	cmd.SetArgs([]string{"set", "my-qq", "enabled", "false"})
	if err := cmd.Execute(); err != nil {
		t.Fatalf("set: %v", err)
	}

	cfg, _ := config.Load(cfgPath)
	if cfg.IM.Adapters["my-qq"].Enabled {
		t.Error("expected enabled=false")
	}
}

func TestIMConfigSetExtra(t *testing.T) {
	dir := t.TempDir()
	cfgPath := filepath.Join(dir, "ggcode.yaml")
	writeTestConfig(t, cfgPath, map[string]config.IMAdapterConfig{
		"my-qq": {
			Enabled:  true,
			Platform: "qq",
			Extra:    map[string]interface{}{"token": "old_token"},
		},
	})

	buf := &bytes.Buffer{}
	cmd := newIMConfigCmd(ptr(cfgPath))
	cmd.SetOut(buf)
	cmd.SetArgs([]string{"set", "my-qq", "extra.token", "new_token"})
	if err := cmd.Execute(); err != nil {
		t.Fatalf("set extra: %v", err)
	}

	cfg, _ := config.Load(cfgPath)
	v, _ := cfg.IM.Adapters["my-qq"].Extra["token"].(string)
	if v != "new_token" {
		t.Errorf("token = %q, want new_token", v)
	}
}

// ============================================================
// im list
// ============================================================

func TestIMListEmpty(t *testing.T) {
	dir := t.TempDir()
	cfgPath := filepath.Join(dir, "ggcode.yaml")
	writeTestConfig(t, cfgPath, nil)

	buf := &bytes.Buffer{}
	cmd := newIMListCmd(ptr(cfgPath))
	cmd.SetOut(buf)
	if err := cmd.Execute(); err != nil {
		t.Fatalf("list: %v", err)
	}
	if !strings.Contains(buf.String(), "No IM adapters") {
		t.Errorf("unexpected output: %s", buf.String())
	}
}

func TestIMListWithAdapters(t *testing.T) {
	dir := t.TempDir()
	cfgPath := filepath.Join(dir, "ggcode.yaml")
	writeTestConfig(t, cfgPath, map[string]config.IMAdapterConfig{
		"my-qq":     {Enabled: true, Platform: "qq"},
		"my-feishu": {Enabled: false, Platform: "feishu"},
	})

	buf := &bytes.Buffer{}
	cmd := newIMListCmd(ptr(cfgPath))
	cmd.SetOut(buf)
	if err := cmd.Execute(); err != nil {
		t.Fatalf("list: %v", err)
	}
	output := buf.String()
	if !strings.Contains(output, "my-qq") || !strings.Contains(output, "my-feishu") {
		t.Errorf("missing adapters in output: %s", output)
	}
}

func TestIMListJSON(t *testing.T) {
	dir := t.TempDir()
	cfgPath := filepath.Join(dir, "ggcode.yaml")
	writeTestConfig(t, cfgPath, map[string]config.IMAdapterConfig{
		"my-qq": {Enabled: true, Platform: "qq"},
	})

	buf := &bytes.Buffer{}
	cmd := newIMListCmd(ptr(cfgPath))
	cmd.SetOut(buf)
	cmd.SetArgs([]string{"--json"})
	if err := cmd.Execute(); err != nil {
		t.Fatalf("list --json: %v", err)
	}

	var result []map[string]interface{}
	if err := json.Unmarshal(buf.Bytes(), &result); err != nil {
		t.Fatalf("invalid JSON: %v", err)
	}
	if len(result) != 1 || result[0]["name"] != "my-qq" {
		t.Errorf("unexpected result: %v", result)
	}
}

// ============================================================
// im bind / unbind
// ============================================================

func TestIMBindAndUnbind(t *testing.T) {
	dir := t.TempDir()
	cfgPath := filepath.Join(dir, "ggcode.yaml")
	writeTestConfig(t, cfgPath, map[string]config.IMAdapterConfig{
		"my-qq": {Enabled: true, Platform: "qq"},
	})

	// Override bindings path to temp dir
	bindingsDir := t.TempDir()
	origPath := resolveBindingsPath
	t.Cleanup(func() { resolveBindingsPath = origPath })
	resolveBindingsPath = func() (string, error) { return filepath.Join(bindingsDir, "bindings.json"), nil }

	workspace := t.TempDir()

	// Bind
	buf := &bytes.Buffer{}
	cmd := newIMBindCmd(ptr(cfgPath))
	cmd.SetOut(buf)
	cmd.SetArgs([]string{"my-qq", "--channel", "123456", "--workspace", workspace})
	if err := cmd.Execute(); err != nil {
		t.Fatalf("bind: %v", err)
	}
	if !strings.Contains(buf.String(), "Bound") {
		t.Errorf("unexpected output: %s", buf.String())
	}

	// Verify binding was written
	store, err := im.NewJSONFileBindingStore(filepath.Join(bindingsDir, "bindings.json"))
	if err != nil {
		t.Fatalf("open store: %v", err)
	}
	bindings, err := store.ListByAdapter("my-qq")
	if err != nil {
		t.Fatalf("list: %v", err)
	}
	if len(bindings) != 1 {
		t.Fatalf("expected 1 binding, got %d", len(bindings))
	}
	if bindings[0].ChannelID != "123456" {
		t.Errorf("channel = %q", bindings[0].ChannelID)
	}

	// Unbind by adapter
	buf.Reset()
	cmd2 := newIMUnbindCmd(ptr(cfgPath))
	cmd2.SetOut(buf)
	cmd2.SetArgs([]string{"my-qq"})
	if err := cmd2.Execute(); err != nil {
		t.Fatalf("unbind: %v", err)
	}
	if !strings.Contains(buf.String(), "Unbound") {
		t.Errorf("unexpected output: %s", buf.String())
	}

	// Verify removed
	bindings, _ = store.ListByAdapter("my-qq")
	if len(bindings) != 0 {
		t.Errorf("expected 0 bindings after unbind, got %d", len(bindings))
	}
}

func TestIMBindMissingChannelFails(t *testing.T) {
	dir := t.TempDir()
	cfgPath := filepath.Join(dir, "ggcode.yaml")
	writeTestConfig(t, cfgPath, map[string]config.IMAdapterConfig{
		"my-qq": {Enabled: true, Platform: "qq"},
	})

	cmd := newIMBindCmd(ptr(cfgPath))
	cmd.SetArgs([]string{"my-qq"})
	if err := cmd.Execute(); err == nil {
		t.Error("expected error for missing --channel")
	}
}

func TestIMBindUnknownAdapterFails(t *testing.T) {
	dir := t.TempDir()
	cfgPath := filepath.Join(dir, "ggcode.yaml")
	writeTestConfig(t, cfgPath, nil)

	cmd := newIMBindCmd(ptr(cfgPath))
	cmd.SetArgs([]string{"nonexistent", "--channel", "123"})
	if err := cmd.Execute(); err == nil {
		t.Error("expected error for unknown adapter")
	}
}

func TestIMUnbindAll(t *testing.T) {
	dir := t.TempDir()
	cfgPath := filepath.Join(dir, "ggcode.yaml")
	writeTestConfig(t, cfgPath, map[string]config.IMAdapterConfig{
		"my-qq":     {Enabled: true, Platform: "qq"},
		"my-feishu": {Enabled: true, Platform: "feishu"},
	})

	bindingsDir := t.TempDir()
	origPath := resolveBindingsPath
	t.Cleanup(func() { resolveBindingsPath = origPath })
	resolveBindingsPath = func() (string, error) { return filepath.Join(bindingsDir, "bindings.json"), nil }

	store, _ := im.NewJSONFileBindingStore(filepath.Join(bindingsDir, "bindings.json"))
	_ = store.Save(im.ChannelBinding{Workspace: "/tmp/w1", Adapter: "my-qq", ChannelID: "c1", Platform: im.PlatformQQ})
	_ = store.Save(im.ChannelBinding{Workspace: "/tmp/w1", Adapter: "my-feishu", ChannelID: "c2", Platform: im.PlatformFeishu})

	buf := &bytes.Buffer{}
	cmd := newIMUnbindCmd(ptr(cfgPath))
	cmd.SetOut(buf)
	cmd.SetArgs([]string{"--all"})
	if err := cmd.Execute(); err != nil {
		t.Fatalf("unbind --all: %v", err)
	}
	if !strings.Contains(buf.String(), "Unbound all") {
		t.Errorf("unexpected output: %s", buf.String())
	}

	bindings, _ := store.List()
	if len(bindings) != 0 {
		t.Errorf("expected 0 bindings, got %d", len(bindings))
	}
}

// ============================================================
// im bindings
// ============================================================

func TestIMBindingsEmpty(t *testing.T) {
	bindingsDir := t.TempDir()
	origPath := resolveBindingsPath
	t.Cleanup(func() { resolveBindingsPath = origPath })
	resolveBindingsPath = func() (string, error) { return filepath.Join(bindingsDir, "bindings.json"), nil }

	cfgPath := filepath.Join(t.TempDir(), "ggcode.yaml")
	writeTestConfig(t, cfgPath, nil)

	buf := &bytes.Buffer{}
	cmd := newIMBindingsCmd(ptr(cfgPath))
	cmd.SetOut(buf)
	if err := cmd.Execute(); err != nil {
		t.Fatalf("bindings: %v", err)
	}
	if !strings.Contains(buf.String(), "No bindings") {
		t.Errorf("unexpected output: %s", buf.String())
	}
}

// ============================================================
// helpers
// ============================================================

func ptr(s string) *string { return &s }

func writeTestConfig(t *testing.T, path string, adapters map[string]config.IMAdapterConfig) {
	t.Helper()
	// Write a valid config using YAML (must pass Validate: vendor must exist)
	var yamlParts []string
	yamlParts = append(yamlParts, "vendor: zai")
	yamlParts = append(yamlParts, "endpoint: test")
	yamlParts = append(yamlParts, "model: test-model")
	if len(adapters) > 0 {
		yamlParts = append(yamlParts, "im:")
		yamlParts = append(yamlParts, "  adapters:")
		for name, a := range adapters {
			yamlParts = append(yamlParts, fmt.Sprintf("    %s:", name))
			yamlParts = append(yamlParts, fmt.Sprintf("      platform: %s", a.Platform))
			yamlParts = append(yamlParts, fmt.Sprintf("      enabled: %v", a.Enabled))
			if len(a.Extra) > 0 {
				yamlParts = append(yamlParts, "      extra:")
				for k, v := range a.Extra {
					yamlParts = append(yamlParts, fmt.Sprintf("        %s: %v", k, v))
				}
			}
		}
	}
	// Add a minimal vendor so Validate passes
	yamlParts = append(yamlParts, "vendors:")
	yamlParts = append(yamlParts, "  zai:")
	yamlParts = append(yamlParts, "    endpoints:")
	yamlParts = append(yamlParts, "      test:")
	yamlParts = append(yamlParts, "        protocol: openai")
	yamlParts = append(yamlParts, "        base_url: http://localhost:0")
	yamlParts = append(yamlParts, "        models:")
	yamlParts = append(yamlParts, "          - test-model")

	content := strings.Join(yamlParts, "\n") + "\n"
	if err := os.WriteFile(path, []byte(content), 0644); err != nil {
		t.Fatalf("write config: %v", err)
	}
}
