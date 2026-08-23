package im

// Regression tests for issue #968:
//  1. signal Note to Self messages were 100% dropped (missing source fields)
//  2. slack Socket Mode ack required accepts_response_payload==true
//  3. slack handleMessage had no (channel, ts) dedup table
// plus small follow-ups: group-sync sender bypassing allowed_users.

import (
	"context"
	"encoding/json"
	"sync"
	"testing"
	"time"

	"github.com/topcheer/ggcode/internal/config"
)

// --- #968 problem 1: signal Note to Self -------------------------------

// TestSignalNoteToSelfNotDropped verifies that a syncMessage envelope whose
// destination is the adapter's own account (Note to Self, sent from another
// device) survives sender extraction. Observable proxy: processEnvelope marks
// the message timestamp in the seen dedup map ONLY after the sender gate -
// before the fix the envelope was dropped at sender extraction ("") and the
// seen map stayed empty.
func TestSignalNoteToSelfNotDropped(t *testing.T) {
	adapterCfg := config.IMAdapterConfig{
		Enabled:  true,
		Platform: "signal",
		Extra: map[string]interface{}{
			"account": "+1234567890",
		},
	}
	a, err := newSignalAdapter("test", config.IMConfig{}, adapterCfg, nil)
	if err != nil {
		t.Fatalf("newSignalAdapter: %v", err)
	}

	envelope := map[string]any{
		"syncMessage": map[string]any{
			"sentMessage": map[string]any{
				"destinationNumber": "+1234567890",
				"message":           "remember the milk",
				"timestamp":         float64(1718000000000),
			},
		},
	}
	a.processEnvelope(context.Background(), envelope)

	// The fix sets source fields on the envelope itself.
	if got, _ := envelope["sourceNumber"].(string); got != "+1234567890" {
		t.Errorf("sourceNumber = %q, want %q (sender extraction would fail)", got, "+1234567890")
	}
	if got, _ := envelope["sourceName"].(string); got != "Me" {
		t.Errorf("sourceName = %q, want %q", got, "Me")
	}

	// Passed the sender gate => timestamp recorded in the dedup map.
	a.mu.RLock()
	_, marked := a.seen[1718000000000]
	a.mu.RUnlock()
	if !marked {
		t.Error("note-to-self message was dropped before dedup marking (sender extraction failed)")
	}
}

// TestSignalNoteToSelfEchoSuppressionStillWorks verifies the echo-suppression
// path is intact: a sent-timestamp match must still return before any
// inbound processing (the fix must not reintroduce self-echo loops).
func TestSignalNoteToSelfEchoSuppressionStillWorks(t *testing.T) {
	adapterCfg := config.IMAdapterConfig{
		Enabled:  true,
		Platform: "signal",
		Extra: map[string]interface{}{
			"account": "+1234567890",
		},
	}
	a, _ := newSignalAdapter("test", config.IMConfig{}, adapterCfg, nil)

	const ts = int64(1718000000999)
	a.addSentTimestamp(ts)
	envelope := map[string]any{
		"syncMessage": map[string]any{
			"sentMessage": map[string]any{
				"destinationNumber": "+1234567890",
				"message":           "echo of our own send",
				"timestamp":         float64(ts),
			},
		},
	}
	a.processEnvelope(context.Background(), envelope)

	a.mu.RLock()
	_, marked := a.seen[ts]
	a.mu.RUnlock()
	if marked {
		t.Error("echo-suppressed self-send should not be marked as inbound")
	}
}

// --- #968 follow-up: group sync from own account bypasses allowed_users ---

// TestSignalGroupSyncSelfBypassAllowedUsers: a group sync message whose
// sender is the account itself must not be killed by a restrictive
// allowed_users list (it legitimately originates from this account).
func TestSignalGroupSyncSelfBypassAllowedUsers(t *testing.T) {
	adapterCfg := config.IMAdapterConfig{
		Enabled:  true,
		Platform: "signal",
		Extra: map[string]interface{}{
			"account":         "+1234567890",
			"allowed_users":   "+9999999999", // does NOT include own account
			"group_allowlist": "*",
		},
	}
	a, _ := newSignalAdapter("test", config.IMConfig{}, adapterCfg, nil)

	const ts = int64(1718000001234)
	envelope := map[string]any{
		"syncMessage": map[string]any{
			"sentMessage": map[string]any{
				"groupInfo": map[string]any{"groupId": "group-abc"},
				"message":   "synced from my phone",
				"timestamp": float64(ts),
			},
		},
	}
	a.processEnvelope(context.Background(), envelope)

	a.mu.RLock()
	_, marked := a.seen[ts]
	a.mu.RUnlock()
	if !marked {
		t.Error("group sync from own account was dropped by allowed_users filter")
	}
}

// --- #968 problem 2: slack Socket Mode ack ------------------------------

// TestSlackAckForEnvelopeWithoutResponsePayload verifies an ack is produced
// for envelopes regardless of accepts_response_payload. Slack requires an
// ack for EVERY envelope with an envelope_id; the flag only governs whether
// the ack may carry a payload. message/app_mention envelopes commonly carry
// accepts_response_payload=false and previously went unacked (=> redelivery).
func TestSlackAckForEnvelopeWithoutResponsePayload(t *testing.T) {
	// Empty envelope_id (hello/disconnect frames) - nothing to ack.
	if _, ok := slackAckForEnvelope(""); ok {
		t.Error("empty envelope_id should not produce an ack")
	}

	// Envelope id as delivered for message events with
	// accepts_response_payload=false - must still ack.
	ack, ok := slackAckForEnvelope("16d5332e-XXXX")
	if !ok {
		t.Fatal("envelope with accepts_response_payload=false must be acked")
	}
	var decoded map[string]any
	if err := json.Unmarshal(ack, &decoded); err != nil {
		t.Fatalf("ack frame is not valid JSON: %v", err)
	}
	if got, _ := decoded["envelope_id"].(string); got != "16d5332e-XXXX" {
		t.Errorf("ack envelope_id = %q, want %q", got, "16d5332e-XXXX")
	}
}

// --- #968 problem 3: slack (channel, ts) dedup --------------------------

// TestSlackMarkSeenDedup verifies the dedup table: the same (channel, ts) is
// processed exactly once, while other channels/ts pairs are independent.
func TestSlackMarkSeenDedup(t *testing.T) {
	a := &slackAdapter{name: "test", seen: make(map[string]time.Time)}

	if !a.markSeenDedup("C111", "1718000000.000100") {
		t.Error("first delivery must be processed")
	}
	if a.markSeenDedup("C111", "1718000000.000100") {
		t.Error("duplicate (channel, ts) must be dropped")
	}
	if a.markSeenDedup("C111", "1718000000.000100") {
		t.Error("third delivery of the same (channel, ts) must be dropped")
	}

	// Same ts in a different channel is a different message.
	if !a.markSeenDedup("C222", "1718000000.000100") {
		t.Error("same ts in another channel must be processed")
	}
	// Same channel, new ts is a new message.
	if !a.markSeenDedup("C111", "1718000000.000200") {
		t.Error("new ts in same channel must be processed")
	}

	// Unknown ids must not crash or poison the table.
	if !a.markSeenDedup("", "1718000000.000300") {
		t.Error("empty channel should pass through (no dedup key)")
	}
	if !a.markSeenDedup("C111", "") {
		t.Error("empty ts should pass through (no dedup key)")
	}

	a.mu.RLock()
	n := len(a.seen)
	a.mu.RUnlock()
	if n != 3 {
		t.Errorf("seen table size = %d, want 3 (empty ids are not keyed)", n)
	}
}

// TestSlackMarkSeenDedup_Concurrent verifies the dedup table is race-free
// under concurrent delivery of the same message (Socket Mode can redeliver
// on multiple connections during reconnect).
func TestSlackMarkSeenDedup_Concurrent(t *testing.T) {
	a := &slackAdapter{name: "test", seen: make(map[string]time.Time)}
	const workers = 16
	first := make(chan struct{}, workers)
	var wg sync.WaitGroup
	for i := 0; i < workers; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			if a.markSeenDedup("C333", "1718000000.555500") {
				first <- struct{}{}
			}
		}()
	}
	wg.Wait()
	close(first)
	wins := 0
	for range first {
		wins++
	}
	if wins != 1 {
		t.Errorf("exactly one goroutine should win the race, got %d", wins)
	}
	a.mu.RLock()
	n := len(a.seen)
	a.mu.RUnlock()
	if n != 1 {
		t.Errorf("seen table size = %d, want exactly 1 entry for concurrent same-key deliveries", n)
	}
}
