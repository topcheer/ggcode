package main

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/topcheer/ggcode/desktop/wailskit"
)

// Issue #630: deleting the CURRENT session cleared backend state but the
// frontend never received a session:changed notification it could act on
// (the implicit notify fires with an empty sessionId that the Layout handler
// ignored), so the deleted transcript stayed on screen and the next message
// silently landed in a brand-new session. DeleteSession must emit an explicit
// session:changed event.
func TestIssue630_DeleteCurrentSessionEmitsSessionChanged(t *testing.T) {
	tmp := t.TempDir()
	t.Setenv("HOME", tmp)
	dir := filepath.Join(tmp, ".ggcode", "sessions")
	id := "sess630delete"
	path := filepath.Join(dir, id+".jsonl")

	now := time.Now()
	var b strings.Builder
	fmt.Fprintf(&b, "%s\n", `{"type":"meta","session_id":"`+id+`","title":"Doomed","created_at":"`+now.Format(time.RFC3339Nano)+`","updated_at":"`+now.Format(time.RFC3339Nano)+`"}`)
	fmt.Fprintf(&b, "%s\n", `{"type":"message","session_id":"`+id+`","timestamp":"`+now.Format(time.RFC3339Nano)+`","message":{"role":"user","content":[{"type":"text","text":"bye"}]}}`)
	if err := os.MkdirAll(dir, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(b.String()), 0o600); err != nil {
		t.Fatal(err)
	}

	chat, err := wailskit.NewChatBridge()
	if err != nil {
		t.Fatalf("NewChatBridge: %v", err)
	}
	t.Cleanup(func() { chat.Close() })
	if err := chat.LoadSession(id); err != nil {
		t.Fatalf("LoadSession: %v", err)
	}

	app := &App{chat: chat}
	// Observe the buffered channel directly — do NOT start the event-loop
	// goroutine (startEventLoop); its consumer drains events before this
	// test's receive loop can see them.
	app.streamEvents = make(chan uiEvent, 4096)

	if err := app.DeleteSession(id); err != nil {
		t.Fatalf("DeleteSession: %v", err)
	}

	// Drain pending events, waiting briefly for the explicit emit.
	deadline := time.Now().Add(3 * time.Second)
	sawSessionChanged := false
	for time.Now().Before(deadline) {
		select {
		case ev := <-app.streamEvents:
			if ev.name != "session:changed" {
				continue
			}
			sawSessionChanged = true
			m, ok := ev.payload.(map[string]string)
			if !ok {
				t.Fatalf("session:changed payload type = %T, want map[string]string", ev.payload)
			}
			if m["sessionId"] != "" {
				t.Fatalf("session:changed sessionId = %q, want empty (current session cleared)", m["sessionId"])
			}
		default:
			if sawSessionChanged {
				return
			}
			time.Sleep(10 * time.Millisecond)
		}
	}
	if !sawSessionChanged {
		t.Fatal("deleting the current session emitted no session:changed event — frontend keeps rendering the deleted transcript (#630)")
	}
}
