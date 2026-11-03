//go:build goolm

package wailskit

import (
	"os"
	"testing"
	"time"
)

func TestIssue3769_SelfSaveDoesNotTriggerReload(t *testing.T) {
	_, _ = setupConfigTestEnv(t, "default_mode: bypass\nlanguage: zh\n")
	cfg := GetGlobalConfig()
	if cfg.DefaultMode != "bypass" {
		t.Fatalf("fixture: merged default_mode = %q, want plan", cfg.DefaultMode)
	}

	globalMu.Lock()
	if err := saveWithInstanceWriteback(cfg, "zai"); err != nil {
		globalMu.Unlock()
		t.Fatalf("self-save: %v", err)
	}

	syncCfgFileLocked()
	after := globalCfg
	globalMu.Unlock()

	if after.DefaultMode != "bypass" {
		t.Fatalf("poller after self-save replaced merged view: default_mode=%q", after.DefaultMode)
	}
	if !after.HasInstanceConfigAttached() {
		t.Fatal("instance attachment lost after self-save + poller tick - writeback chain broken")
	}
}

func TestIssue3769_ExternalReloadKeepsMergedView(t *testing.T) {
	globalPath, _ := setupConfigTestEnv(t, "default_mode: bypass\nlanguage: zh\n")
	cfg := GetGlobalConfig()
	if cfg.DefaultMode != "bypass" {
		t.Fatalf("fixture: merged default_mode = %q", cfg.DefaultMode)
	}

	globalMu.Lock()
	if err := saveWithInstanceWriteback(cfg, "zai"); err != nil {
		globalMu.Unlock()
		t.Fatalf("self-save: %v", err)
	}
	globalMu.Unlock()

	if err := os.Chtimes(globalPath, time.Now().Add(2*time.Second), time.Now().Add(2*time.Second)); err != nil {
		t.Fatalf("chtimes: %v", err)
	}

	globalMu.Lock()
	syncCfgFileLocked()
	after := globalCfg
	globalMu.Unlock()

	if after.DefaultMode != "bypass" {
		t.Fatalf("external reload dropped instance override: default_mode=%q", after.DefaultMode)
	}
	if !after.HasInstanceConfigAttached() {
		t.Fatal("external reload lost instance attachment")
	}
}
