package im

// Pins for the r187 inbound-routing seams on whatsappAdapter.handleInbound
// (whatsapp_adapter.go). Behavior-preserving extraction: handleInbound keeps
// the orchestration (IsFromMe filter → dedup mark → text extraction →
// bound-channel filter → pairing flow → async dispatch) and the phases move
// to helpers. Existing coverage (whatsapp_adapter_issue974_test.go,
// issue_1252_1257_test.go) is untouched and must keep passing unmodified.

import (
	"strconv"
	"testing"
	"time"

	"go.mau.fi/whatsmeow/proto/waE2E"
	"go.mau.fi/whatsmeow/types/events"
	"google.golang.org/protobuf/proto"
)

func TestWhatsAppInboundTextPins(t *testing.T) {
	cases := []struct {
		name string
		msg  *waE2E.Message
		want string
	}{
		{"conversation", &waE2E.Message{Conversation: proto.String("hello")}, "hello"},
		{"conversation trimmed", &waE2E.Message{Conversation: proto.String("  hello  ")}, "hello"},
		{"extended text", &waE2E.Message{ExtendedTextMessage: &waE2E.ExtendedTextMessage{Text: proto.String("ext")}}, "ext"},
		{"caption fallback", &waE2E.Message{ImageMessage: &waE2E.ImageMessage{Caption: proto.String("cap")}}, "cap"},
		{"conversation beats caption", &waE2E.Message{Conversation: proto.String("text"), ImageMessage: &waE2E.ImageMessage{Caption: proto.String("cap")}}, "text"},
		{"whitespace conversation falls to caption", &waE2E.Message{Conversation: proto.String("   "), ImageMessage: &waE2E.ImageMessage{Caption: proto.String("cap")}}, "cap"},
		{"empty", &waE2E.Message{}, ""},
	}
	for _, tc := range cases {
		ev := &events.Message{Message: tc.msg}
		if got := whatsappInboundText(ev); got != tc.want {
			t.Errorf("%s: whatsappInboundText = %q, want %q", tc.name, got, tc.want)
		}
	}
}

func TestWhatsAppRecordSeenPins(t *testing.T) {
	// Fresh vs redelivery.
	a := &whatsappAdapter{name: "t", seen: map[string]time.Time{}}
	if !a.recordSeen("m1") {
		t.Fatal("fresh id must be accepted")
	}
	if a.recordSeen("m1") {
		t.Fatal("redelivery of the same msgid must be rejected (#974)")
	}
	if len(a.seen) != 1 {
		t.Fatalf("redelivery must not grow the map: seen=%v", a.seen)
	}

	// TTL prune on overflow: stale entries are evicted, new one survives.
	a2 := &whatsappAdapter{name: "t", seen: map[string]time.Time{}}
	stale := time.Now().Add(-6 * time.Minute)
	for i := 0; i < waDedupMaxSize; i++ {
		a2.seen[strconv.Itoa(i)] = stale
	}
	if !a2.recordSeen("new") {
		t.Fatal("overflow with stale entries must still accept")
	}
	if len(a2.seen) != 1 {
		t.Fatalf("stale entries must be pruned on overflow: len=%d", len(a2.seen))
	}
	if _, ok := a2.seen["new"]; !ok {
		t.Fatal("newest entry must survive TTL prune")
	}

	// #1567-D burst fallback: fresh entries beyond the cap drop the oldest.
	a3 := &whatsappAdapter{name: "t", seen: map[string]time.Time{}}
	for i := 0; i < waDedupMaxSize; i++ {
		a3.seen["k"+strconv.Itoa(i)] = time.Now()
	}
	if !a3.recordSeen("k-new") {
		t.Fatal("burst overflow must still accept")
	}
	if len(a3.seen) != waDedupMaxSize-1 {
		t.Fatalf("burst overflow must drop below cap: len=%d, want %d", len(a3.seen), waDedupMaxSize-1)
	}
	if _, ok := a3.seen["k-new"]; !ok {
		t.Fatal("newest entry must survive the burst fallback")
	}
	if _, ok := a3.seen["k0"]; ok {
		t.Fatal("oldest entry must be dropped first (#1567-D)")
	}
	if _, ok := a3.seen["k1"]; ok {
		t.Fatal("second-oldest entry must also drop (loop runs while len >= cap)")
	}
}

func TestWhatsAppDropUnboundChannelPins(t *testing.T) {
	// No manager: nothing to filter.
	a := &whatsappAdapter{name: "wa"}
	if a.dropUnboundChannel("any-chat") {
		t.Fatal("no manager must not filter")
	}

	m := NewManager()
	a2 := &whatsappAdapter{name: "wa", manager: m}
	if a2.dropUnboundChannel("chat-b") {
		t.Fatal("no binding must not filter")
	}

	m.currentBindings["wa"] = &ChannelBinding{Adapter: "wa", ChannelID: "chat-a"}
	if !a2.dropUnboundChannel("chat-b") {
		t.Fatal("unbound channel must be dropped post-pairing")
	}
	if a2.dropUnboundChannel("chat-a") {
		t.Fatal("bound channel must pass")
	}

	// Empty bound ChannelID: filter inactive.
	m.currentBindings["wa"] = &ChannelBinding{Adapter: "wa"}
	if a2.dropUnboundChannel("chat-b") {
		t.Fatal("empty bound ChannelID must not filter")
	}
}

func TestWhatsAppPairingAndSubmitNilManagerPins(t *testing.T) {
	a := &whatsappAdapter{name: "t"}
	if a.handlePairingFlow("chat", InboundMessage{}) {
		t.Fatal("nil manager must not consume")
	}
	a.submitInbound(InboundMessage{}) // must be a no-op, no panic
}

func TestWhatsAppSubmitInboundDelivers(t *testing.T) {
	bridge := &errBridge540{}
	m := NewManager()
	m.bridge = bridge
	m.BindSession(SessionBinding{Workspace: "ws", SessionID: "s1"})
	m.currentBindings["wa"] = &ChannelBinding{Workspace: "ws", Adapter: "wa", ChannelID: "C1"}
	a := &whatsappAdapter{name: "wa", manager: m}

	a.submitInbound(InboundMessage{
		Text:     "hi",
		Envelope: Envelope{Platform: PlatformWhatsApp, Adapter: "wa", ChannelID: "C1", SenderID: "s"},
	})

	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		bridge.mu.Lock()
		calls := bridge.calls
		bridge.mu.Unlock()
		if calls == 1 {
			return
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatal("submitInbound must deliver the message through the manager bridge")
}
