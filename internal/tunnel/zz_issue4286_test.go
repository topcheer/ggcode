package tunnel

import (
	"encoding/json"
	"strings"
	"testing"
)

// TestIssue4286TextPayloadFallback: tools without a dedicated presentation
// rule (read_file / grep / run_command / MCP tools) must still carry a text
// payload in the tunnel event, otherwise mobile clients have no expandable
// full text (#4286).
func TestIssue4286TextPayloadFallback(t *testing.T) {
	long := strings.Repeat("abcdefgh", 40) // 320 chars
	var summary, payload, mode string
	applyTextPayloadFallback(&summary, &payload, &mode, long)

	if payload != long {
		t.Errorf("payload must carry the full result, got %d chars", len(payload))
	}
	if mode != "text" {
		t.Errorf("payloadMode must be text, got %q", mode)
	}
	if len([]rune(summary)) > 124 { // 120 + "..."
		t.Errorf("summary must be compacted to ~120 chars, got %d", len([]rune(summary)))
	}
	if !strings.HasSuffix(summary, "...") {
		t.Errorf("truncated summary must end with ..., got %q", summary)
	}
}

// TestIssue4286PushToolResultFallbackFields: PushToolResult on a tool outside
// the presentation rules must fill Summary/Payload/PayloadMode (previously all
// empty when DescribeToolResult returned ok=false).
func TestIssue4286PushToolResultFallbackFields(t *testing.T) {
	b, d := newBrokerForTest()
	long := strings.Repeat("x", 200)
	b.PushToolResult("tid-1", "read_file", long, false)

	msgs := d.drain()
	var found ToolResultData
	hit := false
	for _, m := range msgs {
		if m.Type != EventToolResult {
			continue
		}
		var data ToolResultData
		if err := json.Unmarshal(m.Data, &data); err != nil {
			t.Fatalf("unmarshal tool result: %v", err)
		}
		if data.ToolID == "tid-1" {
			found = data
			hit = true
		}
	}
	if !hit {
		t.Fatal("tool result event not enqueued")
	}
	if found.Payload != long {
		t.Errorf("payload must carry full result, got %d chars", len(found.Payload))
	}
	if found.PayloadMode != "text" {
		t.Errorf("payloadMode must be text, got %q", found.PayloadMode)
	}
	if found.Summary == "" {
		t.Error("summary must not be empty")
	}
}

// TestIssue4286ShortResultNoEllipsis: short results keep a verbatim summary.
func TestIssue4286ShortResultNoEllipsis(t *testing.T) {
	got := tunnelCompactSingleLine("short\n  result")
	if got != "short result" {
		t.Errorf("short multiline result must collapse to single line, got %q", got)
	}
}
