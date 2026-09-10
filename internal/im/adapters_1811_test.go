package im

import (
	"path/filepath"
	"testing"
)

// #1811 case 2: AdapterBindings reports the persisted workspaces so Rebind
// can roll back to them when the rebind itself fails.
func Test1811AdapterBindings(t *testing.T) {
	dir := t.TempDir()
	store, err := NewJSONFileBindingStore(filepath.Join(dir, "bindings.json"))
	if err != nil {
		t.Fatal(err)
	}
	m := NewManager()
	defer m.StopBindingWatcher()
	if err := m.SetBindingStore(store); err != nil {
		t.Fatal(err)
	}

	if got := m.AdapterBindings("nope"); got != nil {
		t.Fatalf("unknown adapter must have no bindings, got %v", got)
	}

	if err := m.BindAdapterToWorkspace("qq", "/ws/a"); err != nil {
		t.Fatal(err)
	}
	got := m.AdapterBindings("qq")
	if len(got) != 1 || got[0] != "/ws/a" {
		t.Fatalf("expected [/ws/a], got %v", got)
	}

	// BindExclusive replaces: rebind elsewhere leaves only the new one.
	if err := m.BindAdapterToWorkspace("qq", "/ws/b"); err != nil {
		t.Fatal(err)
	}
	got = m.AdapterBindings("qq")
	if len(got) != 1 || got[0] != "/ws/b" {
		t.Fatalf("expected [/ws/b] after exclusive rebind, got %v", got)
	}
}
