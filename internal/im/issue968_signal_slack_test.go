package im

import (
	"context"
	"encoding/json"
	"sync"
	"testing"
	"time"

	"github.com/topcheer/ggcode/internal/config"
)

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

	if got, _ := envelope["sourceNumber"].(string); got != "+1234567890" {
		t.Errorf("sourceNumber = %q, want %q (sender extraction would fail)", got, "+1234567890")
	}
	if got, _ := envelope["sourceName"].(string); got != "Me" {
		t.Errorf("sourceName = %q, want %q", got, "Me")
	}

	a.mu.RLock()
	_, marked := a.seen[1718000000000]
	a.mu.RUnlock()
	if !marked {
		t.Error("note-to-self message was dropped before dedup marking (sender extraction failed)")
	}
}

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

func TestSignalGroupSyncSelfBypassAllowedUsers(t *testing.T) {
	adapterCfg := config.IMAdapterConfig{
		Enabled:  true,
		Platform: "signal",
		Extra: map[string]interface{}{
			"account":         "+1234567890",
			"allowed_users":   "+9999999999",
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

func TestSlackAckForEnvelopeWithoutResponsePayload(t *testing.T) {

	if _, ok := slackAckForEnvelope(""); ok {
		t.Error("empty envelope_id should not produce an ack")
	}

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

	if !a.markSeenDedup("C222", "1718000000.000100") {
		t.Error("same ts in another channel must be processed")
	}

	if !a.markSeenDedup("C111", "1718000000.000200") {
		t.Error("new ts in same channel must be processed")
	}

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
