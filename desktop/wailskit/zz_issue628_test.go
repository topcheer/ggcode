package wailskit

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/topcheer/ggcode/internal/session"
)

func TestIssue628_RenameSyncsLiveBridgeTitle(t *testing.T) {
	tmp := t.TempDir()
	t.Setenv("HOME", tmp)
	dir := filepath.Join(tmp, ".ggcode", "sessions")
	id := "sess628rename"
	path := filepath.Join(dir, id+".jsonl")

	now := time.Now()
	var b strings.Builder
	fmt.Fprintf(&b, "%s\n", `{"type":"meta","session_id":"`+id+`","title":"Old title","created_at":"`+now.Format(time.RFC3339Nano)+`","updated_at":"`+now.Format(time.RFC3339Nano)+`"}`)
	fmt.Fprintf(&b, "%s\n", `{"type":"message","session_id":"`+id+`","timestamp":"`+now.Format(time.RFC3339Nano)+`","message":{"role":"user","content":[{"type":"text","text":"hello"}]}}`)
	if err := os.MkdirAll(dir, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(b.String()), 0o600); err != nil {
		t.Fatal(err)
	}

	bridge, err := NewChatBridge()
	if err != nil {
		t.Fatalf("NewChatBridge: %v", err)
	}
	SetChatBridge(bridge)
	t.Cleanup(func() { SetChatBridge(nil); bridge.Close() })
	if err := bridge.LoadSession(id); err != nil {
		t.Fatalf("LoadSession: %v", err)
	}

	currentTitle := func() string {
		bridge.mu.Lock()
		defer bridge.mu.Unlock()
		if bridge.currentSes == nil {
			return ""
		}
		return bridge.currentSes.Title
	}
	if got := currentTitle(); got != "Old title" {
		t.Fatalf("setup: current title = %q, want %q", got, "Old title")
	}

	if err := RenameSession(id, "New title"); err != nil {
		t.Fatalf("RenameSession: %v", err)
	}

	if got := currentTitle(); got != "New title" {
		t.Fatalf("currentSes.Title not synced: got %q, want %q", got, "New title")
	}

	bridge.mu.Lock()
	ses := bridge.currentSes
	store := bridge.sessionStore
	bridge.mu.Unlock()
	if ses == nil || store == nil {
		t.Fatal("setup: bridge lost current session or store")
	}
	if err := store.AppendMetaToDisk(ses); err != nil {
		t.Fatalf("AppendMetaToDisk: %v", err)
	}
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}

	lastMeta := ""
	for _, line := range strings.Split(string(data), "\n") {
		if strings.Contains(line, `"type":"meta"`) {
			lastMeta = line
		}
	}
	if !strings.Contains(lastMeta, `"title":"New title"`) {
		t.Fatalf("meta write-back resurrected old title. Last meta record: %s", lastMeta)
	}
}

func TestIssue628_RenameOtherSessionLeavesCurrentAlone(t *testing.T) {
	bridge := &ChatBridge{currentSes: &session.Session{ID: "current-id", Title: "Original"}}

	bridge.syncRenamedTitle("some-other-id", "irrelevant")

	bridge.mu.Lock()
	ses := bridge.currentSes
	bridge.mu.Unlock()
	if ses == nil || ses.Title != "Original" {
		t.Fatalf("non-matching rename leaked into current session title: %+v", ses)
	}

	bridge.syncRenamedTitle("current-id", "Updated")
	bridge.mu.Lock()
	title := bridge.currentSes.Title
	bridge.mu.Unlock()
	if title != "Updated" {
		t.Fatalf("matching rename did not sync: %q", title)
	}
}
