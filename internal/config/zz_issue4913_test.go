package config

import (
	"path/filepath"
	"testing"
)

// #4913: creating a bot means the user wants IM to work. AddIMAdapter used
// to persist only the adapter map - the global im.enabled switch stayed
// memory-only, so the bot worked this session and silently failed to start
// after restart (daemon/root gate on the DISK config).
func TestIssue4913_AddIMAdapterPersistsGlobalEnable(t *testing.T) {
	withTestHome(t)
	path := filepath.Join(t.TempDir(), "ggcode.yaml")
	cfg := DefaultConfig()
	cfg.FilePath = path
	if err := cfg.Save(); err != nil {
		t.Fatalf("Save returned error: %v", err)
	}
	if cfg.IM.Enabled {
		t.Fatal("precondition: fresh config must have IM disabled")
	}
	if err := cfg.AddIMAdapter("qq-main", IMAdapterConfig{
		Enabled:  true,
		Platform: "qq",
	}); err != nil {
		t.Fatalf("AddIMAdapter returned error: %v", err)
	}
	reloaded, err := Load(path)
	if err != nil {
		t.Fatalf("Load returned error: %v", err)
	}
	if !reloaded.IM.Enabled {
		t.Fatal("#4913: after bot creation, reloaded config must have im.enabled=true - the restart gate reads the disk")
	}
	if _, ok := reloaded.IM.Adapters["qq-main"]; !ok {
		t.Fatalf("adapter must also persist, got %#v", reloaded.IM.Adapters)
	}
}

// Rollback symmetry (#2797/#4486): removing the adapter must NOT flip the
// global switch off - other adapters may still be live.
func TestIssue4913_RemoveIMAdapterKeepsGlobalEnable(t *testing.T) {
	withTestHome(t)
	path := filepath.Join(t.TempDir(), "ggcode.yaml")
	cfg := DefaultConfig()
	cfg.FilePath = path
	if err := cfg.Save(); err != nil {
		t.Fatalf("Save returned error: %v", err)
	}
	if err := cfg.AddIMAdapter("qq-main", IMAdapterConfig{Enabled: true, Platform: "qq"}); err != nil {
		t.Fatalf("AddIMAdapter returned error: %v", err)
	}
	if err := cfg.RemoveIMAdapter("qq-main"); err != nil {
		t.Fatalf("RemoveIMAdapter returned error: %v", err)
	}
	reloaded, err := Load(path)
	if err != nil {
		t.Fatalf("Load returned error: %v", err)
	}
	if !reloaded.IM.Enabled {
		t.Fatal("removing one adapter must keep the global switch on (other adapters may be live)")
	}
}
