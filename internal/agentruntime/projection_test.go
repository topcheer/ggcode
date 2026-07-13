package agentruntime

import (
	"path/filepath"
	"testing"

	"github.com/topcheer/ggcode/internal/session"
	"github.com/topcheer/ggcode/internal/tunnel"
)

func TestHydrateProjectionReplayFromSessionLedgerRequiresCompleteLedger(t *testing.T) {
	store, err := tunnel.NewProjectionStore(t.TempDir())
	if err != nil {
		t.Fatalf("new projection store: %v", err)
	}
	ses := &session.Session{
		ID:                   "sess-1",
		TunnelEventsComplete: false,
		TunnelEvents: []session.TunnelEvent{{
			EventID: "ev-1",
			Type:    tunnel.EventText,
			Data:    []byte(`{"id":"msg-1","chunk":"hello"}`),
		}},
	}

	replay, err := HydrateProjectionReplayFromSessionLedger(store, ses, nil)
	if err != nil {
		t.Fatalf("hydrate: %v", err)
	}
	if len(replay) != 0 {
		t.Fatalf("expected no replay changes, got %d events", len(replay))
	}
	got, err := store.ReplayEvents(ses.ID)
	if err != nil {
		t.Fatalf("store replay: %v", err)
	}
	if len(got) != 0 {
		t.Fatalf("expected store to stay empty, got %d events", len(got))
	}
}

func TestHydrateProjectionReplayFromSessionLedgerDedupesAndReloads(t *testing.T) {
	store, err := tunnel.NewProjectionStore(filepath.Join(t.TempDir(), "projection"))
	if err != nil {
		t.Fatalf("new projection store: %v", err)
	}
	if err := store.Append(tunnel.GatewayMessage{
		SessionID: "sess-1",
		EventID:   "ev-1",
		Type:      tunnel.EventSessionInfo,
		Data:      []byte(`{"workspace":"/tmp/repo"}`),
	}); err != nil {
		t.Fatalf("seed append: %v", err)
	}
	initial, err := store.ReplayEvents("sess-1")
	if err != nil {
		t.Fatalf("seed replay: %v", err)
	}

	ses := &session.Session{
		ID:                   "sess-1",
		TunnelEventsComplete: true,
		TunnelEvents: []session.TunnelEvent{
			{
				EventID: "ev-1",
				Type:    tunnel.EventSessionInfo,
				Data:    []byte(`{"workspace":"/tmp/repo"}`),
			},
			{
				EventID: "ev-2",
				Type:    tunnel.EventText,
				Data:    []byte(`{"id":"msg-1","chunk":"hello"}`),
			},
		},
	}

	replay, err := HydrateProjectionReplayFromSessionLedger(store, ses, initial)
	if err != nil {
		t.Fatalf("hydrate: %v", err)
	}
	// HydrateProjectionReplayFromSessionLedger is now a no-op — tunnel events
	// are no longer stored in session JSONL. The replay should be unchanged.
	if len(replay) != len(initial) {
		t.Fatalf("expected %d replay events (unchanged), got %d", len(initial), len(replay))
	}
}
