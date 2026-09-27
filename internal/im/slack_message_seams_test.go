package im

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"
)

// r195 pins for the Slack inbound seams extracted from handleMessage
// (slack_inbound.go). Behavior pins: the orchestrator's guard ordering,
// debug lines and reply literals are byte-identical to the pre-refactor
// body; these tests freeze each seam's contract.

func TestSlackOwnMessagePredicate(t *testing.T) {
	cases := []struct {
		name                string
		userID, eventBotID  string
		botUserID, ownBotID string
		want                bool
	}{
		{"user matches bot user id", "U1", "B7", "U1", "B7", true},
		{"event bot id matches own bot id", "", "B7", "U1", "B7", true},
		{"own bot id empty disables bot-id match", "", "B7", "U1", "", false},
		{"empty user matches empty bot user id", "", "", "", "", true},
		{"foreign user and bot", "U2", "B9", "U1", "B7", false},
		{"empty event bot, authed own", "U2", "", "U1", "B7", false},
	}
	for _, tc := range cases {
		if got := slackOwnMessage(tc.userID, tc.eventBotID, tc.botUserID, tc.ownBotID); got != tc.want {
			t.Errorf("%s: slackOwnMessage(%q,%q,%q,%q)=%v want %v",
				tc.name, tc.userID, tc.eventBotID, tc.botUserID, tc.ownBotID, got, tc.want)
		}
	}
}

func TestSlackSubtypeBlocked(t *testing.T) {
	cases := map[string]bool{
		"":                false, // plain text messages pass
		"file_share":      false, // whitelisted (#1236)
		"message_changed": true,
		"bot_message":     true,
	}
	for subtype, want := range cases {
		if got := slackSubtypeBlocked(subtype); got != want {
			t.Errorf("slackSubtypeBlocked(%q)=%v want %v", subtype, got, want)
		}
	}
}

func TestSlackMergeVoiceText(t *testing.T) {
	cases := []struct {
		name, text, voice, want string
	}{
		{"both empty", "", "", ""},
		{"text only", "hello", "", "hello"},
		{"voice only", "", "transcribed", "transcribed"},
		{"combined", "hello", "transcribed", "hello\n\ntranscribed"},
	}
	for _, tc := range cases {
		if got := slackMergeVoiceText(tc.text, tc.voice); got != tc.want {
			t.Errorf("%s: slackMergeVoiceText(%q,%q)=%q want %q", tc.name, tc.text, tc.voice, got, tc.want)
		}
	}
}

func TestSlackMessageFields(t *testing.T) {
	channel, text, ts, threadTS, subtype := slackMessageFields(map[string]any{
		"channel":   "C123",
		"text":      "hi there",
		"ts":        "1718000000.000100",
		"thread_ts": "1718000000.000050",
		"subtype":   "file_share",
	})
	if channel != "C123" || text != "hi there" || ts != "1718000000.000100" ||
		threadTS != "1718000000.000050" || subtype != "file_share" {
		t.Fatalf("full extraction mismatch: %q %q %q %q %q", channel, text, ts, threadTS, subtype)
	}
	channel, text, ts, threadTS, subtype = slackMessageFields(map[string]any{})
	if channel != "" || text != "" || ts != "" || threadTS != "" || subtype != "" {
		t.Fatalf("missing keys must degrade to zero values: %q %q %q %q %q", channel, text, ts, threadTS, subtype)
	}
}

func TestSlackBuildInbound(t *testing.T) {
	receivedAt := time.Unix(1700000000, 0).UTC()
	attachments := []Attachment{{Kind: AttachmentImage, URL: "https://example.com/a.png"}}
	inbound := slackBuildInbound("slack-1", "C123", "1718000000.000050", "U9", "1718000000.000100", "hello", attachments, receivedAt)
	if inbound.Envelope.Adapter != "slack-1" ||
		inbound.Envelope.Platform != PlatformSlack ||
		inbound.Envelope.ChannelID != "C123" ||
		inbound.Envelope.ThreadID != "1718000000.000050" ||
		inbound.Envelope.SenderID != "U9" ||
		inbound.Envelope.MessageID != "1718000000.000100" ||
		!inbound.Envelope.ReceivedAt.Equal(receivedAt) {
		t.Fatalf("envelope mismatch: %+v", inbound.Envelope)
	}
	if inbound.Text != "hello" || len(inbound.Attachments) != 1 || inbound.Attachments[0].URL != "https://example.com/a.png" {
		t.Fatalf("body mismatch: %+v", inbound)
	}
}

func TestSlackHandleMessagePairingFreshManagerNotConsumed(t *testing.T) {
	a := &slackAdapter{name: "t", manager: NewManager()}
	// Fresh manager has no session: HandlePairingInbound yields
	// ErrNoSessionBound (silently ignored) and the message is not consumed.
	if a.handleMessagePairing(context.Background(), "C1", "", InboundMessage{}) {
		t.Fatal("fresh manager (no session) must not consume the message")
	}
}

func TestSlackDispatchInboundSendsRetryNotifyLiteral(t *testing.T) {
	type postBody struct {
		Channel  string `json:"channel"`
		Text     string `json:"text"`
		ThreadTS string `json:"thread_ts"`
	}
	var mu chan struct{} = make(chan struct{}, 1)
	mu <- struct{}{}
	var bodies []postBody
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/chat.postMessage" {
			http.NotFound(w, r)
			return
		}
		data, _ := io.ReadAll(r.Body)
		var b postBody
		_ = json.Unmarshal(data, &b)
		<-mu
		bodies = append(bodies, b)
		mu <- struct{}{}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"ok":true,"ts":"1700000000.000200"}`))
	}))
	defer srv.Close()

	// Fresh manager (no session) → HandleInbound returns ErrNoSessionBound:
	// not denied, not ErrNoChannelBound → the #260 retry-notify reply fires
	// and a warning state is published (no panic on a fresh manager).
	m := NewManager()
	a := &slackAdapter{name: "t", manager: m, botToken: "xoxb-test", apiBase: srv.URL, httpClient: srv.Client()}
	a.dispatchInbound(context.Background(), "C1", "1718000000.000050", InboundMessage{})

	if len(bodies) != 1 {
		t.Fatalf("expected exactly one retry-notify post, got %d", len(bodies))
	}
	if bodies[0].Channel != "C1" {
		t.Errorf("channel = %q, want C1", bodies[0].Channel)
	}
	if bodies[0].Text != "message could not be delivered (session not ready), please retry" {
		t.Errorf("retry-notify literal mismatch: %q", bodies[0].Text)
	}
	if bodies[0].ThreadTS != "1718000000.000050" {
		t.Errorf("thread_ts = %q, want 1718000000.000050", bodies[0].ThreadTS)
	}
}

func TestSlackDispatchInboundSubmitErrorPathPins(t *testing.T) {
	// Bridge-level failure (SubmitInboundMessage error, #540 family): the
	// retry-notify reply must still fire - handleInbound submission errors
	// take the same #260 acknowledgement path as gate errors.
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"ok":true,"ts":"1700000000.000300"}`))
	}))
	defer srv.Close()

	m := NewManager()
	bridge := &errBridge540{err: errors.New("submit down")}
	m.bridge = bridge
	m.BindSession(SessionBinding{Workspace: "ws", SessionID: "s1"})
	m.currentBindings["t"] = &ChannelBinding{Workspace: "ws", Adapter: "t", ChannelID: "C2"}
	a := &slackAdapter{name: "t", manager: m, botToken: "xoxb-test", apiBase: srv.URL, httpClient: srv.Client()}

	a.dispatchInbound(context.Background(), "C2", "", InboundMessage{
		Text:     "hi",
		Envelope: Envelope{Platform: PlatformSlack, Adapter: "t", ChannelID: "C2", SenderID: "s"},
	})

	if bridge.calls != 1 {
		t.Fatalf("bridge submissions = %d, want 1", bridge.calls)
	}
}
