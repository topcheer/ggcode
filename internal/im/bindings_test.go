package im

import (
	"os"
	"path/filepath"
	"testing"
)

// --- MemoryBindingStore ---

func TestMemoryBindingStore_SaveListDelete(t *testing.T) {
	store := NewMemoryBindingStore()

	list, err := store.ListByWorkspace("/workspace/ws1")
	if err != nil {
		t.Fatal(err)
	}
	if len(list) != 0 {
		t.Error("expected empty list for non-existent binding")
	}

	binding := ChannelBinding{
		Workspace: "/workspace/ws1",
		Platform:  PlatformQQ,
		Adapter:   "qq-bot",
		TargetID:  "user123",
	}
	if err := store.Save(binding); err != nil {
		t.Fatal(err)
	}

	loaded, err := store.ListByWorkspace("/workspace/ws1")
	if err != nil {
		t.Fatal(err)
	}
	if len(loaded) != 1 || loaded[0].TargetID != "user123" {
		t.Errorf("expected TargetID=user123, got %v", loaded)
	}

	// Also findable by adapter
	byAdapter, err := store.ListByAdapter("qq-bot")
	if err != nil {
		t.Fatal(err)
	}
	if len(byAdapter) != 1 || byAdapter[0].TargetID != "user123" {
		t.Errorf("expected TargetID=user123 via adapter, got %v", byAdapter)
	}

	list, err = store.List()
	if err != nil {
		t.Fatal(err)
	}
	if len(list) != 1 {
		t.Errorf("expected 1 binding, got %d", len(list))
	}

	if err := store.Delete("/workspace/ws1", "qq-bot"); err != nil {
		t.Fatal(err)
	}

	loaded, err = store.ListByWorkspace("/workspace/ws1")
	if err != nil {
		t.Fatal(err)
	}
	if len(loaded) != 0 {
		t.Error("expected empty after delete")
	}
}

func TestMemoryBindingStore_MultipleBindings(t *testing.T) {
	store := NewMemoryBindingStore()

	for i := 0; i < 5; i++ {
		store.Save(ChannelBinding{
			Workspace: "/ws1",
			Platform:  PlatformQQ,
			Adapter:   "bot" + string(rune('0'+i)),
			TargetID:  "target" + string(rune('0'+i)),
		})
	}

	list, err := store.List()
	if err != nil {
		t.Fatal(err)
	}
	if len(list) != 5 {
		t.Errorf("expected 5 bindings, got %d", len(list))
	}

	// All 5 should be in the same workspace
	byWorkspace, err := store.ListByWorkspace("/ws1")
	if err != nil {
		t.Fatal(err)
	}
	if len(byWorkspace) != 5 {
		t.Errorf("expected 5 bindings for /ws1, got %d", len(byWorkspace))
	}

	store.Delete("/ws1", "bot2")
	list, _ = store.List()
	if len(list) != 4 {
		t.Errorf("expected 4 bindings after delete, got %d", len(list))
	}
}

func TestMemoryBindingStore_BoundAtAutoSet(t *testing.T) {
	store := NewMemoryBindingStore()
	binding := ChannelBinding{
		Workspace: "/ws1",
		Platform:  PlatformQQ,
		Adapter:   "bot1",
	}
	store.Save(binding)

	loaded, _ := store.ListByWorkspace("/ws1")
	if len(loaded) != 1 || loaded[0].BoundAt.IsZero() {
		t.Error("expected BoundAt to be auto-set")
	}
}

// --- JSONFileBindingStore ---

func TestJSONFileBindingStore_SaveListDelete(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "bindings.json")

	store, err := NewJSONFileBindingStore(path)
	if err != nil {
		t.Fatal(err)
	}

	list, err := store.ListByWorkspace("/ws1")
	if err != nil {
		t.Fatal(err)
	}
	if len(list) != 0 {
		t.Error("expected empty for non-existent binding")
	}

	binding := ChannelBinding{
		Workspace: "/ws1",
		Platform:  PlatformQQ,
		Adapter:   "bot1",
		TargetID:  "user456",
	}
	if err := store.Save(binding); err != nil {
		t.Fatal(err)
	}

	if _, err := os.Stat(path); os.IsNotExist(err) {
		t.Error("expected bindings file to exist")
	}

	loaded, err := store.ListByWorkspace("/ws1")
	if err != nil {
		t.Fatal(err)
	}
	if len(loaded) != 1 || loaded[0].TargetID != "user456" {
		t.Errorf("expected TargetID=user456, got %v", loaded)
	}

	all, err := store.List()
	if err != nil {
		t.Fatal(err)
	}
	if len(all) != 1 {
		t.Errorf("expected 1 binding, got %d", len(all))
	}

	if err := store.Delete("/ws1", "bot1"); err != nil {
		t.Fatal(err)
	}

	loaded, _ = store.ListByWorkspace("/ws1")
	if len(loaded) != 0 {
		t.Error("expected empty after delete")
	}
}

func TestJSONFileBindingStore_BoundAtAutoSet(t *testing.T) {
	dir := t.TempDir()
	store, _ := NewJSONFileBindingStore(filepath.Join(dir, "bindings.json"))

	binding := ChannelBinding{
		Workspace: "/ws1",
		Platform:  PlatformQQ,
		Adapter:   "bot1",
	}
	store.Save(binding)

	loaded, _ := store.ListByWorkspace("/ws1")
	if len(loaded) != 1 || loaded[0].BoundAt.IsZero() {
		t.Error("expected BoundAt to be auto-set")
	}
}

func TestJSONFileBindingStore_InvalidJSON(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "bindings.json")

	os.WriteFile(path, []byte("{invalid json}"), 0o600)

	store, _ := NewJSONFileBindingStore(path)
	_, err := store.List()
	if err == nil {
		t.Error("expected error for invalid JSON")
	}
}

func TestJSONFileBindingStore_MultipleBindings(t *testing.T) {
	dir := t.TempDir()
	store, _ := NewJSONFileBindingStore(filepath.Join(dir, "bindings.json"))

	for i := 0; i < 3; i++ {
		store.Save(ChannelBinding{
			Workspace: "/ws1",
			Platform:  PlatformQQ,
			Adapter:   "bot" + string(rune('A'+i)),
			TargetID:  "target" + string(rune('A'+i)),
		})
	}

	list, _ := store.List()
	if len(list) != 3 {
		t.Errorf("expected 3 bindings, got %d", len(list))
	}

	store.Delete("/ws1", "botB")
	list, _ = store.List()
	if len(list) != 2 {
		t.Errorf("expected 2 bindings after delete, got %d", len(list))
	}
}

func TestJSONFileBindingStore_LegacyMigration(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "bindings.json")

	// Write a legacy format: keys are workspace-only (no \x00 separator)
	legacy := `{
  "/ws1": {
    "Workspace": "/ws1",
    "Platform": "qq",
    "Adapter": "qq-bot",
    "TargetID": "user1",
    "BoundAt": "2025-01-01T00:00:00Z"
  }
}`
	if err := os.WriteFile(path, []byte(legacy), 0o600); err != nil {
		t.Fatal(err)
	}

	store, err := NewJSONFileBindingStore(path)
	if err != nil {
		t.Fatal(err)
	}

	// Should auto-migrate and be findable by adapter
	byAdapter, err := store.ListByAdapter("qq-bot")
	if err != nil {
		t.Fatal(err)
	}
	if len(byAdapter) != 1 || byAdapter[0].TargetID != "user1" {
		t.Errorf("expected legacy binding to migrate and be found, got %v", byAdapter)
	}
}

// --- normalizeWorkspace ---

func TestNormalizeWorkspace(t *testing.T) {
	result := normalizeWorkspace("/test/path")
	if result == "" {
		t.Error("expected non-empty result")
	}
}

// --- DefaultBindingsPath ---

func TestDefaultBindingsPath(t *testing.T) {
	path, err := DefaultBindingsPath()
	if err != nil {
		t.Fatal(err)
	}
	if path == "" {
		t.Error("expected non-empty path")
	}
}

func TestCompositeKey(t *testing.T) {
	key := compositeKey("/ws1", "bot1")
	ws, adapter := splitCompositeKey(key)
	if ws != normalizeWorkspace("/ws1") {
		t.Errorf("expected workspace %q, got %q", normalizeWorkspace("/ws1"), ws)
	}
	if adapter != "bot1" {
		t.Errorf("expected adapter bot1, got %q", adapter)
	}
}
