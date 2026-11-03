package tui

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/topcheer/ggcode/internal/subagent"
	"github.com/topcheer/ggcode/internal/tunnel"
)

func snapshotReasoningChunk(t *testing.T, texts ...string) string {
	t.Helper()
	events := make([]subagent.AgentEvent, 0, len(texts))
	for _, txt := range texts {
		events = append(events, subagent.AgentEvent{Type: subagent.AgentEventReasoning, Text: txt})
	}
	out := tunnelSnapshotAgentEvents("a1", "t1", "32", events, "", "", "completed", "", "")
	for _, ev := range out {
		if ev.Type == tunnel.EventSubagentReasoning {
			var data tunnel.SubagentReasoningData
			if err := json.Unmarshal(ev.Data, &data); err == nil {
				return data.Chunk
			}
		}
	}
	return ""
}

func Test1808ReplayPreservesWhitespaceChunks(t *testing.T) {
	got := snapshotReasoningChunk(t, "hello ", " ", "world")
	if got != "hello  world" {
		t.Fatalf("whitespace separator must survive replay, got %q", got)
	}
}

func Test1808ReplayReplacesSentinel(t *testing.T) {
	for _, pad := range []string{"", "\n", " "} {
		got := snapshotReasoningChunk(t, "before ", tunnel.RedactedReasoningSentinel+pad, " after")
		if strings.Contains(got, tunnel.RedactedReasoningSentinel) {
			t.Fatalf("sentinel (pad=%q) must never reach display, got %q", pad, got)
		}
		if !strings.Contains(got, tunnel.RedactedReasoningPlaceholder) {
			t.Fatalf("placeholder (pad=%q) must be substituted, got %q", pad, got)
		}
	}
}
