package config

import (
	"path/filepath"
	"testing"
)

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
