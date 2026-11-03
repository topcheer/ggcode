package im

import (
	"encoding/json"
	"testing"

	"maunium.net/go/mautrix/event"
)

func TestIssue4756_EditOfReplyStripGateSeesInnerReplyTo(t *testing.T) {
	raw := `{
		"msgtype": "m.text",
		"body": "> <@alice:example.org> original\n\nedited reply body",
		"m.new_content": {
			"msgtype": "m.text",
			"body": "> <@alice:example.org> original\n\nedited reply body",
			"m.relates_to": {
				"m.in_reply_to": {"event_id": "$orig"}
			}
		},
		"m.relates_to": {
			"rel_type": "m.replace",
			"event_id": "$first"
		}
	}`
	var c event.MessageEventContent
	if err := json.Unmarshal([]byte(raw), &c); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}

	if c.GetReplyTo() != "" {
		t.Fatalf("outer reply-to should be empty for m.replace, got %q", c.GetReplyTo())
	}

	if c.NewContent == nil {
		t.Fatal("NewContent missing")
	}
	if got := c.NewContent.GetReplyTo(); got != "$orig" {
		t.Fatalf("NewContent.GetReplyTo() = %q, want $orig", got)
	}

	stripped := stripMatrixReplyFallback(c.NewContent.Body)
	if stripped != "edited reply body" {
		t.Fatalf("strip = %q, want %q", stripped, "edited reply body")
	}
}
