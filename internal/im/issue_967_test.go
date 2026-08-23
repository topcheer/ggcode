package im

// Regression tests for GitHub issue #967: binding ownership protocol
// asymmetries.
//
//	1: EnableBinding must reject hijacking a binding owned by another LIVE
//	   session (same #689/#693 guard as UnmuteBinding/UnmuteAll).
//	2: EnableAll must claim (memory + store) and DisableAll must clear
//	   LastSessionID in the store, mirroring EnableBinding/DisableBinding.
//	3: HandleInbound must compare the TRIMMED inbound ChannelID against the
//	   (trimmed) persisted binding.ChannelID - a channel ID with surrounding
//	   whitespace must no longer be permanently denied after the first
//	   message.
//	4: UnbindChannel/DeleteBinding/UnbindAdapter must cascade stopAdapter so
//	   no live adapter goroutine survives its binding's removal.

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"strings"
	"sync"
	"testing"
	"time"
)

// ---- shared helpers ----

type okBridge967 struct{}

func (okBridge967) SubmitInboundMessage(context.Context, InboundMessage) error { return nil }

// countingSink967 is a Sink+Closer that records Close calls (stopAdapter cascade).
type countingSink967 struct {
	mu     sync.Mutex
	closed int
}

func (s *countingSink967) Name() string { return "test-adp" }
func (s *countingSink967) Send(context.Context, ChannelBinding, OutboundEvent) error {
	return nil
}
func (s *countingSink967) Close() error {
	s.mu.Lock()
	s.closed++
	s.mu.Unlock()
	return nil
}
func (s *countingSink967) closeCount() int {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.closed
}

// newManager967 builds a Manager with a live session and (optionally) a live
// peer instance registered in the instance-detect dir, so
// foreignOwnerPossiblyAliveLocked returns true.
func newManager967(t *testing.T, withLivePeer bool) (*Manager, *MemoryBindingStore) {
	t.Helper()
	dir := t.TempDir()
	instancesDir := dir + "/.ggcode/instances"
	if err := os.MkdirAll(instancesDir, 0o755); err != nil {
		t.Fatalf("mkdir instances: %v", err)
	}

	store := NewMemoryBindingStore()
	m := NewManager()
	m.bridge = okBridge967{}
	m.bindingStore = store
	m.BindSession(SessionBinding{Workspace: dir, SessionID: "sess-A"})

	if withLivePeer {
		sleepCmd := exec.Command("sleep", "30")
		if err := sleepCmd.Start(); err != nil {
			t.Skip("cannot fork sleep process")
		}
		t.Cleanup(func() { _ = sleepCmd.Process.Kill() })
		info := InstanceInfo{
			PID:       sleepCmd.Process.Pid,
			UUID:      "peer-967-live",
			StartedAt: time.Now().Add(-time.Minute),
		}
		_ = os.WriteFile(fmt.Sprintf("%s/%d-peer-967.json", instancesDir, info.PID), mustMarshal967(t, info), 0o644)
	}

	// Register self so instanceDetect is non-nil; with no peer file above,
	// foreignOwnerPossiblyAliveLocked reports false (dead owner).
	if _, _, err := m.RegisterInstance(dir, ""); err != nil {
		t.Fatalf("RegisterInstance: %v", err)
	}
	return m, store
}

func mustMarshal967(t *testing.T, v any) []byte {
	t.Helper()
	data, err := json.Marshal(v)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	return data
}

func disabledForeignBinding967(workspace string) *ChannelBinding {
	return &ChannelBinding{
		Workspace:     workspace,
		Adapter:       "test-adp",
		ChannelID:     "ch-1",
		LastSessionID: "sess-B",
	}
}

// ---- Issue 1: EnableBinding foreign-live hijack rejection ----

func TestIssue967EnableBindingRejectsForeignLiveOwner(t *testing.T) {
	m, _ := newManager967(t, true)
	m.disabledBindings["test-adp"] = disabledForeignBinding967(m.session.Workspace)

	err := m.EnableBinding("test-adp")
	if err == nil {
		t.Fatal("EnableBinding must reject a binding owned by another live session")
	}
	if !strings.Contains(err.Error(), "another live session") {
		t.Fatalf("unexpected error: %v", err)
	}
	// The binding must STAY disabled and ownership must be untouched.
	if _, ok := m.currentBindings["test-adp"]; ok {
		t.Fatal("rejected EnableBinding must not move the binding to currentBindings")
	}
	if !m.IsBindingDisabled("test-adp") {
		t.Fatal("binding must remain disabled after rejection")
	}
}

func TestIssue967EnableBindingDeadOwnerTakeoverAllowed(t *testing.T) {
	m, _ := newManager967(t, false) // no live peer -> foreign owner is dead
	m.disabledBindings["test-adp"] = disabledForeignBinding967(m.session.Workspace)

	if err := m.EnableBinding("test-adp"); err != nil {
		t.Fatalf("dead-owner takeover via EnableBinding should be allowed, got: %v", err)
	}
	b := m.currentBindings["test-adp"]
	if b == nil || b.LastSessionID != "sess-A" {
		t.Fatalf("binding must be re-enabled and claimed for sess-A, got %+v", b)
	}
}

// ---- Issue 2: EnableAll/DisableAll symmetric claim/clear ----

func TestIssue967DisableAllClearsStoreSessionID(t *testing.T) {
	m, store := newManager967(t, false)
	ws := m.session.Workspace
	if err := store.Save(ChannelBinding{Workspace: ws, Adapter: "adp-1", ChannelID: "c1", LastSessionID: "sess-A"}); err != nil {
		t.Fatalf("Save: %v", err)
	}
	if err := store.Save(ChannelBinding{Workspace: ws, Adapter: "adp-2", ChannelID: "c2", LastSessionID: "sess-A"}); err != nil {
		t.Fatalf("Save: %v", err)
	}
	m.currentBindings["adp-1"] = &ChannelBinding{Workspace: ws, Adapter: "adp-1", ChannelID: "c1", LastSessionID: "sess-A"}
	m.currentBindings["adp-2"] = &ChannelBinding{Workspace: ws, Adapter: "adp-2", ChannelID: "c2", LastSessionID: "sess-A"}

	n, err := m.DisableAll()
	if err != nil || n != 2 {
		t.Fatalf("DisableAll = %d, %v; want 2, nil", n, err)
	}
	for _, adp := range []string{"adp-1", "adp-2"} {
		bindings, _ := store.ListByWorkspace(normalizeWorkspace(ws))
		for _, b := range bindings {
			if b.Adapter == adp && b.LastSessionID != "" {
				t.Fatalf("DisableAll must clear LastSessionID for %s so other sessions can claim; got %q", adp, b.LastSessionID)
			}
		}
	}
}

func TestIssue967EnableAllClaimsAndSkipsForeignLive(t *testing.T) {
	m, store := newManager967(t, true)
	ws := m.session.Workspace
	// Seed store rows so the EnableAll claim (UpdateSessionID) has a row to
	// update: owned: eligible for claim; foreignLive: must be skipped.
	if err := store.Save(ChannelBinding{Workspace: ws, Adapter: "adp-ours", ChannelID: "c1"}); err != nil {
		t.Fatalf("Save: %v", err)
	}
	if err := store.Save(ChannelBinding{Workspace: ws, Adapter: "adp-foreign", ChannelID: "c2", LastSessionID: "sess-B"}); err != nil {
		t.Fatalf("Save: %v", err)
	}
	m.disabledBindings["adp-ours"] = &ChannelBinding{Workspace: ws, Adapter: "adp-ours", ChannelID: "c1"}
	m.disabledBindings["adp-foreign"] = &ChannelBinding{Workspace: ws, Adapter: "adp-foreign", ChannelID: "c2", LastSessionID: "sess-B"}

	n, err := m.EnableAll()
	if err != nil {
		t.Fatalf("EnableAll: %v", err)
	}
	if n != 1 {
		t.Fatalf("EnableAll enabled %d, want 1 (foreign-live must be skipped)", n)
	}
	if !m.IsBindingDisabled("adp-foreign") {
		t.Fatal("foreign-live binding must remain disabled")
	}
	ours := m.currentBindings["adp-ours"]
	if ours == nil || ours.LastSessionID != "sess-A" {
		t.Fatalf("enabled binding must be claimed in memory for sess-A, got %+v", ours)
	}
	bindings, _ := store.ListByWorkspace(normalizeWorkspace(ws))
	for _, b := range bindings {
		if b.Adapter == "adp-ours" && b.LastSessionID != "sess-A" {
			t.Fatalf("EnableAll must persist LastSessionID=sess-A for adp-ours, got %q", b.LastSessionID)
		}
		if b.Adapter == "adp-foreign" && b.LastSessionID != "sess-B" {
			t.Fatalf("EnableAll must not rewrite foreign owner's LastSessionID for adp-foreign, got %q", b.LastSessionID)
		}
	}
}

// ---- Issue 3: ChannelID with whitespace must not lock out ----

func TestIssue967HandleInboundChannelIDWhitespaceNotDenied(t *testing.T) {
	m, store := newManager967(t, false)
	ws := m.session.Workspace
	if err := store.Save(ChannelBinding{Workspace: ws, Adapter: "test-adp"}); err != nil {
		t.Fatalf("Save: %v", err)
	}
	m.currentBindings["test-adp"] = &ChannelBinding{Workspace: ws, Adapter: "test-adp"}

	// First inbound: channel ID has surrounding whitespace. Before the fix
	// this returned ErrInboundChannelDenied (write trims, compare didn't).
	msg := InboundMessage{
		Envelope: Envelope{Adapter: "test-adp", ChannelID: "  ch-967  ", MessageID: "m1"},
		Text:     "hello",
	}
	if err := m.HandleInbound(context.Background(), msg); err != nil {
		t.Fatalf("first inbound with whitespace-padded channel ID must be accepted, got: %v", err)
	}
	// Second inbound from the same padded channel must also be accepted
	// (regression for the "permanently locked" symptom).
	msg2 := msg
	msg2.Envelope.MessageID = "m2"
	if err := m.HandleInbound(context.Background(), msg2); err != nil {
		t.Fatalf("second inbound from padded channel ID must be accepted, got: %v", err)
	}
	// A DIFFERENT channel must still be denied.
	msg3 := InboundMessage{
		Envelope: Envelope{Adapter: "test-adp", ChannelID: "other-channel", MessageID: "m3"},
		Text:     "intruder",
	}
	if err := m.HandleInbound(context.Background(), msg3); err == nil {
		t.Fatal("inbound from a different channel must still be denied")
	}
}

// ---- Ancillary: fanOutSend must report ctx-cancellation drops ----

func TestIssue967EmitCancelledCtxReportsError(t *testing.T) {
	m, _ := newManager967(t, false)
	m.RegisterSink(&countingSink967{})
	m.currentBindings["test-adp"] = &ChannelBinding{
		Workspace: m.session.Workspace,
		Adapter:   "test-adp",
		ChannelID: "ch-1",
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	err := m.Emit(ctx, OutboundEvent{Kind: OutboundEventText, Text: "hi"})
	if err == nil {
		t.Fatal("Emit with a cancelled context must not report success while all sends are dropped")
	}
	if !strings.Contains(err.Error(), "context canceled") {
		t.Fatalf("error should surface ctx cause, got: %v", err)
	}
}

// ---- Ancillary: HandleInbound persist failure must roll back binding ----

func TestIssue967HandleInboundPersistFailureRollsBack(t *testing.T) {
	m, _ := newManager967(t, false)
	ws := m.session.Workspace
	m.bindingStore = &failSaveStore{BindingStore: NewMemoryBindingStore()}
	m.currentBindings["test-adp"] = &ChannelBinding{Workspace: ws, Adapter: "test-adp"}

	msg := InboundMessage{
		Envelope: Envelope{Adapter: "test-adp", ChannelID: "ch-967", MessageID: "m1"},
		Text:     "hello",
	}
	if err := m.HandleInbound(context.Background(), msg); err == nil {
		t.Fatal("persist failure must surface as an error from HandleInbound")
	}
	// The in-memory mutations (ChannelID + LastInboundMessageID) must be
	// rolled back so memory matches the unchanged store entry.
	b := m.currentBindings["test-adp"]
	if b.ChannelID != "" || b.LastInboundMessageID != "" {
		t.Fatalf("binding must roll back on persist failure, got ChannelID=%q LastInboundMessageID=%q", b.ChannelID, b.LastInboundMessageID)
	}
}

// ---- Issue 4: unbind paths must stop the adapter ----

func TestIssue967UnbindPathsStopAdapter(t *testing.T) {
	type step struct {
		name string
		fn   func(m *Manager) error
	}
	steps := []step{
		{"UnbindChannel", func(m *Manager) error { return m.UnbindChannel(m.session.Workspace) }},
		{"DeleteBinding", func(m *Manager) error { return m.DeleteBinding("test-adp", m.session.Workspace) }},
		{"UnbindAdapter", func(m *Manager) error { return m.UnbindAdapter("test-adp") }},
	}
	for _, st := range steps {
		t.Run(st.name, func(t *testing.T) {
			m, store := newManager967(t, false)
			ws := m.session.Workspace
			if err := store.Save(ChannelBinding{Workspace: ws, Adapter: "test-adp", ChannelID: "ch-1"}); err != nil {
				t.Fatalf("Save: %v", err)
			}
			sink := &countingSink967{}
			m.RegisterSink(sink)
			ctx, cancel := context.WithCancel(context.Background())
			m.RegisterAdapterCancel("test-adp", cancel)
			m.currentBindings["test-adp"] = &ChannelBinding{Workspace: ws, Adapter: "test-adp", ChannelID: "ch-1"}

			if err := st.fn(m); err != nil {
				t.Fatalf("%s: %v", st.name, err)
			}
			// Cancel must have been invoked by stopAdapter.
			select {
			case <-ctx.Done():
			default:
				t.Fatalf("%s must stop the adapter (cancel not invoked)", st.name)
			}
			// Sink must be closed and unregistered.
			if sink.closeCount() == 0 {
				t.Fatalf("%s must close the adapter connection", st.name)
			}
			if m.sinks["test-adp"] != nil {
				t.Fatalf("%s must unregister the adapter sink", st.name)
			}
		})
	}
}
