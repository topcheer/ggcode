package a2aintrospect

import (
	"strings"

	"github.com/topcheer/ggcode/internal/a2a"
	"github.com/topcheer/ggcode/internal/debug"
)

// ActivityFromRingEntries maps in-memory debug ring entries to activity
// events for the a2a activity face (#5635 P2). The old face tailed the
// on-disk JSONL and labeled every line Tool:"log" with a "-N" pseudo
// timestamp; the ring is the same log stream debug.Log dual-writes, so
// the face now reads memory instead of disk and recovers the real tool
// name / OK flag from the log line shape where a "tool=<name>" marker
// exists (tool-result logging in the tool loop), falling back to the
// category label for lines without one.
func ActivityFromRingEntries(entries []debug.RingEntry) []a2a.RuntimeActivityEvent {
	events := make([]a2a.RuntimeActivityEvent, 0, len(entries))
	for _, e := range entries {
		ev := a2a.RuntimeActivityEvent{
			TS:   e.Time,
			Tool: e.Category,
			OK:   true,
			Size: len(e.Message),
		}
		if name, ok := toolFromLogLine(e.Message); ok {
			ev.Tool = name
			// Match the writer-side marker (chat_tool_loop.go logs
			// "tool result ERROR: tool=<name>"), not a bare "ERROR"
			// substring: ordinary payloads carrying the word ERROR
			// (paths, params) must not tint the OK flag.
			ev.OK = !strings.Contains(e.Message, "result ERROR: tool=")
		}
		events = append(events, ev)
	}
	return events
}

// ActivityStatsFromEvents aggregates a sampled activity window into the
// health block of the activity face (#5657-followup plan item 5): tool
// failure rate and same-tool consecutive-error retry chains.
func ActivityStatsFromEvents(events []a2a.RuntimeActivityEvent) *a2a.RuntimeActivityStats {
	st := &a2a.RuntimeActivityStats{}
	lastFailTool := ""
	for _, ev := range events {
		if ev.Tool == ev.TS && ev.TS == "" {
			continue
		}
		st.ToolCalls++
		if !ev.OK {
			st.ToolFailures++
			if ev.Tool == lastFailTool {
				st.RetryChains++
			} else {
				lastFailTool = ev.Tool
			}
		} else {
			lastFailTool = ""
		}
	}
	if st.ToolCalls > 0 {
		st.FailurePct = float64(st.ToolFailures) / float64(st.ToolCalls) * 100
	}
	return st
}

// toolFromLogLine extracts the tool name from a debug log line carrying
// a "tool=<name>" marker, e.g. "[agent] tool result ERROR: tool=run_command".
func toolFromLogLine(msg string) (string, bool) {
	const marker = "tool="
	i := strings.Index(msg, marker)
	if i < 0 {
		return "", false
	}
	rest := msg[i+len(marker):]
	if j := strings.IndexAny(rest, " \t"); j >= 0 {
		rest = rest[:j]
	}
	if rest == "" {
		return "", false
	}
	return rest, true
}
