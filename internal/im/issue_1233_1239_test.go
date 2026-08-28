package im

// Regression tests for GitHub issues #1233-#1239 (batch 5):
//
//	#1233: DisableAll/DisableBinding ownership guard (foreign-live claims)
//	#1234: DisableAll/EnableAll syncInstanceActiveChannels parity
//	#1236: slack bot_id self-filter for file_share loopback
//	#1237: slack SendInteractive 200-body ratelimited retry
//	#1239: signal TriggerTyping group recipient form

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"
)

// ---- #1233 / #1234: DisableAll / DisableBinding ownership + sync ----

func TestDisableAllSkipsForeignLiveAndMuted(t *testing.T) {
	m, store := newManager967(t, true) // live peer session-B
	ws := m.session.Workspace
	if err := store.Save(ChannelBinding{Workspace: ws, Adapter: "adp-ours", ChannelID: "c1", LastSessionID: "sess-A"}); err != nil {
		t.Fatalf("Save: %v", err)
	}
	if err := store.Save(ChannelBinding{Workspace: ws, Adapter: "adp-foreign", ChannelID: "c2", LastSessionID: "sess-B"}); err != nil {
		t.Fatalf("Save: %v", err)
	}
	if err := store.Save(ChannelBinding{Workspace: ws, Adapter: "adp-race", ChannelID: "c3", LastSessionID: "sess-B"}); err != nil {
		t.Fatalf("Save: %v", err)
	}
	m.currentBindings["adp-ours"] = &ChannelBinding{Workspace: ws, Adapter: "adp-ours", ChannelID: "c1", LastSessionID: "sess-A"}
	// Foreign-owned bindings are parked muted (#689/#693)...
	m.currentBindings["adp-foreign"] = &ChannelBinding{Workspace: ws, Adapter: "adp-foreign", ChannelID: "c2", LastSessionID: "sess-B", Muted: true}
	// ...but an unmuted foreign-live binding must also be skipped (race window).
	m.currentBindings["adp-race"] = &ChannelBinding{Workspace: ws, Adapter: "adp-race", ChannelID: "c3", LastSessionID: "sess-B"}

	n, err := m.DisableAll()
	if err != nil || n != 1 {
		t.Fatalf("DisableAll = %d, %v; want 1 (only our own binding), nil", n, err)
	}
	if _, ok := m.currentBindings["adp-ours"]; ok {
		t.Fatal("own binding must be disabled")
	}
	for _, adp := range []string{"adp-foreign", "adp-race"} {
		if _, ok := m.currentBindings[adp]; !ok {
			t.Fatalf("%s must stay in currentBindings (foreign-live skip)", adp)
		}
		if m.IsBindingDisabled(adp) {
			t.Fatalf("%s must not be disabled", adp)
		}
	}
	bindings, _ := store.ListByWorkspace(normalizeWorkspace(ws))
	for _, b := range bindings {
		if b.Adapter == "adp-foreign" || b.Adapter == "adp-race" {
			if b.LastSessionID != "sess-B" {
				t.Fatalf("DisableAll must not clear foreign owner's claim for %s; got %q", b.Adapter, b.LastSessionID)
			}
		}
	}
}

func TestDisableBindingRejectsForeignLiveOwner(t *testing.T) {
	m, store := newManager967(t, true)
	ws := m.session.Workspace
	if err := store.Save(ChannelBinding{Workspace: ws, Adapter: "adp-x", ChannelID: "c1", LastSessionID: "sess-B"}); err != nil {
		t.Fatalf("Save: %v", err)
	}
	m.currentBindings["adp-x"] = &ChannelBinding{Workspace: ws, Adapter: "adp-x", ChannelID: "c1", LastSessionID: "sess-B"}

	err := m.DisableBinding("adp-x")
	if err == nil || !strings.Contains(err.Error(), "another live session") {
		t.Fatalf("DisableBinding must reject foreign-live owner, got: %v", err)
	}
	if _, ok := m.currentBindings["adp-x"]; !ok {
		t.Fatal("rejected DisableBinding must leave the binding in currentBindings")
	}
	bindings, _ := store.ListByWorkspace(normalizeWorkspace(ws))
	for _, b := range bindings {
		if b.Adapter == "adp-x" && b.LastSessionID != "sess-B" {
			t.Fatalf("rejected DisableBinding must not clear the persisted claim; got %q", b.LastSessionID)
		}
	}
}

func TestDisableAllEnableAllSyncInstanceChannels(t *testing.T) {
	m, _ := newManager967(t, false)
	ws := m.session.Workspace
	m.currentBindings["adp-1"] = &ChannelBinding{Workspace: ws, Adapter: "adp-1", ChannelID: "c1", LastSessionID: "sess-A"}
	m.syncInstanceActiveChannels() // simulate the pre-existing true snapshot

	hasActive := func() bool {
		d := m.InstanceDetect()
		if d == nil {
			t.Fatal("instance detect is nil")
		}
		d.mu.Lock()
		defer d.mu.Unlock()
		return d.info.HasActiveChannels
	}
	if !hasActive() {
		t.Fatal("precondition: snapshot should be true with an active binding")
	}

	if n, err := m.DisableAll(); err != nil || n != 1 {
		t.Fatalf("DisableAll = %d, %v", n, err)
	}
	if hasActive() {
		t.Fatal("#1234: DisableAll must sync HasActiveChannels=false (stale true wrongly gates other instances' im_send)")
	}

	if n, err := m.EnableAll(); err != nil || n != 1 {
		t.Fatalf("EnableAll = %d, %v", n, err)
	}
	if !hasActive() {
		t.Fatal("#1234: EnableAll must sync HasActiveChannels=true")
	}
}

// ---- #1236: slack bot_id self-filter ----

type recordingBridge1236 struct {
	mu    sync.Mutex
	count int
	last  InboundMessage
}

func (r *recordingBridge1236) SubmitInboundMessage(_ context.Context, msg InboundMessage) error {
	r.mu.Lock()
	r.count++
	r.last = msg
	r.mu.Unlock()
	return nil
}

func newSlackSelfFilterAdapter1236(t *testing.T) (*slackAdapter, *recordingBridge1236) {
	t.Helper()
	bridge := &recordingBridge1236{}
	m := NewManager()
	m.bridge = bridge
	m.bindingStore = NewMemoryBindingStore()
	m.BindSession(SessionBinding{Workspace: "/tmp/ws-1236", SessionID: "sess-1"})
	m.currentBindings["slack"] = &ChannelBinding{Workspace: "/tmp/ws-1236", Adapter: "slack", ChannelID: "ch-1"}
	return &slackAdapter{
		name:      "slack",
		manager:   m,
		botUserID: "U-me",
		botID:     "B-me",
		seen:      map[string]time.Time{},
	}, bridge
}

func TestSlackSkipsOwnBotFileShareLoopback(t *testing.T) {
	a, bridge := newSlackSelfFilterAdapter1236(t)

	// Bot's own file_share: bot_id set, no user field. Before #1236 this
	// passed the (dead) user filter and the whitelisted subtype, feeding the
	// agent its own uploaded image as a fresh user turn.
	a.handleMessage(context.Background(), map[string]any{
		"channel": "ch-1",
		"ts":      "1700000000.000100",
		"subtype": "file_share",
		"bot_id":  "B-me",
		"text":    "here is your image",
	})
	if bridge.count != 0 {
		t.Fatalf("own bot file_share must be dropped, got %d inbound", bridge.count)
	}

	// A real user's message still flows through.
	a.handleMessage(context.Background(), map[string]any{
		"channel": "ch-1",
		"ts":      "1700000000.000200",
		"user":    "U-other",
		"text":    "hello agent",
	})
	if bridge.count != 1 || bridge.last.Text != "hello agent" {
		t.Fatalf("user message must reach the bridge once, got %d msgs, last=%+v", bridge.count, bridge.last)
	}

	// Another app's bot message (different bot_id) keeps current behavior:
	// not our loopback, still delivered.
	a.handleMessage(context.Background(), map[string]any{
		"channel": "ch-1",
		"ts":      "1700000000.000300",
		"bot_id":  "B-other",
		"text":    "from another bot",
	})
	if bridge.count != 2 {
		t.Fatalf("foreign bot message must not be filtered by our bot_id, got %d", bridge.count)
	}
}

// ---- #1237: SendInteractive 200-body ratelimited retry ----

func TestSlackSendInteractiveRetriesRateLimitedBody(t *testing.T) {
	var requests int
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		requests++
		w.Header().Set("Content-Type", "application/json")
		if requests == 1 {
			_, _ = io.WriteString(w, `{"ok":false,"error":"ratelimited"}`)
			return
		}
		_, _ = io.WriteString(w, `{"ok":true,"ts":"1700000000.000999"}`)
	}))
	defer srv.Close()

	a := &slackAdapter{
		name:       "slack",
		httpClient: srv.Client(),
		apiBase:    srv.URL,
		connected:  true,
		botToken:   "xoxb-test",
	}
	ts, err := a.SendInteractive(context.Background(), ChannelBinding{ChannelID: "ch-1"}, InteractiveMessage{
		ID:   "q1",
		Text: "approve?",
		Buttons: []InteractiveButton{
			{Label: "Yes", Value: "y"},
			{Label: "No", Value: "n"},
		},
	})
	if err != nil {
		t.Fatalf("SendInteractive must survive a 200-body ratelimited response: %v", err)
	}
	if ts != "1700000000.000999" {
		t.Fatalf("unexpected ts: %q", ts)
	}
	if requests != 2 {
		t.Fatalf("expected exactly 2 requests (1 rate-limited + 1 success), got %d", requests)
	}
}

// ---- #1239: signal TriggerTyping group recipient ----

func TestSignalTriggerTypingGroupRecipientForm(t *testing.T) {
	var body map[string]any
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		data, _ := io.ReadAll(r.Body)
		_ = json.Unmarshal(data, &body)
		w.WriteHeader(http.StatusAccepted)
	}))
	defer srv.Close()

	a := &signalAdapter{
		baseURL:   srv.URL,
		account:   "+15550001111",
		conn:      srv.Client(),
		connected: true,
	}
	if err := a.TriggerTyping(context.Background(), ChannelBinding{ChannelID: "group:abc123"}); err != nil {
		t.Fatalf("TriggerTyping: %v", err)
	}
	if body == nil {
		t.Fatal("no typing request captured")
	}
	if _, has := body["groupId"]; has {
		t.Fatal("#1239: groupId field does not exist in the typing API and must not be sent")
	}
	recipient, _ := body["recipient"].(string)
	// group IDs use the same "group." + double-encoded form as sendText.
	want := "group." + base64.StdEncoding.EncodeToString([]byte("abc123"))
	if recipient != want {
		t.Fatalf("recipient = %q, want %q", recipient, want)
	}
}
