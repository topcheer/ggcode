//go:build !integration

package tunnel

import (
	"encoding/json"
	"fmt"
	"path/filepath"
	"testing"
)

func TestProjectionStoreReplayEventsCapsTailAndKeepsLatestBootstrap(t *testing.T) {
	store, err := NewProjectionStore(filepath.Join(t.TempDir(), "projection"))
	if err != nil {
		t.Fatal(err)
	}

	sessionID := "sess-1"
	if err := store.Append(GatewayMessage{
		SessionID: sessionID,
		EventID:   "session-info",
		Type:      EventSessionInfo,
		Data:      json.RawMessage(`{"session_id":"sess-1"}`),
	}); err != nil {
		t.Fatal(err)
	}
	if err := store.Append(GatewayMessage{
		SessionID: sessionID,
		EventID:   "status-latest",
		Type:      EventStatus,
		Data:      json.RawMessage(`{"status":"busy"}`),
	}); err != nil {
		t.Fatal(err)
	}
	if err := store.Append(GatewayMessage{
		SessionID: sessionID,
		EventID:   "activity-latest",
		Type:      EventActivity,
		Data:      json.RawMessage(`{"activity":"thinking"}`),
	}); err != nil {
		t.Fatal(err)
	}

	for i := 0; i < ProjectionReplayLimit+20; i++ {
		if err := store.Append(GatewayMessage{
			SessionID: sessionID,
			EventID:   fmt.Sprintf("text-%04d", i),
			Type:      EventText,
			Data:      json.RawMessage(fmt.Sprintf(`{"id":"msg-1","chunk":"%d"}`, i)),
		}); err != nil {
			t.Fatal(err)
		}
	}

	replay, err := store.ReplayEvents(sessionID)
	if err != nil {
		t.Fatal(err)
	}
	if len(replay) != ProjectionReplayLimit {
		t.Fatalf("replay len = %d, want %d", len(replay), ProjectionReplayLimit)
	}
	if replay[0].EventID != "session-info" || replay[1].EventID != "status-latest" || replay[2].EventID != "activity-latest" {
		t.Fatalf("expected bootstrap events first, got %q %q %q", replay[0].EventID, replay[1].EventID, replay[2].EventID)
	}
	if replay[len(replay)-1].EventID != "text-1019" {
		t.Fatalf("last event = %q, want text-1019", replay[len(replay)-1].EventID)
	}
	if replay[3].EventID != "text-0023" {
		t.Fatalf("first tail event = %q, want text-0023", replay[3].EventID)
	}
}

func TestBuildProjectionReplaySkipsBootstrapDuplicatesAlreadyInTail(t *testing.T) {
	replay := buildProjectionReplay(&projectionFile{
		SessionID: "sess-1",
		SessionInfo: &GatewayMessage{
			SessionID: "sess-1",
			EventID:   "info-1",
			Type:      EventSessionInfo,
		},
		Status: &GatewayMessage{
			SessionID: "sess-1",
			EventID:   "status-2",
			Type:      EventStatus,
		},
		Activity: &GatewayMessage{
			SessionID: "sess-1",
			EventID:   "activity-3",
			Type:      EventActivity,
		},
		Events: []GatewayMessage{
			{SessionID: "sess-1", EventID: "status-2", Type: EventStatus},
			{SessionID: "sess-1", EventID: "text-4", Type: EventText},
		},
	})

	if len(replay) != 4 {
		t.Fatalf("replay len = %d, want 4", len(replay))
	}
	if replay[0].EventID != "info-1" || replay[1].EventID != "status-2" || replay[2].EventID != "activity-3" || replay[3].EventID != "text-4" {
		t.Fatalf("unexpected replay order: %#v", replay)
	}
}
