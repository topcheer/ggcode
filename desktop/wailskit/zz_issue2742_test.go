package wailskit

// Issue #2742: pendingDigests staged turn digests for a saveSession() sink
// that was removed with #594 — the channel had zero consumers (no read side,
// no persistence, no forwarding) yet kept being appended and cleared, and the
// comment still claimed the digests would be persisted on the next save.
// The dead channel is deleted; the REAL sinks (liveHistory + frontend event
// stream) are pinned here so the deletion cannot regress digest delivery.

import (
	"encoding/json"
	"strings"
	"testing"
	"time"

	"github.com/topcheer/ggcode/internal/metrics"
)

func issue2742Bridge() *ChatBridge {
	return &ChatBridge{
		usageTurnIndex: 1,
		metricEvents: []metrics.MetricEvent{
			{Timestamp: time.Now(), TurnIndex: 1, Type: "llm", Duration: 2 * time.Second, InputTokens: 10, OutputTokens: 5},
		},
	}
}

func TestIssue2742DigestStillReachesLiveHistoryAndFrontend(t *testing.T) {
	b := issue2742Bridge()

	var gotType string
	var gotText string
	b.OnStreamEvent = func(eventType string, data json.RawMessage) {
		gotType = eventType
		var m map[string]string
		_ = json.Unmarshal(data, &m)
		gotText = m["text"]
	}
	b.emitTurnDigest()

	// Sink 1: liveHistory (rendered by CurrentSessionHistory).
	b.mu.Lock()
	found := false
	for _, sm := range b.liveHistory {
		if sm.Role == "system" && strings.TrimSpace(sm.Content) != "" {
			found = true
		}
	}
	b.mu.Unlock()
	if !found {
		t.Fatal("turn digest missing from liveHistory after emitTurnDigest")
	}

	// Sink 2: frontend event stream.
	if gotType != "system" || gotText == "" {
		t.Fatalf("frontend digest event not emitted: type=%q text=%q", gotType, gotText)
	}
}

func TestIssue2742DigestNotDuplicatedAfterReemit(t *testing.T) {
	b := issue2742Bridge()
	b.emitTurnDigest()
	b.emitTurnDigest() // same turn: must not re-emit (lastMetricDigestTurn gate)
	b.mu.Lock()
	n := 0
	for _, sm := range b.liveHistory {
		if sm.Role == "system" {
			n++
		}
	}
	b.mu.Unlock()
	if n != 1 {
		t.Fatalf("expected exactly 1 system digest in liveHistory, got %d", n)
	}
}
