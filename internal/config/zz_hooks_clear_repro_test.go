package config

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/topcheer/ggcode/internal/hooks"
)

func TestInstanceHooksClearToEmptySilentlyLost(t *testing.T) {
	withTestHome(t)
	tmpDir := t.TempDir()
	globalPath := filepath.Join(tmpDir, "ggcode.yaml")

	if err := os.WriteFile(globalPath, []byte("language: en\n"), 0644); err != nil {
		t.Fatal(err)
	}
	workspace := t.TempDir()
	instPath := filepath.Join(InstanceDir(workspace), "ggcode.yaml")

	cfg, err := LoadWithInstance(globalPath, workspace)
	if err != nil {
		t.Fatalf("LoadWithInstance: %v", err)
	}
	cfg.Hooks.PreToolUse = []hooks.Hook{{Match: "*", Command: "echo A"}}
	if err := cfg.SaveInstance(workspace); err != nil {
		t.Fatalf("SaveInstance(set): %v", err)
	}
	instData, err := os.ReadFile(instPath)
	if err != nil || !strings.Contains(string(instData), "echo A") {
		t.Fatalf("precondition: instance file should carry hook A; err=%v data=%s", err, instData)
	}

	cfg2, err := LoadWithInstance(globalPath, workspace)
	if err != nil {
		t.Fatalf("reload: %v", err)
	}
	if len(cfg2.Hooks.PreToolUse) == 0 {
		t.Fatal("precondition: reloaded merged config should carry hook A from instance file")
	}
	cfg2.Hooks.PreToolUse = nil
	cfg2.Hooks.PostToolUse = nil
	if err := cfg2.SaveInstance(workspace); err != nil {
		t.Fatalf("SaveInstance(clear): %v", err)
	}

	instData, err = os.ReadFile(instPath)
	if err == nil {
		if strings.Contains(string(instData), "echo A") {
			t.Errorf("#4315 regression (write side): cleared hooks still on disk after SaveInstance")
		}
	} else if !os.IsNotExist(err) {
		t.Fatalf("read instance file after clear: %v", err)
	}

	cfg3, err := LoadWithInstance(globalPath, workspace)
	if err != nil {
		t.Fatalf("reload after clear: %v", err)
	}
	if len(cfg3.Hooks.PreToolUse) != 0 || len(cfg3.Hooks.PostToolUse) != 0 {
		t.Errorf("#4315 regression (read side): cleared hooks resurrected on reload: pre=%d post=%d", len(cfg3.Hooks.PreToolUse), len(cfg3.Hooks.PostToolUse))
	}
}
