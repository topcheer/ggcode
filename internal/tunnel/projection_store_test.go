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

func TestBuildProjectionReplaySortsCanonicalEventOrder(t *testing.T) {
	replay := buildProjectionReplay(&projectionFile{
		SessionID: "sess-1",
		Events: []GatewayMessage{
			{SessionID: "sess-1", EventID: "ev-000000003", Type: EventTextDone},
			{SessionID: "sess-1", EventID: "ev-000000001", Type: EventText},
			{SessionID: "sess-1", EventID: "ev-000000004", Type: EventStatus},
			{SessionID: "sess-1", EventID: "ev-000000002", Type: EventActivity},
		},
	})

	got := []string{replay[0].EventID, replay[1].EventID, replay[2].EventID, replay[3].EventID}
	want := []string{"ev-000000001", "ev-000000002", "ev-000000003", "ev-000000004"}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("replay[%d] = %q, want %q (full=%v)", i, got[i], want[i], got)
		}
	}
}

func TestBuildProjectionReplayKeepsNewerTailStatusAfterOlderBootstrap(t *testing.T) {
	replay := buildProjectionReplay(&projectionFile{
		SessionID: "sess-1",
		Status: &GatewayMessage{
			SessionID: "sess-1",
			EventID:   "ev-000000005",
			Type:      EventStatus,
			Data:      json.RawMessage(`{"status":"busy","message":"Writing..."}`),
		},
		Activity: &GatewayMessage{
			SessionID: "sess-1",
			EventID:   "ev-000000007",
			Type:      EventActivity,
			Data:      json.RawMessage(`{"activity":"Writing..."}`),
		},
		Events: []GatewayMessage{
			{
				SessionID: "sess-1",
				EventID:   "ev-000000010",
				Type:      EventStatus,
				Data:      json.RawMessage(`{"status":"idle","message":""}`),
			},
			{
				SessionID: "sess-1",
				EventID:   "ev-000000011",
				Type:      EventActivity,
				Data:      json.RawMessage(`{"activity":""}`),
			},
		},
	})

	if len(replay) != 4 {
		t.Fatalf("replay len = %d, want 4", len(replay))
	}
	got := []string{replay[0].EventID, replay[1].EventID, replay[2].EventID, replay[3].EventID}
	want := []string{"ev-000000005", "ev-000000007", "ev-000000010", "ev-000000011"}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("replay[%d] = %q, want %q (full=%v)", i, got[i], want[i], got)
		}
	}
	if replay[2].Type != EventStatus || string(replay[2].Data) != `{"status":"idle","message":""}` {
		t.Fatalf("expected newer idle status to survive replay, got %#v", replay[2])
	}
	if replay[3].Type != EventActivity || string(replay[3].Data) != `{"activity":""}` {
		t.Fatalf("expected newer empty activity to survive replay, got %#v", replay[3])
	}
}
