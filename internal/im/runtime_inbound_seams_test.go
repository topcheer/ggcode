package im

// Pins for the r187 inbound-routing seams on Manager.HandleInbound
// (runtime.go). Behavior-preserving extraction: HandleInbound keeps the
// orchestration (lock → dedup → mute → prepare → snapshot/callback →
// submit → rollback-on-error) and the phases move to *Locked helpers with
// the caller-holds-m.mu contract. Lock order and every failure-path
// rollback (#540) are pinned here. Existing end-to-end coverage
// (zz_issue540_fix_test.go, issue_967_test.go, runtime_mute_test.go) is
// untouched and must keep passing unmodified.

import (
	"errors"
	"testing"
	"time"
)

func TestDedupInboundLockedPins(t *testing.T) {
	m := NewManager()
	m.mu.Lock()
	defer m.mu.Unlock()

	// Fresh message: marked, key = adapter:msgID.
	key, dup := m.dedupInboundLocked(InboundMessage{Envelope: Envelope{Adapter: "slack", MessageID: "m1"}})
	if dup || key != "slack:m1" {
		t.Fatalf("fresh: dup=%v key=%q, want false / slack:m1", dup, key)
	}
	if len(m.seenMessages) != 1 {
		t.Fatalf("fresh must be marked: seen=%v", m.seenMessages)
	}

	// Redelivery of the same MessageID must be reported for skipping.
	key, dup = m.dedupInboundLocked(InboundMessage{Envelope: Envelope{Adapter: "slack", MessageID: "m1"}})
	if !dup || key != "slack:m1" {
		t.Fatalf("redelivery: dup=%v key=%q, want true / slack:m1", dup, key)
	}
	if len(m.seenMessages) != 1 {
		t.Fatalf("redelivery must not add a mark: seen=%v", m.seenMessages)
	}

	// No MessageID: no dedup key, no mark (rollback no-op territory).
	key, dup = m.dedupInboundLocked(InboundMessage{Envelope: Envelope{Adapter: "tg"}})
	if dup || key != "" {
		t.Fatalf("no msgID: dup=%v key=%q, want false / empty", dup, key)
	}
	if len(m.seenMessages) != 1 {
		t.Fatalf("no-msgID must not mark: seen=%v", m.seenMessages)
	}
}

func TestPruneSeenLockedPins(t *testing.T) {
	m := NewManager()
	m.seenMessages["stale"] = time.Now().Add(-6 * time.Minute)
	m.seenMessages["fresh"] = time.Now()

	m.mu.Lock()
	m.pruneSeenLocked()
	m.mu.Unlock()

	if _, ok := m.seenMessages["stale"]; ok {
		t.Fatal("entry older than 5m must be pruned")
	}
	if _, ok := m.seenMessages["fresh"]; !ok {
		t.Fatal("fresh entry must survive pruning")
	}
}

func TestPrepareInboundLockedGuardPins(t *testing.T) {
	// rollback mirrors the orchestrator closure: deletes the seen-mark.
	rollback := func(m *Manager) func() {
		return func() { delete(m.seenMessages, "slack:m1") }
	}
	newMsg := func(channelID string) *InboundMessage {
		return &InboundMessage{Envelope: Envelope{Adapter: "slack", ChannelID: channelID, MessageID: "m1"}}
	}
	newM := func() *Manager {
		m := NewManager()
		m.seenMessages["slack:m1"] = time.Now()
		return m
	}

	// Gate 1: no session → ErrNoSessionBound, dedup rolled back.
	m := newM()
	if _, err := m.prepareInboundLocked(newMsg("C1"), false, &errBridge540{}, rollback(m)); !errors.Is(err, ErrNoSessionBound) {
		t.Fatalf("no session: err=%v, want ErrNoSessionBound", err)
	}
	if _, ok := m.seenMessages["slack:m1"]; ok {
		t.Fatal("no-session failure must roll back the dedup mark (#540)")
	}

	// Gate 2: session but no binding → ErrNoChannelBound, dedup rolled back.
	m = newM()
	m.BindSession(SessionBinding{Workspace: "ws", SessionID: "s1"})
	if _, err := m.prepareInboundLocked(newMsg("C1"), m.session != nil, nil, rollback(m)); !errors.Is(err, ErrNoChannelBound) {
		t.Fatalf("no binding: err=%v, want ErrNoChannelBound", err)
	}
	if _, ok := m.seenMessages["slack:m1"]; ok {
		t.Fatal("no-binding failure must roll back the dedup mark (#540)")
	}

	// Gate 3: binding but no bridge → ErrNoBridge, dedup rolled back.
	m = newM()
	m.BindSession(SessionBinding{Workspace: "ws", SessionID: "s1"})
	m.currentBindings["slack"] = &ChannelBinding{Workspace: "ws", Adapter: "slack", ChannelID: "C1"}
	if _, err := m.prepareInboundLocked(newMsg("C1"), true, nil, rollback(m)); !errors.Is(err, ErrNoBridge) {
		t.Fatalf("no bridge: err=%v, want ErrNoBridge", err)
	}
	if _, ok := m.seenMessages["slack:m1"]; ok {
		t.Fatal("no-bridge failure must roll back the dedup mark (#540)")
	}

	// Gate 4: inbound channel mismatches the binding → ErrInboundChannelDenied.
	m = newM()
	m.BindSession(SessionBinding{Workspace: "ws", SessionID: "s1"})
	m.currentBindings["slack"] = &ChannelBinding{Workspace: "ws", Adapter: "slack", ChannelID: "C1"}
	if _, err := m.prepareInboundLocked(newMsg("C2"), true, &errBridge540{}, rollback(m)); !errors.Is(err, ErrInboundChannelDenied) {
		t.Fatalf("denied channel: err=%v, want ErrInboundChannelDenied", err)
	}
	if _, ok := m.seenMessages["slack:m1"]; ok {
		t.Fatal("denied-channel failure must roll back the dedup mark (#540)")
	}
}

func TestPrepareInboundLockedMutationPins(t *testing.T) {
	m := NewManager()
	m.BindSession(SessionBinding{Workspace: "ws", SessionID: "s1"})
	binding := &ChannelBinding{Workspace: "ws", Adapter: "slack", ChannelID: "C1"}
	m.currentBindings["slack"] = binding
	msg := &InboundMessage{Envelope: Envelope{Adapter: "slack", ChannelID: "C1", MessageID: "m9"}}

	changed, err := m.prepareInboundLocked(msg, true, &errBridge540{}, func() {})
	if err != nil {
		t.Fatalf("happy path: %v", err)
	}
	if !changed {
		t.Fatal("last-inbound update must report changed=true")
	}
	// ReceivedAt fill must land on the caller's copy: the orchestrator
	// submits this same msg to the bridge afterwards.
	if msg.Envelope.ReceivedAt.IsZero() {
		t.Fatal("ReceivedAt must be filled on the caller's msg copy")
	}
	if binding.LastInboundMessageID != "m9" {
		t.Fatalf("LastInboundMessageID = %q, want m9", binding.LastInboundMessageID)
	}
	if !binding.LastInboundAt.Equal(msg.Envelope.ReceivedAt) {
		t.Fatalf("LastInboundAt must equal the filled ReceivedAt")
	}
	if binding.PassiveReplyCount != 0 {
		t.Fatalf("PassiveReplyCount must reset, got %d", binding.PassiveReplyCount)
	}
	if !binding.PassiveReplyStartedAt.IsZero() {
		t.Fatal("PassiveReplyStartedAt must reset to zero time")
	}

	// ChannelID claim: empty binding ChannelID adopts the (trimmed) inbound one.
	binding2 := &ChannelBinding{Workspace: "ws", Adapter: "slack"}
	m.currentBindings["slack2"] = binding2
	msg2 := &InboundMessage{Envelope: Envelope{Adapter: "slack2", ChannelID: "  C9  ", MessageID: "m10"}}
	changed, err = m.prepareInboundLocked(msg2, true, &errBridge540{}, func() {})
	if err != nil || !changed {
		t.Fatalf("channel claim: changed=%v err=%v, want true/nil", changed, err)
	}
	if binding2.ChannelID != "C9" {
		t.Fatalf("ChannelID must adopt the TRIMMED inbound value, got %q", binding2.ChannelID)
	}
}
