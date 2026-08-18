package acp

import (
	"encoding/json"
	"strings"
	"testing"
	"time"

	"github.com/topcheer/ggcode/internal/debug"
)

// #669 defect 2: DeliverResponse drops responses whose id is non-numeric
// (peer violated JSON-RPC 2.0 id echo). The drop must be logged so the
// subsequent SendRequest timeout is diagnosable.
func TestIssue669DeliverResponseLogsStringID(t *testing.T) {
	debug.EnableForTest(t)
	defer debug.Close()

	tr := NewTransport(strings.NewReader(""), &strings.Builder{})

	// Sanity: numeric ids still route to the pending map.
	id := tr.nextID.Add(1)
	registerPendingForTest(t, tr, id)

	resp := &JSONRPCResponse{ID: json.Number("123")}
	tr.DeliverResponse(resp) // numeric — no drop log expected for this one

	// String id: silently dropped before the fix; must log now.
	dropped := &JSONRPCResponse{ID: "abc-123"}
	tr.DeliverResponse(dropped)

	entries := debug.RingHistory(50, "acp")
	found := false
	for _, e := range entries {
		if strings.Contains(e.Message, "non-numeric id") && strings.Contains(e.Message, "abc-123") {
			found = true
		}
	}
	if !found {
		t.Fatalf("DeliverResponse should log the dropped non-numeric id response; entries: %+v", entries)
	}
}

// registerPendingForTest registers a pending SendRequest channel for id.
func registerPendingForTest(t *testing.T, tr *Transport, id int64) {
	t.Helper()
	tr.pendingMu.Lock()
	tr.pending[id] = make(chan *JSONRPCResponse, 1)
	tr.pendingMu.Unlock()
}

// #669 defect 2 companion: numeric ids are unaffected — a float64 id
// (standard json decoding of a number) still reaches the pending caller.
func TestIssue669DeliverResponseNumericStillWorks(t *testing.T) {
	tr := NewTransport(strings.NewReader(""), &strings.Builder{})
	id := tr.nextID.Add(1)
	ch := registerPendingAndGet(t, tr, id)

	resp := &JSONRPCResponse{ID: float64(id)}
	tr.DeliverResponse(resp)

	select {
	case got := <-ch:
		if got != resp {
			t.Fatalf("received wrong response")
		}
	case <-time.After(time.Second):
		t.Fatalf("numeric id response was not delivered")
	}
}

func registerPendingAndGet(t *testing.T, tr *Transport, id int64) chan *JSONRPCResponse {
	t.Helper()
	ch := make(chan *JSONRPCResponse, 1)
	tr.pendingMu.Lock()
	tr.pending[id] = ch
	tr.pendingMu.Unlock()
	return ch
}
