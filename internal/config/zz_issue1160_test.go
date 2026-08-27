package config

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// #1160: patchExternalFile and PatchIMAdapter used to swallow
// yaml.Unmarshal errors when reading external files (im.yaml and friends).
// A corrupted file decoded into an empty map, the patch was applied to that
// empty map, and the result atomically replaced the user's file - silently
// dropping every existing entry. The Unmarshal error must propagate and the
// file must be left untouched.

const corruptYAML = "adapters:\n  qq:\n    enabled: true\n\tding: broken-tab\n"

// TestIssue1160PatchIMAdapterRejectsCorruptFile verifies that patching with a
// corrupted im.yaml returns an error and does NOT rewrite the file (the
// pre-existing qq adapter must survive byte-for-byte).
func TestIssue1160PatchIMAdapterRejectsCorruptFile(t *testing.T) {
	withTestHome(t)
	tmpDir := t.TempDir()
	cfgPath := filepath.Join(tmpDir, "ggcode.yaml")

	cfg, err := Load(cfgPath)
	if err != nil {
		t.Fatalf("Load error: %v", err)
	}

	imPath := IMPath(tmpDir)
	corrupt := "adapters:\n  qq:\n    enabled: true\n    env:\n      appid: \"12345\"\n\tding: broken\n"
	if werr := os.WriteFile(imPath, []byte(corrupt), 0o600); werr != nil {
		t.Fatalf("writing corrupt im.yaml: %v", werr)
	}

	perr := cfg.PatchIMAdapter("ding", func(adapter map[string]interface{}) {
		adapter["enabled"] = true
	})
	if perr == nil {
		t.Fatal("PatchIMAdapter on corrupted im.yaml: expected error, got nil")
	}
	if !strings.Contains(perr.Error(), "parsing im config") {
		t.Fatalf("unexpected error type: %v", perr)
	}

	data, rerr := os.ReadFile(imPath)
	if rerr != nil {
		t.Fatalf("reading back im.yaml: %v", rerr)
	}
	if string(data) != corrupt {
		t.Fatalf("im.yaml must be left untouched after failed patch.\nwant:\n%s\ngot:\n%s", corrupt, string(data))
	}
	if !strings.Contains(string(data), "qq") {
		t.Error("existing qq adapter lost from im.yaml")
	}
}

// TestIssue1160PatchExternalFileRejectsCorruptFile verifies the generic
// external-file path (mcp_servers.yaml) behaves the same way.
func TestIssue1160PatchExternalFileRejectsCorruptFile(t *testing.T) {
	withTestHome(t)
	tmpDir := t.TempDir()
	cfgPath := filepath.Join(tmpDir, "ggcode.yaml")

	cfg, err := Load(cfgPath)
	if err != nil {
		t.Fatalf("Load error: %v", err)
	}

	mcpPath := filepath.Join(tmpDir, "mcp_servers.yaml")
	corrupt := "servers:\n  echo:\n    command: /bin/echo\n\tservers2: [unclosed\n"
	if werr := os.WriteFile(mcpPath, []byte(corrupt), 0o600); werr != nil {
		t.Fatalf("writing corrupt mcp_servers.yaml: %v", werr)
	}

	perr := cfg.patchExternalFile("mcp_servers", func(raw map[string]interface{}) {
		raw["injected"] = map[string]interface{}{"evil": true}
	})
	if perr == nil {
		t.Fatal("patchExternalFile on corrupted mcp_servers.yaml: expected error, got nil")
	}
	if !strings.Contains(perr.Error(), "parsing mcp_servers") {
		t.Fatalf("unexpected error type: %v", perr)
	}

	data, rerr := os.ReadFile(mcpPath)
	if rerr != nil {
		t.Fatalf("reading back mcp_servers.yaml: %v", rerr)
	}
	if string(data) != corrupt {
		t.Fatalf("mcp_servers.yaml must be left untouched after failed patch.\nwant:\n%s\ngot:\n%s", corrupt, string(data))
	}
}

// TestIssue1160PatchStillWorksOnValidFile guards the happy path: a valid
// im.yaml must still receive patches without losing existing adapters.
func TestIssue1160PatchStillWorksOnValidFile(t *testing.T) {
	withTestHome(t)
	tmpDir := t.TempDir()
	cfgPath := filepath.Join(tmpDir, "ggcode.yaml")

	cfg, err := Load(cfgPath)
	if err != nil {
		t.Fatalf("Load error: %v", err)
	}

	imPath := IMPath(tmpDir)
	valid := "adapters:\n  qq:\n    enabled: true\n"
	if werr := os.WriteFile(imPath, []byte(valid), 0o600); werr != nil {
		t.Fatalf("writing valid im.yaml: %v", werr)
	}

	if perr := cfg.PatchIMAdapter("ding", func(adapter map[string]interface{}) {
		adapter["enabled"] = true
	}); perr != nil {
		t.Fatalf("PatchIMAdapter on valid im.yaml should succeed: %v", perr)
	}

	data, rerr := os.ReadFile(imPath)
	if rerr != nil {
		t.Fatalf("reading back im.yaml: %v", rerr)
	}
	content := string(data)
	if !strings.Contains(content, "qq") || !strings.Contains(content, "ding") {
		t.Fatalf("im.yaml should contain qq preserved plus new ding entry:\n%s", content)
	}
}
