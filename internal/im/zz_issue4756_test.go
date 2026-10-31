package im

import (
	"encoding/json"
	"testing"

	"maunium.net/go/mautrix/event"
)

// #4756-A: for an edited reply (m.replace whose new_content carries the
// m.in_reply_to relation), the strip gate must see the reply relation. The
// outer relates_to is the replace marker, so NewContent.GetReplyTo() is the
// only signal - pin both halves: mautrix exposes the inner reply-to, and the
// fallback stripper removes the serialized prefix from such a body.
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
	// Outer: replace marker only - no reply-to (the pre-fix gate read this).
	if c.GetReplyTo() != "" {
		t.Fatalf("outer reply-to should be empty for m.replace, got %q", c.GetReplyTo())
	}
	// Inner: the reply relation the fixed gate falls back to.
	if c.NewContent == nil {
		t.Fatal("NewContent missing")
	}
	if got := c.NewContent.GetReplyTo(); got != "$orig" {
		t.Fatalf("NewContent.GetReplyTo() = %q, want $orig", got)
	}
	// The stripper must remove the serialized fallback prefix from the
	// new_content body such an edit carries.
	stripped := stripMatrixReplyFallback(c.NewContent.Body)
	if stripped != "edited reply body" {
		t.Fatalf("strip = %q, want %q", stripped, "edited reply body")
	}
}
