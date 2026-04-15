package im

import (
	"os"
	"path/filepath"
	"testing"
)

// --- MemoryBindingStore ---

func TestMemoryBindingStore_SaveLoadDelete(t *testing.T) {
	store := NewMemoryBindingStore()

	b, err := store.Load("/workspace/ws1")
	if err != nil {
		t.Fatal(err)
	}
	if b != nil {
		t.Error("expected nil for non-existent binding")
	}

	binding := ChannelBinding{
		Workspace: "/workspace/ws1",
		Platform:  PlatformQQ,
		TargetID:  "user123",
	}
	if err := store.Save(binding); err != nil {
		t.Fatal(err)
	}

	loaded, err := store.Load("/workspace/ws1")
	if err != nil {
		t.Fatal(err)
	}
	if loaded == nil || loaded.TargetID != "user123" {
		t.Errorf("expected TargetID=user123, got %v", loaded)
	}

	list, err := store.List()
	if err != nil {
		t.Fatal(err)
	}
	if len(list) != 1 {
		t.Errorf("expected 1 binding, got %d", len(list))
	}

	if err := store.Delete("/workspace/ws1"); err != nil {
		t.Fatal(err)
	}

	b, err = store.Load("/workspace/ws1")
	if err != nil {
		t.Fatal(err)
	}
	if b != nil {
		t.Error("expected nil after delete")
	}
}

func TestMemoryBindingStore_MultipleBindings(t *testing.T) {
	store := NewMemoryBindingStore()

	for i := 0; i < 5; i++ {
		store.Save(ChannelBinding{
			Workspace: "/ws" + string(rune('0'+i)),
			Platform:  PlatformQQ,
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

	store.Delete("/ws2")
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
	}
	store.Save(binding)

	loaded, _ := store.Load("/ws1")
	if loaded.BoundAt.IsZero() {
		t.Error("expected BoundAt to be auto-set")
	}
}

// --- JSONFileBindingStore ---

func TestJSONFileBindingStore_SaveLoadDelete(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "bindings.json")

	store, err := NewJSONFileBindingStore(path)
	if err != nil {
		t.Fatal(err)
	}

	b, err := store.Load("/ws1")
	if err != nil {
		t.Fatal(err)
	}
	if b != nil {
		t.Error("expected nil for non-existent binding")
	}

	binding := ChannelBinding{
		Workspace: "/ws1",
		Platform:  PlatformQQ,
		TargetID:  "user456",
	}
	if err := store.Save(binding); err != nil {
		t.Fatal(err)
	}

	if _, err := os.Stat(path); os.IsNotExist(err) {
		t.Error("expected bindings file to exist")
	}

	loaded, err := store.Load("/ws1")
	if err != nil {
		t.Fatal(err)
	}
	if loaded == nil || loaded.TargetID != "user456" {
		t.Errorf("expected TargetID=user456, got %v", loaded)
	}

	list, err := store.List()
	if err != nil {
		t.Fatal(err)
	}
	if len(list) != 1 {
		t.Errorf("expected 1 binding, got %d", len(list))
	}

	if err := store.Delete("/ws1"); err != nil {
		t.Fatal(err)
	}

	b, _ = store.Load("/ws1")
	if b != nil {
		t.Error("expected nil after delete")
	}
}

func TestJSONFileBindingStore_BoundAtAutoSet(t *testing.T) {
	dir := t.TempDir()
	store, _ := NewJSONFileBindingStore(filepath.Join(dir, "bindings.json"))

	binding := ChannelBinding{
		Workspace: "/ws1",
		Platform:  PlatformQQ,
	}
	store.Save(binding)

	loaded, _ := store.Load("/ws1")
	if loaded.BoundAt.IsZero() {
		t.Error("expected BoundAt to be auto-set")
	}
}

func TestJSONFileBindingStore_InvalidJSON(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "bindings.json")

	os.WriteFile(path, []byte("{invalid json}"), 0o600)

	store, _ := NewJSONFileBindingStore(path)
	_, err := store.Load("/ws1")
	if err == nil {
		t.Error("expected error for invalid JSON")
	}
}

func TestJSONFileBindingStore_MultipleBindings(t *testing.T) {
	dir := t.TempDir()
	store, _ := NewJSONFileBindingStore(filepath.Join(dir, "bindings.json"))

	for i := 0; i < 3; i++ {
		store.Save(ChannelBinding{
			Workspace: "/ws" + string(rune('A'+i)),
			Platform:  PlatformQQ,
			TargetID:  "target" + string(rune('A'+i)),
		})
	}

	list, _ := store.List()
	if len(list) != 3 {
		t.Errorf("expected 3 bindings, got %d", len(list))
	}

	store.Delete("/wsB")
	list, _ = store.List()
	if len(list) != 2 {
		t.Errorf("expected 2 bindings after delete, got %d", len(list))
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
