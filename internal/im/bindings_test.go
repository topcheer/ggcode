package im

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
	"time"
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

func TestDefaultBindingsPath_RespectsHomeOverride(t *testing.T) {
	orig := os.Getenv("HOME")
	defer os.Setenv("HOME", orig)

	tmp := t.TempDir()
	os.Setenv("HOME", tmp)

	path, err := DefaultBindingsPath()
	if err != nil {
		t.Fatal(err)
	}
	want := tmp + "/.ggcode/im-bindings.json"
	if path != want {
		t.Errorf("DefaultBindingsPath() = %q, want %q", path, want)
	}
}

func TestDefaultBindingsPath_WriteReadUnderOverrideHome(t *testing.T) {
	orig := os.Getenv("HOME")
	defer os.Setenv("HOME", orig)

	tmp := t.TempDir()
	os.Setenv("HOME", tmp)
	os.MkdirAll(tmp+"/.ggcode", 0755)

	bindingsPath, err := DefaultBindingsPath()
	if err != nil {
		t.Fatal(err)
	}

	store, err := NewJSONFileBindingStore(bindingsPath)
	if err != nil {
		t.Fatal(err)
	}

	binding := ChannelBinding{
		Workspace: "/test/workspace",
		Platform:  "qq",
		Adapter:   "test-bot",
		TargetID:  "test-target",
		ChannelID: "test-channel",
	}

	// Save
	if err := store.Save(binding); err != nil {
		t.Fatalf("Save: %v", err)
	}

	// Read back via ListByWorkspace
	all, err := store.ListByWorkspace(binding.Workspace)
	if err != nil {
		t.Fatalf("ListByWorkspace: %v", err)
	}
	if len(all) != 1 {
		t.Fatalf("expected 1 binding, got %d", len(all))
	}
	if all[0].ChannelID != binding.ChannelID {
		t.Errorf("ChannelID = %q, want %q", all[0].ChannelID, binding.ChannelID)
	}

	// Confirm file exists under tmpdir
	data, err := os.ReadFile(tmp + "/.ggcode/im-bindings.json")
	if err != nil {
		t.Fatalf("file should exist under tmpdir: %v", err)
	}
	if len(data) == 0 {
		t.Error("bindings file is empty")
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

func TestJSONFileBindingStore_ContextTokenRoundTrip(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "bindings.json")
	store, _ := NewJSONFileBindingStore(path)

	// Save a binding with ContextToken
	b := ChannelBinding{
		Workspace:             "/tmp/ws",
		Platform:              PlatformWechat,
		Adapter:               "wechat",
		ChannelID:             "user-123",
		ContextToken:          "AARzJWA-test-token",
		ContextTokenUpdatedAt: time.Date(2026, 5, 3, 15, 0, 0, 0, time.Local),
		BoundAt:               time.Now(),
	}
	if err := store.Save(b); err != nil {
		t.Fatalf("Save: %v", err)
	}

	// Reload from disk
	all, err := store.List()
	if err != nil {
		t.Fatalf("List: %v", err)
	}
	if len(all) != 1 {
		t.Fatalf("expected 1 binding, got %d", len(all))
	}
	got := all[0]
	if got.ContextToken != "AARzJWA-test-token" {
		t.Errorf("ContextToken: got %q, want %q", got.ContextToken, "AARzJWA-test-token")
	}
	if got.ContextTokenUpdatedAt.IsZero() {
		t.Error("ContextTokenUpdatedAt should not be zero")
	}

	// Update token
	b.ContextToken = "NEW-TOKEN-XYZ"
	b.ContextTokenUpdatedAt = time.Now()
	if err := store.Save(b); err != nil {
		t.Fatalf("Save update: %v", err)
	}
	all, _ = store.List()
	if all[0].ContextToken != "NEW-TOKEN-XYZ" {
		t.Errorf("updated ContextToken: got %q", all[0].ContextToken)
	}
}

func TestJSONFileBindingStore_OldFileWithoutContextToken(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "bindings.json")

	// Write an old-format file without ContextToken fields
	// Use Go string with actual null byte in key (JSON key = workspace + \x00 + adapter)
	key := "/tmp/ws\x00wechat"
	oldMap := map[string]ChannelBinding{
		key: {
			Workspace: "/tmp/ws",
			Platform:  PlatformWechat,
			Adapter:   "wechat",
			ChannelID: "user-123",
			BoundAt:   time.Date(2026, 5, 3, 15, 0, 0, 0, time.Local),
		},
	}
	oldJSON, _ := json.Marshal(oldMap)
	if err := os.WriteFile(path, oldJSON, 0600); err != nil {
		t.Fatalf("WriteFile: %v", err)
	}

	// Load — should not fail, ContextToken should be empty string
	store, _ := NewJSONFileBindingStore(path)
	all, err := store.List()
	if err != nil {
		t.Fatalf("List: %v", err)
	}
	if len(all) != 1 {
		t.Fatalf("expected 1 binding, got %d", len(all))
	}
	got := all[0]
	if got.ContextToken != "" {
		t.Errorf("ContextToken should be empty for old format, got %q", got.ContextToken)
	}
	if got.ChannelID != "user-123" {
		t.Errorf("ChannelID: got %q", got.ChannelID)
	}

	// Save with new token — should preserve all fields
	got.ContextToken = "fresh-token"
	got.ContextTokenUpdatedAt = time.Now()
	if err := store.Save(got); err != nil {
		t.Fatalf("Save with token: %v", err)
	}
	all, _ = store.List()
	if all[0].ContextToken != "fresh-token" {
		t.Errorf("after save, ContextToken: got %q", all[0].ContextToken)
	}
	if all[0].ChannelID != "user-123" {
		t.Errorf("ChannelID preserved: got %q", all[0].ChannelID)
	}
}

// --- Home override for pairing and pc_session_store paths ---

func TestDefaultPairingStatePath_RespectsHomeOverride(t *testing.T) {
	orig := os.Getenv("HOME")
	defer os.Setenv("HOME", orig)

	tmp := t.TempDir()
	os.Setenv("HOME", tmp)

	path, err := DefaultPairingStatePath()
	if err != nil {
		t.Fatal(err)
	}
	want := tmp + "/.ggcode/im-pairing.json"
	if path != want {
		t.Errorf("DefaultPairingStatePath() = %q, want %q", path, want)
	}
}

func TestDefaultPCSessionStorePath_RespectsHomeOverride(t *testing.T) {
	orig := os.Getenv("HOME")
	defer os.Setenv("HOME", orig)

	tmp := t.TempDir()
	os.Setenv("HOME", tmp)

	path, err := DefaultPCSessionStorePath()
	if err != nil {
		t.Fatal(err)
	}
	want := tmp + "/.ggcode/im-pc-sessions.json"
	if path != want {
		t.Errorf("DefaultPCSessionStorePath() = %q, want %q", path, want)
	}
}
