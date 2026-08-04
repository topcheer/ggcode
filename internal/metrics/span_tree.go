package metrics

import (
	"crypto/sha1"
	"encoding/hex"
	"fmt"
	"sort"
	"time"
)

// SpanNode represents a single span in a hierarchical execution trace tree.
// Modeled after OpenTelemetry/LangSmith span semantics: each span has a unique
// ID, an optional parent ID, a name, start/end timestamps, and arbitrary
// attributes (tokens, cost, success, error, etc.).
//
// The tree structure is: Session root → Turn spans → LLM/tool child spans.
// This hierarchy enables visualization in tools that expect span trees
// (LangSmith, Langfuse, Jaeger, Phoenix/Arize) and supports critical-path
// analysis for latency debugging.
type SpanNode struct {
	SpanID     string                 `json:"span_id"`
	ParentID   string                 `json:"parent_id,omitempty"`
	Name       string                 `json:"name"`
	Kind       string                 `json:"kind"` // "session", "turn", "llm", "tool"
	StartTime  time.Time              `json:"start_time"`
	EndTime    time.Time              `json:"end_time"`
	Attributes map[string]interface{} `json:"attributes,omitempty"`
	Children   []SpanNode             `json:"children,omitempty"`
}

// Duration returns the wall-clock duration of this span.
func (s SpanNode) Duration() time.Duration {
	if s.EndTime.IsZero() || s.StartTime.IsZero() {
		return 0
	}
	return s.EndTime.Sub(s.StartTime)
}

// spanID generates a deterministic, stable ID from a set of key parts.
// This ensures the same events always produce the same span IDs, making
// traces reproducible and diff-able across runs.
func spanID(parts ...string) string {
	h := sha1.New()
	for _, p := range parts {
		h.Write([]byte(p))
		h.Write([]byte{0})
	}
	return hex.EncodeToString(h.Sum(nil)[:8])
}

// BuildSpanTree converts a flat list of MetricEvents into a hierarchical
// SpanNode tree. The root is a "session" span; each turn becomes a child
// "turn" span; LLM calls and tool executions become grandchildren.
//
// Because MetricEvents are emitted at completion time (they carry Duration
// but not explicit start/end timestamps), we reconstruct start times by
// subtracting duration from the event timestamp. This is deterministic and
// does not require additional instrumentation.
//
// The resulting tree can be serialized to JSON and consumed by external
// observability tools that expect span-tree formats.
func BuildSpanTree(sessionID string, events []MetricEvent) SpanNode {
	if len(events) == 0 {
		return SpanNode{
			SpanID: spanID(sessionID),
			Name:   sessionID,
			Kind:   "session",
		}
	}

	// Group events by turn index, preserving chronological order within each turn.
	turnEvents := make(map[int][]MetricEvent)
	turnOrder := make([]int, 0)
	for _, ev := range events {
		if _, exists := turnEvents[ev.TurnIndex]; !exists {
			turnOrder = append(turnOrder, ev.TurnIndex)
		}
		turnEvents[ev.TurnIndex] = append(turnEvents[ev.TurnIndex], ev)
	}
	sort.Ints(turnOrder)

	// Determine session span boundaries from all event timestamps.
	var minStart, maxEnd time.Time
	for _, ev := range events {
		start := eventStart(ev)
		end := ev.Timestamp
		if !start.IsZero() {
			if minStart.IsZero() || start.Before(minStart) {
				minStart = start
			}
		}
		if !end.IsZero() {
			if maxEnd.IsZero() || end.After(maxEnd) {
				maxEnd = end
			}
		}
	}

	rootID := spanID(sessionID)
	root := SpanNode{
		SpanID:    rootID,
		Name:      sessionID,
		Kind:      "session",
		StartTime: minStart,
		EndTime:   maxEnd,
		Children:  make([]SpanNode, 0, len(turnOrder)),
	}

	for _, turnIdx := range turnOrder {
		evs := turnEvents[turnIdx]
		turnSpan := buildTurnSpan(rootID, turnIdx, evs)
		root.Children = append(root.Children, turnSpan)
	}

	return root
}

// buildTurnSpan creates a "turn" span containing all LLM and tool events
// for a given turn index.
func buildTurnSpan(parentID string, turnIdx int, events []MetricEvent) SpanNode {
	turnID := spanID(parentID, fmt.Sprintf("turn-%d", turnIdx))

	// Compute turn-level time bounds.
	var minStart, maxEnd time.Time
	for _, ev := range events {
		start := eventStart(ev)
		end := ev.Timestamp
		if !start.IsZero() {
			if minStart.IsZero() || start.Before(minStart) {
				minStart = start
			}
		}
		if !end.IsZero() {
			if maxEnd.IsZero() || end.After(maxEnd) {
				maxEnd = end
			}
		}
	}

	// Sort events by start time for deterministic child ordering.
	sort.SliceStable(events, func(i, j int) bool {
		return eventStart(events[i]).Before(eventStart(events[j]))
	})

	turn := SpanNode{
		SpanID:    turnID,
		ParentID:  parentID,
		Name:      fmt.Sprintf("turn %d", turnIdx),
		Kind:      "turn",
		StartTime: minStart,
		EndTime:   maxEnd,
		Children:  make([]SpanNode, 0, len(events)),
	}

	for i, ev := range events {
		child := buildLeafSpan(turnID, turnIdx, i, ev)
		turn.Children = append(turn.Children, child)
	}

	return turn
}

// buildLeafSpan creates an "llm" or "tool" span from a single MetricEvent.
func buildLeafSpan(parentID string, turnIdx, seqIdx int, ev MetricEvent) SpanNode {
	start := eventStart(ev)
	end := ev.Timestamp
	if end.IsZero() {
		end = start
	}

	var name, kind string
	attrs := make(map[string]interface{})

	switch ev.Type {
	case "llm":
		kind = "llm"
		name = "llm.generate"
		if ev.Model != "" {
			name = fmt.Sprintf("llm.generate (%s)", ev.Model)
			attrs["model"] = ev.Model
		}
		attrs["ttft_ms"] = ev.TTFT.Milliseconds()
		attrs["think_time_ms"] = ev.ThinkTime.Milliseconds()
		attrs["input_tokens"] = ev.InputTokens
		attrs["output_tokens"] = ev.OutputTokens
		attrs["cache_read_tokens"] = ev.CacheRead
		attrs["cache_write_tokens"] = ev.CacheWrite
	case "tool":
		kind = "tool"
		toolName := ev.ToolName
		if toolName == "" {
			toolName = "unknown"
		}
		name = fmt.Sprintf("tool.%s", toolName)
		attrs["tool_name"] = toolName
		attrs["success"] = ev.ToolSuccess
		if ev.ToolError != "" {
			attrs["error"] = ev.ToolError
		}
		attrs["duration_ms"] = ev.ToolDuration.Milliseconds()
	default:
		kind = ev.Type
		name = ev.Type
	}

	if ev.Vendor != "" {
		attrs["vendor"] = ev.Vendor
	}

	return SpanNode{
		SpanID:     spanID(parentID, fmt.Sprintf("%s-%d-%d", kind, turnIdx, seqIdx)),
		ParentID:   parentID,
		Name:       name,
		Kind:       kind,
		StartTime:  start,
		EndTime:    end,
		Attributes: attrs,
	}
}

// eventStart reconstructs the start time of an event by subtracting its
// duration from its completion timestamp.
func eventStart(ev MetricEvent) time.Time {
	if ev.Timestamp.IsZero() {
		return time.Time{}
	}
	switch ev.Type {
	case "llm":
		if ev.Duration > 0 {
			return ev.Timestamp.Add(-ev.Duration)
		}
	case "tool":
		if ev.ToolDuration > 0 {
			return ev.Timestamp.Add(-ev.ToolDuration)
		}
	}
	return ev.Timestamp
}

// CountSpans returns the total number of spans in a tree (root + all descendants).
func CountSpans(node SpanNode) int {
	count := 1
	for _, child := range node.Children {
		count += CountSpans(child)
	}
	return count
}

// FlattenSpans returns a flat list of all spans in the tree, useful for
// tools that prefer a flat array with parent_id references (OTLP-style).
func FlattenSpans(node SpanNode) []SpanNode {
	result := []SpanNode{node}
	for _, child := range node.Children {
		result = append(result, FlattenSpans(child)...)
	}
	return result
}

// FindErrorSpans returns all tool spans that recorded a failure (success=false
// or non-empty error). Useful for root-cause analysis of execution failures.
func FindErrorSpans(node SpanNode) []SpanNode {
	var errors []SpanNode
	if node.Kind == "tool" {
		isError := false
		if success, ok := node.Attributes["success"].(bool); ok && !success {
			isError = true
		}
		if errMsg, ok := node.Attributes["error"].(string); ok && errMsg != "" {
			isError = true
		}
		if isError {
			errors = append(errors, node)
		}
	}
	for _, child := range node.Children {
		errors = append(errors, FindErrorSpans(child)...)
	}
	return errors
}
