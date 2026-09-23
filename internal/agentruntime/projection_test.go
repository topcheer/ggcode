package agentruntime

import (
	"path/filepath"
	"testing"

	"github.com/topcheer/ggcode/internal/session"
	"github.com/topcheer/ggcode/internal/tunnel"
)

// PrepareProjectionReplay is the sole replay path: the session JSONL ledger
// (even an incomplete one) must never leak into the replay — the projection
// store is the only source of truth.
func TestPrepareProjectionReplayIgnoresIncompleteSessionLedger(t *testing.T) {
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

	epoch, replay, err := PrepareProjectionReplay(store, ses)
	if err != nil {
		t.Fatalf("prepare: %v", err)
	}
	if epoch != 1 {
		t.Fatalf("expected default epoch 1 for empty store, got %d", epoch)
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

// A populated projection store must be replayed verbatim; the session ledger
// is ignored (no dedupe/reload against JSONL anymore).
func TestPrepareProjectionReplayReplayUnchangedBySessionLedger(t *testing.T) {
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

	_, replay, err := PrepareProjectionReplay(store, ses)
	if err != nil {
		t.Fatalf("prepare: %v", err)
	}
	if len(replay) != len(initial) {
		t.Fatalf("expected %d replay events (unchanged), got %d", len(initial), len(replay))
	}
}
