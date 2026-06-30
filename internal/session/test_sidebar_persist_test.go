package session

import (
	"testing"

	"github.com/topcheer/ggcode/internal/provider"
)

func TestSaveLoadSidebarAndPermissionMode(t *testing.T) {
	dir := t.TempDir()
	store, err := NewJSONLStore(dir)
	if err != nil {
		t.Fatalf("NewJSONLStore: %v", err)
	}

	visible := false
	ses := &Session{
		ID:             "test-sidebar-persist",
		Title:          "test",
		Workspace:      dir,
		PermissionMode: "bypass",
		SidebarVisible: &visible,
	}
	ses.Messages = []provider.Message{
		{Role: "user", Content: []provider.ContentBlock{{Type: "text", Text: "hello"}}},
	}

	if err := store.Save(ses); err != nil {
		t.Fatalf("Save: %v", err)
	}

	loaded, err := store.Load("test-sidebar-persist")
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if loaded.PermissionMode != "bypass" {
		t.Errorf("PermissionMode: expected bypass, got %q", loaded.PermissionMode)
	}
	if loaded.SidebarVisible == nil {
		t.Fatal("SidebarVisible: expected non-nil")
	}
	if *loaded.SidebarVisible != false {
		t.Errorf("SidebarVisible: expected false, got %v", *loaded.SidebarVisible)
	}
}
