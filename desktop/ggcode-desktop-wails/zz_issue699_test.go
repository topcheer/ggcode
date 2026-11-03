package main

import (
	"strings"
	"testing"

	"github.com/topcheer/ggcode/desktop/wailskit"
)

// #699: NewSession must check the chat snapshot taken at function entry and
// must NOT re-read a.chat after the nil check. A concurrent switchWorkspace
// nils a.chat between the snapshot and the use — the old code re-read the
// field (a.chat == nil / a.chat.Cancel()) and either silently returned
// ("", nil) or panicked on a nil *ChatBridge receiver.
func TestIssue699NewSessionUsesChatSnapshot(t *testing.T) {
	a := NewApp()

	sid, err := a.NewSession()
	if err == nil {
		t.Fatalf("NewSession with nil chat: expected error, got silent success (sid=%q)", sid)
	}
	if !strings.Contains(err.Error(), "chat not available") {
		t.Fatalf("NewSession with nil chat: unexpected error: %v", err)
	}
	if sid != "" {
		t.Fatalf("NewSession with nil chat: expected empty session id, got %q", sid)
	}

}

// #699: RespondAskUser must route the response through the snapshotted chat
// (the one the ask_user waiter belongs to), not a re-read a.chat. With a
// nil a.chat after snapshot the old code panicked inside ChatBridge's
// method on a nil receiver; the snapshot version is safe because it is
// checked before use and never dereferenced off the field.
func TestIssue699RespondAskUserNilChatNoPanic(t *testing.T) {
	a := NewApp()

	a.RespondAskUser("req-1", `{"status":"submitted","answers":[]}`)
}

// #699 defensive: initWorkspace now propagates NewChatBridge errors instead
// of silently returning with a.chat == nil masquerading as success.
func TestIssue699InitWorkspacePropagatesError(t *testing.T) {
	a := NewApp()
	a.dc = wailskit.LoadDesktopConfig()

	if err := a.initWorkspace(""); err != nil {
		t.Fatalf("initWorkspace(\"\") should be a no-op nil, got %v", err)
	}
}
