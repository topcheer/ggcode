package tunnel

import (
	"encoding/json"
	"strings"
	"testing"
)

func TestIssue4286TextPayloadFallback(t *testing.T) {
	long := strings.Repeat("abcdefgh", 40)
	var summary, payload, mode string
	applyTextPayloadFallback(&summary, &payload, &mode, long)

	if payload != long {
		t.Errorf("payload must carry the full result, got %d chars", len(payload))
	}
	if mode != "text" {
		t.Errorf("payloadMode must be text, got %q", mode)
	}
	if len([]rune(summary)) > 124 {
		t.Errorf("summary must be compacted to ~120 chars, got %d", len([]rune(summary)))
	}
	if !strings.HasSuffix(summary, "...") {
		t.Errorf("truncated summary must end with ..., got %q", summary)
	}
}

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

func TestIssue4286ShortResultNoEllipsis(t *testing.T) {
	got := tunnelCompactSingleLine("short\n  result")
	if got != "short result" {
		t.Errorf("short multiline result must collapse to single line, got %q", got)
	}
}
