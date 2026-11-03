package main

import (
	"strings"
	"testing"

	"github.com/topcheer/ggcode/desktop/wailskit"
)

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

func TestIssue699RespondAskUserNilChatNoPanic(t *testing.T) {
	a := NewApp()

	a.RespondAskUser("req-1", `{"status":"submitted","answers":[]}`)
}

func TestIssue699InitWorkspacePropagatesError(t *testing.T) {
	a := NewApp()
	a.dc = wailskit.LoadDesktopConfig()

	if err := a.initWorkspace(""); err != nil {
		t.Fatalf("initWorkspace(\"\") should be a no-op nil, got %v", err)
	}
}
