package main

import (
	"testing"

	"github.com/topcheer/ggcode/desktop/wailskit"
)

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

func TestTeardownChatBridgeNilSafe(t *testing.T) {
	a := &App{}
	a.teardownChatBridge()
	if a.chat != nil {
		t.Fatal("a.chat set by teardown on an empty app")
	}
}
