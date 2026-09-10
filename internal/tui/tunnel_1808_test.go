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

// #1808: snapshot replay must preserve pure-whitespace separator chunks
// (the runner keeps them deliberately - trimming glues words together);
// routing them through NormalizeReasoningChunk returned "" and replay
// rebuilt the text with words glued.
func Test1808ReplayPreservesWhitespaceChunks(t *testing.T) {
	got := snapshotReasoningChunk(t, "hello ", " ", "world")
	if got != "hello  world" {
		t.Fatalf("whitespace separator must survive replay, got %q", got)
	}
}

// #1808: sentinel replacement still works on the replay path, including
// whitespace-padded forms (exact-match leaked them in the runner's stream
// path; the replay path must not regress either direction).
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
