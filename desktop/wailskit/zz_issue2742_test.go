package wailskit

import (
	"encoding/json"
	"strings"
	"testing"
	"time"

	"github.com/topcheer/ggcode/internal/metrics"
	"github.com/topcheer/ggcode/internal/provider"
	"github.com/topcheer/ggcode/internal/session"
)

func issue2742Bridge(events []metrics.MetricEvent) (*ChatBridge, *[]string) {
	var emitted []string
	b := &ChatBridge{
		usageTurnIndex:       1,
		lastMetricDigestTurn: 0,
		metricEvents:         events,
		OnStreamEvent: func(name string, raw json.RawMessage) {
			if name == "system" {
				var m map[string]string
				_ = json.Unmarshal(raw, &m)
				emitted = append(emitted, m["text"])
			}
		},
	}
	return b, &emitted
}

func issue2742LLMEvent(turn int) metrics.MetricEvent {
	return metrics.MetricEvent{
		Timestamp:    time.Now(),
		TurnIndex:    turn,
		Type:         "llm",
		Duration:     10 * time.Millisecond,
		InputTokens:  100,
		OutputTokens: 20,
	}
}

func TestIssue2742_DigestStillReachesLiveHistoryAndFrontend(t *testing.T) {
	b, emitted := issue2742Bridge([]metrics.MetricEvent{issue2742LLMEvent(1)})
	b.emitTurnDigest()

	if len(b.liveHistory) != 1 {
		t.Fatalf("liveHistory got %d entries, want 1 (digest must persist in-memory)", len(b.liveHistory))
	}
	if b.liveHistory[0].Role != "system" || b.liveHistory[0].Content == "" {
		t.Fatalf("liveHistory[0] = %+v, want non-empty system digest", b.liveHistory[0])
	}
	if len(*emitted) != 1 {
		t.Fatalf("frontend system events = %d, want 1", len(*emitted))
	}
	if !strings.Contains((*emitted)[0], b.liveHistory[0].Content) {
		t.Fatalf("frontend event text %q does not match liveHistory digest %q", (*emitted)[0], b.liveHistory[0].Content)
	}
}

func TestIssue2742_SameTurnDigestDeduped(t *testing.T) {
	b, emitted := issue2742Bridge([]metrics.MetricEvent{issue2742LLMEvent(1)})
	b.emitTurnDigest()
	b.emitTurnDigest()

	if len(b.liveHistory) != 1 || len(*emitted) != 1 {
		t.Fatalf("dedup failed: liveHistory=%d emitted=%d, want 1/1", len(b.liveHistory), len(*emitted))
	}
	if b.lastMetricDigestTurn != 1 {
		t.Fatalf("lastMetricDigestTurn = %d, want 1", b.lastMetricDigestTurn)
	}
}

func TestIssue2742_DigestDoesNotTouchSessionMessages(t *testing.T) {
	b, _ := issue2742Bridge([]metrics.MetricEvent{issue2742LLMEvent(1)})
	b.currentSes = &session.Session{Messages: []provider.Message{
		{Role: "user", Content: []provider.ContentBlock{provider.TextBlock("hi")}},
	}}
	before := len(b.currentSes.Messages)
	b.emitTurnDigest()
	if len(b.currentSes.Messages) != before {
		t.Fatalf("currentSes.Messages grew %d -> %d; digest must not stage into session messages", before, len(b.currentSes.Messages))
	}
}

func TestIssue2742_RestoredStateSuppressesReEmit(t *testing.T) {
	b, emitted := issue2742Bridge([]metrics.MetricEvent{issue2742LLMEvent(1)})
	b.lastMetricDigestTurn = 1
	b.emitTurnDigest()
	if len(b.liveHistory) != 0 || len(*emitted) != 0 {
		t.Fatalf("restored state should suppress re-emit: liveHistory=%d emitted=%d", len(b.liveHistory), len(*emitted))
	}
}
