package im

// Issue #942: tryQueueOrBeginRun cleared pendingInterruptions when beginning a
// new run. Messages queued in the race window where the previous run was
// finishing (queued while cancelFunc was still set, then a new message began
// the next run) were silently discarded — the webhook had already ACKed them,
// so the loss was unrecoverable. The fix keeps the queue intact; the new
// run's interruption handler / runQueuedLoop drains it in FIFO order.

import (
	"testing"

	"github.com/topcheer/ggcode/internal/agent"
	"github.com/topcheer/ggcode/internal/provider"
	"github.com/topcheer/ggcode/internal/session"
	"github.com/topcheer/ggcode/internal/tool"
)

func TestTryQueueOrBeginRunPreservesQueuedInterruptions(t *testing.T) {
	store, err := session.NewJSONLStore(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	ses := session.NewSession("openai", "api", "test-model")
	prov := &daemonBridgeMetricsProvider{
		events: []provider.StreamEvent{{Type: provider.StreamEventDone}},
	}
	ag := agent.NewAgent(prov, tool.NewRegistry(), "", 3)
	mgr := NewManager()
	b := NewDaemonBridge(mgr, ag, nil, store, ses)

	// First message begins a run.
	ctx1, queued := b.tryQueueOrBeginRun([]provider.ContentBlock{{Type: "text", Text: "first run"}}, "test: ")
	if queued {
		t.Fatal("first call should begin a run, not queue")
	}
	if ctx1 == nil {
		t.Fatal("begin-run must return a non-nil context")
	}

	// Second message arrives while the run is active → queued.
	if _, queued = b.tryQueueOrBeginRun([]provider.ContentBlock{{Type: "text", Text: "queued during run"}}, "test: "); !queued {
		t.Fatal("second call should queue while a run is active")
	}

	// Run finishes.
	b.finishRunSlot()

	// Third message begins a new run. Previously this wiped the queued
	// message (#942); now it must survive for the new run to drain.
	if _, queued = b.tryQueueOrBeginRun([]provider.ContentBlock{{Type: "text", Text: "second run"}}, "test: "); queued {
		t.Fatal("third call should begin a new run")
	}

	b.mu.Lock()
	pending := len(b.pendingInterruptions)
	b.mu.Unlock()
	if pending != 1 {
		t.Fatalf("pendingInterruptions len = %d after begin-run, want 1 (queued message must survive, #942)", pending)
	}

	// Cleanup: release the run slot.
	b.finishRunSlot()
}
