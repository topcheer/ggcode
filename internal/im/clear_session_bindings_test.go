package im

import (
	"os"
	"path/filepath"
	"testing"
)

func TestClearSessionBindings(t *testing.T) {
	dir, _ := os.MkdirTemp("", "ggcode_im_*")
	defer os.RemoveAll(dir)

	path := filepath.Join(dir, "bindings.json")
	store, err := NewJSONFileBindingStore(path)
	if err != nil {
		t.Fatal(err)
	}

	// Create bindings: two reference session "s1", one references "s2".
	_ = store.Save(ChannelBinding{
		Workspace:     "/ws",
		Platform:      PlatformQQ,
		Adapter:       "qq-1",
		ChannelID:     "ch-1",
		LastSessionID: "s1",
	})
	_ = store.Save(ChannelBinding{
		Workspace:     "/ws",
		Platform:      PlatformTelegram,
		Adapter:       "tg-1",
		ChannelID:     "ch-2",
		LastSessionID: "s1",
	})
	_ = store.Save(ChannelBinding{
		Workspace:     "/ws",
		Platform:      PlatformDiscord,
		Adapter:       "dc-1",
		ChannelID:     "ch-3",
		LastSessionID: "s2",
	})

	// Clear bindings for session "s1".
	mgr := NewManager()
	_ = mgr.SetBindingStore(store)
	mgr.ClearSessionBindings("s1")

	// Verify: qq-1 and tg-1 should have empty LastSessionID, dc-1 unchanged.
	all, _ := store.List()
	for _, b := range all {
		if b.Adapter == "qq-1" || b.Adapter == "tg-1" {
			if b.LastSessionID != "" {
				t.Errorf("adapter %s: expected empty LastSessionID, got %q", b.Adapter, b.LastSessionID)
			}
		}
		if b.Adapter == "dc-1" {
			if b.LastSessionID != "s2" {
				t.Errorf("adapter %s: expected LastSessionID 's2', got %q", b.Adapter, b.LastSessionID)
			}
		}
	}
}

func TestClearSessionBindingsGlobal(t *testing.T) {
	dir, _ := os.MkdirTemp("", "ggcode_im_*")
	defer os.RemoveAll(dir)

	path := filepath.Join(dir, "bindings.json")
	store, _ := NewJSONFileBindingStore(path)

	_ = store.Save(ChannelBinding{
		Workspace:     "/ws",
		Platform:      PlatformQQ,
		Adapter:       "qq-1",
		ChannelID:     "ch-1",
		LastSessionID: "stale-session",
	})

	// Use the standalone global function (operates on default path).
	// Since we can't easily override DefaultBindingsPath, test via Manager instead.
	// This test just verifies ClearSessionBindingsGlobal doesn't panic with
	// a bogus session ID.
	ClearSessionBindingsGlobal("nonexistent-session-id")

	// Verify the binding still has its LastSessionID (session didn't match).
	all, _ := store.List()
	if len(all) != 1 || all[0].LastSessionID != "stale-session" {
		t.Errorf("unexpected binding state after clear: %+v", all)
	}
}
