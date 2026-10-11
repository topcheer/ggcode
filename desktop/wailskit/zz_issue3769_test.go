//go:build goolm

package wailskit

import (
	"os"
	"testing"
	"time"
)

// #3769: in an instance-bound workspace the runtime config is the MERGED
// view (LoadWithInstance). Two bugs broke it:
//  1. self-saves (saveWithInstanceWriteback / persistLimitChange) did not
//     noteConfigFileSaved, so the 2s poller saw our own write as an
//     external change and fired a reload;
//  2. the reload used plain config.Load, replacing the merged view with
//     the raw global file - instance overrides (vendor/language/mode) were
//     silently lost and the instance writeback broke with them.
//
// Pin A: after a self-save, a poller tick must NOT reload (mtime noted) -
// the merged default_mode survives.
// Pin B: after a REAL external change, the reload must keep the merged
// view (instance default_mode) while picking up the global-side change.

func TestIssue3769_SelfSaveDoesNotTriggerReload(t *testing.T) {
	_, _ = setupConfigTestEnv(t, "default_mode: plan\nlanguage: zh\n")
	cfg := GetGlobalConfig()
	if cfg.DefaultMode != "plan" {
		t.Fatalf("fixture: merged default_mode = %q, want plan", cfg.DefaultMode)
	}

	globalMu.Lock()
	if err := saveWithInstanceWriteback(cfg, "zai"); err != nil {
		globalMu.Unlock()
		t.Fatalf("self-save: %v", err)
	}
	// Poller tick immediately after the save - the old code reloaded here
	// (plain Load) and the instance override vanished.
	// NOTE: read the private globalCfg directly - GetGlobalConfig takes
	// globalMu and we already hold it (non-reentrant).
	syncCfgFileLocked()
	after := globalCfg
	globalMu.Unlock()

	if after.DefaultMode != "plan" {
		t.Fatalf("poller after self-save replaced merged view: default_mode=%q", after.DefaultMode)
	}
	if !after.HasInstanceConfigAttached() {
		t.Fatal("instance attachment lost after self-save + poller tick - writeback chain broken")
	}
}

func TestIssue3769_ExternalReloadKeepsMergedView(t *testing.T) {
	globalPath, _ := setupConfigTestEnv(t, "default_mode: plan\nlanguage: zh\n")
	cfg := GetGlobalConfig()
	if cfg.DefaultMode != "plan" {
		t.Fatalf("fixture: merged default_mode = %q", cfg.DefaultMode)
	}

	// Our own save first (notes the mtime), then a genuine external edit:
	// the mtime moves past the noted one.
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
	after := globalCfg // private read: we hold globalMu
	globalMu.Unlock()

	if after.DefaultMode != "plan" {
		t.Fatalf("external reload dropped instance override: default_mode=%q", after.DefaultMode)
	}
	if !after.HasInstanceConfigAttached() {
		t.Fatal("external reload lost instance attachment")
	}
}
