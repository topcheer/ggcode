package main

import (
	"testing"

	"github.com/topcheer/ggcode/desktop/wailskit"
)

// #1183: CompleteOnboard used to call initWorkspace directly, skipping the
// old bridge's cleanup. teardownChatBridge (extracted from switchWorkspace)
// must clear the active bridge and unregister the shared pointer so a
// re-run of onboarding cannot leak the JSONL session lock, A2A port,
// tunnel share, IM adapters, or double-emit events.
func TestTeardownChatBridgeClearsActiveBridge(t *testing.T) {
	a := &App{chat: mustChatBridge(t)}
	wailskit.SetChatBridge(a.chat)

	a.teardownChatBridge()

	if a.chat != nil {
		t.Fatal("teardownChatBridge left a.chat set: old bridge would leak session lock / A2A port / tunnel (#1183)")
	}
	if wailskit.GetChatBridge() != nil {
		t.Fatal("shared chat bridge pointer still set after teardown: old OnStreamEvent wiring would double-emit (#1183)")
	}
}

func mustChatBridge(t *testing.T) *wailskit.ChatBridge {
	t.Helper()
	b, err := wailskit.NewChatBridge()
	if err != nil {
		t.Skipf("NewChatBridge unavailable in test env: %v", err)
	}
	return b
}

// #1183: teardown must be safe when no bridge is active (first onboarding
// run and shutdown paths both call it unconditionally).
func TestTeardownChatBridgeNilSafe(t *testing.T) {
	a := &App{}
	a.teardownChatBridge() // must not panic
	if a.chat != nil {
		t.Fatal("a.chat set by teardown on an empty app")
	}
}
