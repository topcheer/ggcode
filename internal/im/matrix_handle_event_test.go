package im

import (
	"context"
	"testing"
	"time"

	"maunium.net/go/mautrix/event"
)

// Pin tests for the r178 handleEvent seams (matrix_adapter.go). Each test
// pins the exact gate semantics that lived inline in handleEvent before the
// orchestrator flattening; behavior must stay byte-for-byte equivalent.

func TestMatrixDecryptEventPassthrough(t *testing.T) {
	a := &matrixAdapter{}

	// Non-encrypted events pass through untouched.
	plain := &event.Event{Type: event.EventMessage, RoomID: "!r:x"}
	got, ok := a.decryptEvent(context.Background(), plain)
	if !ok || got != plain {
		t.Fatalf("plain event must pass through unchanged: ok=%v same=%v", ok, got == plain)
	}

	// Encrypted events without a crypto machine are dropped.
	enc := &event.Event{Type: event.EventEncrypted, RoomID: "!r:x"}
	got, ok = a.decryptEvent(context.Background(), enc)
	if ok || got != nil {
		t.Fatalf("encrypted event with no mach must be dropped: ok=%v got=%v", ok, got)
	}
}

func TestMatrixMarkEventSeen(t *testing.T) {
	// First sighting is new, immediate repeat is a duplicate.
	a := &matrixAdapter{seen: map[string]time.Time{}}
	if !a.markEventSeen("$e1") {
		t.Fatal("first sighting must be reported new")
	}
	if a.markEventSeen("$e1") {
		t.Fatal("immediate repeat must be reported duplicate")
	}

	// Duplicate must not refresh the recorded timestamp (original behavior:
	// seen[id] is only written on the accepted path).
	stamp := time.Now().Add(-1 * time.Minute)
	b := &matrixAdapter{seen: map[string]time.Time{"$e2": stamp}}
	if b.markEventSeen("$e2") {
		t.Fatal("recent repeat must be reported duplicate")
	}
	if !b.seen["$e2"].Equal(stamp) {
		t.Fatalf("duplicate must not refresh timestamp: got %v want %v", b.seen["$e2"], stamp)
	}

	// Re-sighting outside the 5-minute window is accepted again.
	c := &matrixAdapter{seen: map[string]time.Time{"$e3": time.Now().Add(-6 * time.Minute)}}
	if !c.markEventSeen("$e3") {
		t.Fatal("sighting older than the 5-minute window must be accepted")
	}

	// GC evicts entries older than 10 minutes.
	d := &matrixAdapter{seen: map[string]time.Time{"$old": time.Now().Add(-11 * time.Minute)}}
	if !d.markEventSeen("$new") {
		t.Fatal("new event must be accepted")
	}
	if _, exists := d.seen["$old"]; exists {
		t.Fatal("entry older than 10 minutes must be evicted")
	}
	if _, exists := d.seen["$new"]; !exists {
		t.Fatal("accepted event must be recorded")
	}
}

func TestMatrixRawMessageContent(t *testing.T) {
	a := &matrixAdapter{name: "mx"}

	// nil raw yields an empty m.text-compatible content (json "null" path).
	content, ok := a.rawMessageContent(nil)
	if !ok || content == nil || string(content.MsgType) != "" || content.Body != "" {
		t.Fatalf("nil raw must yield empty content: ok=%v content=%+v", ok, content)
	}

	// Non-text raw msgtype is rejected.
	content, ok = a.rawMessageContent(map[string]any{"msgtype": "m.image", "body": "pic"})
	if ok || content != nil {
		t.Fatalf("m.image raw must be rejected: ok=%v content=%+v", ok, content)
	}

	// m.text body is extracted.
	content, ok = a.rawMessageContent(map[string]any{"msgtype": "m.text", "body": "hello"})
	if !ok || content == nil || string(content.MsgType) != "m.text" || content.Body != "hello" {
		t.Fatalf("m.text raw must be extracted: ok=%v content=%+v", ok, content)
	}

	// Unmarshal failure (marshal of a func value fails -> nil bytes) drops.
	content, ok = a.rawMessageContent(map[string]any{"bad": func() {}})
	if ok || content != nil {
		t.Fatalf("marshal-failed raw must be rejected: ok=%v content=%+v", ok, content)
	}
}

func TestMatrixTextBody(t *testing.T) {
	a := &matrixAdapter{name: "mx"}

	// m.text and empty msgtype pass; body returned as-is.
	for _, mt := range []event.MessageType{event.MsgText, ""} {
		body, ok := a.textBody(&event.MessageEventContent{MsgType: mt, Body: "hi"})
		if !ok || body != "hi" {
			t.Fatalf("msgtype %q must pass: ok=%v body=%q", mt, ok, body)
		}
	}

	// Non-text msgtype is rejected.
	if _, ok := a.textBody(&event.MessageEventContent{MsgType: event.MsgImage, Body: "pic"}); ok {
		t.Fatal("m.image must be rejected")
	}

	// Reply fallback is stripped only when a reply relation exists.
	reply := &event.MessageEventContent{
		MsgType: event.MsgText,
		Body:    "> <@u:x> quoted\n\nreal text",
		RelatesTo: &event.RelatesTo{
			InReplyTo: &event.InReplyTo{EventID: "$src"},
		},
	}
	body, ok := a.textBody(reply)
	if !ok || body != "real text" {
		t.Fatalf("reply fallback must be stripped: ok=%v body=%q", ok, body)
	}

	plain := &event.MessageEventContent{MsgType: event.MsgText, Body: "> just a quote"}
	body, ok = a.textBody(plain)
	if !ok || body != "> just a quote" {
		t.Fatalf("non-reply body must pass untouched: ok=%v body=%q", ok, body)
	}
}

func TestMatrixSenderAllowed(t *testing.T) {
	// Empty allowlist admits everyone (De Morgan pin of the original gate).
	open := &matrixAdapter{}
	if !open.senderAllowed("@anyone:x") {
		t.Fatal("empty allowlist must admit every sender")
	}

	gated := &matrixAdapter{allowedUsers: []string{"@a:x", "@b:x"}}
	if !gated.senderAllowed("@a:x") {
		t.Fatal("allowlisted sender must pass")
	}
	if gated.senderAllowed("@c:x") {
		t.Fatal("non-allowlisted sender must be dropped")
	}
}

func TestMatrixResolveDM(t *testing.T) {
	// Cache hit needs no API (nil client would fail one).
	a := &matrixAdapter{dmRooms: map[string]bool{"!dm:x": true}}
	if !a.resolveDM(context.Background(), "!dm:x") {
		t.Fatal("cached DM room must resolve to DM")
	}

	// Cache miss with nil client falls through to non-DM.
	if a.resolveDM(context.Background(), "!other:x") {
		t.Fatal("cache miss with nil client must resolve to non-DM")
	}
}

func TestMatrixMentionGate(t *testing.T) {
	const room = "!room:x"

	// DM rooms bypass gating entirely.
	dm := &matrixAdapter{userID: "@bot:x", requireMention: true}
	body, ok := dm.mentionGate(room, "hello", nil, true)
	if !ok || body != "hello" {
		t.Fatalf("DM room must bypass mention gating: ok=%v body=%q", ok, body)
	}

	// Free rooms bypass gating.
	free := &matrixAdapter{userID: "@bot:x", requireMention: true, freeRooms: []string{room}}
	body, ok = free.mentionGate(room, "hello", nil, false)
	if !ok || body != "hello" {
		t.Fatalf("free room must bypass mention gating: ok=%v body=%q", ok, body)
	}

	// requireMention=false admits without a mention.
	noReq := &matrixAdapter{userID: "@bot:x"}
	body, ok = noReq.mentionGate(room, "hello", nil, false)
	if !ok || body != "hello" {
		t.Fatalf("requireMention=false must admit: ok=%v body=%q", ok, body)
	}

	// requireMention=true without a mention drops the event.
	strict := &matrixAdapter{userID: "@bot:example.org", requireMention: true}
	if _, ok := strict.mentionGate(room, "hello", nil, false); ok {
		t.Fatal("missing mention in a gated room must drop the event")
	}

	// A mention admits and is stripped from the body.
	body, ok = strict.mentionGate(room, "hey @bot:example.org", nil, false)
	if !ok {
		t.Fatal("mention must admit the event")
	}
	if body != "hey" {
		t.Fatalf("mention must be stripped: got %q", body)
	}
}

func TestMatrixBuildInboundMessage(t *testing.T) {
	a := &matrixAdapter{name: "mx"}
	evt := &event.Event{RoomID: "!r:x", Sender: "@u:x", ID: "$e1"}

	msg := a.buildInboundMessage(context.Background(), evt, "Display", "  hi  ")

	env := msg.Envelope
	switch {
	case env.Adapter != "mx":
		t.Errorf("Adapter = %q", env.Adapter)
	case env.Platform != PlatformMatrix:
		t.Errorf("Platform = %v", env.Platform)
	case env.ChannelID != "!r:x":
		t.Errorf("ChannelID = %q", env.ChannelID)
	case env.SenderID != "@u:x":
		t.Errorf("SenderID = %q", env.SenderID)
	case env.SenderName != "Display":
		t.Errorf("SenderName = %q", env.SenderName)
	case env.MessageID != "$e1":
		t.Errorf("MessageID = %q", env.MessageID)
	case env.ReceivedAt.IsZero():
		t.Error("ReceivedAt must be set")
	case msg.Text != "hi":
		t.Errorf("Text must be TrimSpace'd: got %q", msg.Text)
	}
}

func TestMatrixPairingAndDeliverNilManager(t *testing.T) {
	a := &matrixAdapter{name: "mx"}

	// No manager: pairing is not consumed and delivery is a no-op.
	if a.handlePairing(context.Background(), InboundMessage{}, "!r:x") {
		t.Fatal("nil manager must not consume pairing")
	}
	a.deliverInbound(context.Background(), InboundMessage{}) // must not panic
}
