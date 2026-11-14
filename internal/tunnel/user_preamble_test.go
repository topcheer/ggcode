package tunnel

import (
	"encoding/json"
	"testing"
	"time"
)

const (
	preambleFamilyA = "New user guidance arrived while you were working. Treat it as higher-priority context, adjust your plan immediately if needed, and then continue."
	preambleFamilyB = "The user RETRACTED the request. The run stops here - do not start any new work toward the original goal."
)

func TestStripUserPreambleFamilyA(t *testing.T) {
	raw := preambleFamilyA + "\n\nfix the flaky test in pkg/auth"
	got := StripUserPreamble(raw)
	if got != "fix the flaky test in pkg/auth" {
		t.Fatalf("family A strip = %q, want payload only", got)
	}
}

func TestStripUserPreambleFamilyB(t *testing.T) {
	raw := preambleFamilyB + "\n\nplease revert the config change"
	got := StripUserPreamble(raw)
	if got != "please revert the config change" {
		t.Fatalf("family B strip = %q, want payload only", got)
	}
}

func TestStripUserPreamblePreambleOnly(t *testing.T) {
	if got := StripUserPreamble(preambleFamilyA); got != "" {
		t.Fatalf("family A preamble-only = %q, want empty", got)
	}
	if got := StripUserPreamble(preambleFamilyB); got != "" {
		t.Fatalf("family B preamble-only = %q, want empty", got)
	}
}

func TestStripUserPreambleNormalMessageUntouched(t *testing.T) {
	cases := []string{
		"hello, please review the diff",
		"",
		// Marker appears mid-text (not at start): startsWith must not fire.
		"user asks: does \"New user guidance arrived while you were working\" strip?\n\nyes it does",
		"the agent said: The user RETRACTED the request earlier",
	}
	for _, raw := range cases {
		if got := StripUserPreamble(raw); got != raw {
			t.Fatalf("normal message mutated: got %q, want %q", got, raw)
		}
	}
}

func TestStripUserMessageEvent(t *testing.T) {
	// Stripped + re-encoded.
	data, _ := json.Marshal(MessageData{Text: preambleFamilyA + "\n\nreal payload", Kind: "user"})
	msg := StripUserMessageEvent(GatewayMessage{Type: EventUserMessage, EventID: "ev-1", Data: data})
	var got MessageData
	if err := json.Unmarshal(msg.Data, &got); err != nil {
		t.Fatal(err)
	}
	if got.Text != "real payload" {
		t.Fatalf("event text = %q, want %q", got.Text, "real payload")
	}

	// Non-user events pass through untouched.
	if out := StripUserMessageEvent(GatewayMessage{Type: EventSystemMessage, Data: data}); string(out.Data) != string(data) {
		t.Fatal("system_message event must not be touched")
	}

	// Clean user events are returned without re-encoding.
	clean, _ := json.Marshal(MessageData{Text: "plain question"})
	in := GatewayMessage{Type: EventUserMessage, Data: clean}
	out := StripUserMessageEvent(in)
	if string(out.Data) != string(in.Data) {
		t.Fatal("clean user_message data must stay byte-identical (no re-encode)")
	}

	// Malformed payloads pass through untouched.
	bad := GatewayMessage{Type: EventUserMessage, Data: []byte("{not json")}
	if out := StripUserMessageEvent(bad); string(out.Data) != "{not json" {
		t.Fatal("malformed payload must pass through untouched")
	}
}

func TestBrokerPushUserMessageStripsPreamble(t *testing.T) {
	b, d := newBrokerForTest()
	defer b.Stop()

	b.PushUserMessage(preambleFamilyB + "\n\nstop and clean up")
	time.Sleep(50 * time.Millisecond)
	msgs := d.drain()

	for _, m := range msgs {
		if m.Type != EventUserMessage {
			continue
		}
		var data MessageData
		if err := json.Unmarshal(m.Data, &data); err != nil {
			t.Fatal(err)
		}
		if data.Text != "stop and clean up" {
			t.Fatalf("live user_message text = %q, want stripped payload", data.Text)
		}
		return
	}
	t.Fatal("expected user_message event")
}

func TestBrokerSeedHistoryStripsPreamble(t *testing.T) {
	b, d := newBrokerForTest()
	defer b.Stop()

	b.seedHistoryDirect([]HistoryEntry{
		{Role: "user", Content: preambleFamilyA + "\n\nreal first message"},
		{Role: "user", Content: preambleFamilyB}, // preamble-only: must be skipped
		{Role: "user", Content: "plain second message"},
		{Role: "assistant", Content: "answer"},
	})
	time.Sleep(50 * time.Millisecond)
	msgs := d.drain()

	var texts []string
	for _, m := range msgs {
		if m.Type != EventUserMessage {
			continue
		}
		var data MessageData
		if err := json.Unmarshal(m.Data, &data); err != nil {
			t.Fatal(err)
		}
		texts = append(texts, data.Text)
	}
	if len(texts) != 2 {
		t.Fatalf("expected 2 user_message events after strip+skip, got %d: %v", len(texts), texts)
	}
	if texts[0] != "real first message" || texts[1] != "plain second message" {
		t.Fatalf("seeded texts = %v, want stripped payloads in order", texts)
	}
}
