package tui

import (
	"testing"

	"github.com/topcheer/ggcode/internal/tunnel"
)

// Regression for the webui share-QR bug: bubbletea evolves the Model through
// value copies (tea.NewProgram receives r.model by value), so the REPL-held
// original never sees tunnelSession updates and /api/tunnel/status polled a
// permanently-empty share face while the TUI rendered the QR from the live
// copy. The authoritative share face now lives in a pointer shared across
// copies (tunnelShareLive): a start executed on a COPY (simulating the
// bubbletea Update chain) must be visible to TunnelStatusSnapshot called on
// the ORIGINAL value - exactly what the webui poll reads via
// repl.TunnelStatusSnapshot.
func TestTunnelStatusSnapshotReadsSharedLiveState(t *testing.T) {
	original := NewModel(nil, nil)

	sess := tunnel.NewSession(tunnel.DefaultRelayURL)

	// Simulate bubbletea: handleTunnelStartMsg runs on a copy of the model
	// value and writes through the shared live store.
	handlerCopy := original
	handlerCopy.tunnelShareLive.set(sess, nil)

	// The snapshot called on the ORIGINAL (what the webui reads) must see the
	// share started on the copy. SessionInfo is nil on a bare session, so the
	// QR/connect_url keys are absent - but active must flip true.
	st := original.TunnelStatusSnapshot()["share"].(map[string]any)
	if st["active"] != true {
		t.Fatalf("share must be active on the original's snapshot after a copy-side start, got %v", st)
	}

	// Stop semantics: a copy-side stop must propagate to the original too.
	handlerCopy.tunnelShareLive.clear()
	st = original.TunnelStatusSnapshot()["share"].(map[string]any)
	if st["active"] != false {
		t.Fatalf("share must be inactive after copy-side clear, got %v", st)
	}
}

// Fallback: models whose shared store is nil (older constructions) read
// m.tunnelSession and must not panic.
func TestTunnelStatusSnapshotNilSharedStoreFallback(t *testing.T) {
	m := Model{}
	st := m.TunnelStatusSnapshot()["share"].(map[string]any)
	if st["active"] != false {
		t.Fatalf("inactive model must report active=false, got %v", st)
	}
}
