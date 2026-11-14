package lanchat

import (
	"path/filepath"
	"testing"
	"time"
)

// TestSetSessionIDFiresNickRestored pins #5616: when SetSessionID restores a
// persisted nick, the onNickRestored callback must fire so callers can
// re-stamp identities that were stamped against the pre-restore RandomNick
// placeholder (e.g. the a2a seat-nick roster stamp).
func TestSetSessionIDFiresNickRestored(t *testing.T) {
	base := t.TempDir()
	sessDir := filepath.Join(base, "sessions", "s1")
	if err := SaveNick(sessDir, "alice"); err != nil {
		t.Fatalf("SaveNick seed: %v", err)
	}

	h := NewHub("node-a", "cli", "http://localhost:1", "", NewStore(t.TempDir()), WorkspaceMeta{Workspace: "/w"})
	if h.HumanNick() == "alice" {
		t.Fatalf("precondition: hub started with the persisted nick %q before SetSessionID", h.HumanNick())
	}

	got := make(chan string, 1)
	h.SetOnNickRestored(func(nick string) { got <- nick })
	h.SetSessionID(base, "s1")

	select {
	case nick := <-got:
		if nick != "alice" {
			t.Fatalf("callback nick = %q, want alice", nick)
		}
	case <-time.After(2 * time.Second):
		t.Fatalf("onNickRestored callback never fired after SetSessionID")
	}
	if h.HumanNick() != "alice" {
		t.Fatalf("HumanNick after restore = %q, want alice", h.HumanNick())
	}
}
