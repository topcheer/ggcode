package wailskit

import (
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/topcheer/ggcode/internal/config"
)

// #4497 pin: SetConfig switching workspaces must re-baseline the #1847 sync
// watcher on the new workspace's config file. The sync.Once in
// startConfigFileSync only ever stats the FIRST workspace's file; before the
// fix the poller compared the new path's mtime against the OLD file's
// baseline, so an external edit whose mtime did not exceed the old baseline
// never triggered a reload - and the next desktop save clobbered it with the
// stale in-memory snapshot.
func TestIssue4497_SetConfigRebaselinesOnWorkspaceSwitch(t *testing.T) {
	dir := t.TempDir()
	pathA := filepath.Join(dir, "a-ggcode.yaml")
	pathB := filepath.Join(dir, "b-ggcode.yaml")
	vendorBlockA := "vendor: va\nendpoint: main\ndefault_mode: auto\nvendors:\n  va:\n    endpoints:\n      main:\n        base_url: https://example.com\n        protocol: openai\n"
	vendorBlockB := "vendor: vb\nendpoint: main\ndefault_mode: bypass\nvendors:\n  vb:\n    endpoints:\n      main:\n        base_url: https://example.com\n        protocol: openai\n"
	if err := os.WriteFile(pathA, []byte(vendorBlockA), 0644); err != nil {
		t.Fatalf("write a: %v", err)
	}
	if err := os.WriteFile(pathB, []byte(vendorBlockB), 0644); err != nil {
		t.Fatalf("write b: %v", err)
	}
	// The trap: workspace A's baseline is in the FUTURE, workspace B's file
	// is in the past. Under the old code, no realistic external edit to B
	// could ever beat A's baseline.
	now := time.Now()
	future := now.Add(1 * time.Hour)
	past := now.Add(-1 * time.Hour)
	// Load BEFORE pinning mtimes: Load normalizes and writes the file
	// back, which would otherwise refresh the mtime we are about to pin.
	cfgA, err := config.Load(pathA)
	if err != nil {
		t.Fatalf("load a: %v", err)
	}
	cfgB, err := config.Load(pathB)
	if err != nil {
		t.Fatalf("load b: %v", err)
	}
	if err := os.Chtimes(pathA, future, future); err != nil {
		t.Fatalf("chtimes a: %v", err)
	}
	if err := os.Chtimes(pathB, past, past); err != nil {
		t.Fatalf("chtimes b: %v", err)
	}

	SetConfig(cfgA)
	globalMu.RLock()
	baselineA := cfgFileLastMod
	globalMu.RUnlock()
	if !baselineA.Equal(future) {
		t.Fatalf("baseline after first SetConfig = %v, want A's mtime %v", baselineA, future)
	}

	// Switch to workspace B: the baseline must move to B's file mtime.
	SetConfig(cfgB)
	globalMu.RLock()
	baselineB := cfgFileLastMod
	globalMu.RUnlock()
	if !baselineB.Equal(past) {
		t.Fatalf("baseline after workspace switch = %v, want B's mtime %v (old baseline kept: #4497)", baselineB, past)
	}

	// External session patches B with an mtime that only beats B's fresh
	// baseline - never A's future baseline (this is the exact no-reload trap).
	mid := now.Add(30 * time.Minute)
	if err := os.WriteFile(pathB, []byte("vendor: vb\nendpoint: main\ndefault_mode: supervised\nvendors:\n  vb:\n    endpoints:\n      main:\n        base_url: https://example.com\n        protocol: openai\n"), 0644); err != nil {
		t.Fatalf("external write b: %v", err)
	}
	if err := os.Chtimes(pathB, mid, mid); err != nil {
		t.Fatalf("chtimes b (external): %v", err)
	}
	globalMu.Lock()
	syncCfgFileLocked()
	globalMu.Unlock()

	if got := GetGlobalConfig().DefaultMode; got != "supervised" {
		t.Fatalf("external edit after workspace switch must trigger reload, default_mode=%q (stale baseline suppressed #1847 sync)", got)
	}
}
