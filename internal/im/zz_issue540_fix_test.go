package im

// Regression tests for GitHub issue #540 (IM adapter bug batch).
// Probe scenarios converted to persistent tests:
//   A: HandleInbound dedup mark must roll back on failure paths
//   B: Slack app_mention text must strip the bot's own <@BOTID> token
//   C: DingTalk reconnect attempt counter must reset on clean disconnect
//   D: Feishu startup token fetch must retry with backoff
//   E: TG mention stripping must not lack word boundaries (@dev vs @devops)

import (
	"context"
	"errors"
	"io"
	"net/http"
	"strings"
	"sync"
	"testing"
	"time"
)

// ---- Bug A: dedup rollback on failure ----

type errBridge540 struct {
	mu    sync.Mutex
	calls int
	err   error
}

func (b *errBridge540) SubmitInboundMessage(_ context.Context, _ InboundMessage) error {
	b.mu.Lock()
	defer b.mu.Unlock()
	b.calls++
	return b.err
}

func newManagerWithBinding540(bridge Bridge, store BindingStore) *Manager {
	m := NewManager()
	m.bridge = bridge
	m.bindingStore = store
	m.BindSession(SessionBinding{Workspace: "ws", SessionID: "sess-1"})
	m.currentBindings["slack"] = &ChannelBinding{
		Workspace: "ws",
		Adapter:   "slack",
		ChannelID: "C1",
	}
	return m
}

func TestHandleInboundDedupRollbackOnSubmitFailure(t *testing.T) {
	bridge := &errBridge540{err: errors.New("agent busy")}
	m := newManagerWithBinding540(bridge, NewMemoryBindingStore())
	msg := InboundMessage{
		Envelope: Envelope{Adapter: "slack", ChannelID: "C1", MessageID: "m1"},
		Text:     "hello",
	}
	// First delivery: submit fails, mark must be rolled back.
	if err := m.HandleInbound(context.Background(), msg); err == nil {
		t.Fatal("expected error from failing bridge")
	}
	// Second delivery of the same MessageID (SDK retry) must NOT be deduped away.
	if err := m.HandleInbound(context.Background(), msg); err == nil {
		t.Fatal("expected error from failing bridge on retry")
	}
	bridge.mu.Lock()
	calls := bridge.calls
	bridge.mu.Unlock()
	if calls != 2 {
		t.Fatalf("retry after submit failure was swallowed by dedup: bridge called %d times, want 2", calls)
	}
	// Once submit succeeds, redelivery must be deduplicated.
	bridge.mu.Lock()
	bridge.err = nil
	bridge.mu.Unlock()
	if err := m.HandleInbound(context.Background(), msg); err != nil {
		t.Fatalf("third delivery should succeed: %v", err)
	}
	if err := m.HandleInbound(context.Background(), msg); err != nil {
		t.Fatalf("fourth delivery should succeed: %v", err)
	}
	bridge.mu.Lock()
	calls = bridge.calls
	bridge.mu.Unlock()
	if calls != 3 {
		t.Fatalf("successful message was not deduped: bridge called %d times, want 3", calls)
	}
}

func TestHandleInboundDedupRollbackOnEarlyFailures(t *testing.T) {
	cases := []struct {
		name    string
		setup   func(*Manager)
		wantErr error
	}{
		{"no session", func(m *Manager) { m.session = nil }, ErrNoSessionBound},
		{"no binding", func(m *Manager) { delete(m.currentBindings, "slack") }, ErrNoChannelBound},
		{"no bridge", func(m *Manager) { m.bridge = nil }, ErrNoBridge},
		{"channel denied", func(m *Manager) {
			m.currentBindings["slack"] = &ChannelBinding{Workspace: "ws", Adapter: "slack", ChannelID: "OTHER"}
		}, ErrInboundChannelDenied},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			m := newManagerWithBinding540(&errBridge540{}, nil)
			tc.setup(m)
			msg := InboundMessage{Envelope: Envelope{Adapter: "slack", ChannelID: "C1", MessageID: "m1"}}
			if err := m.HandleInbound(context.Background(), msg); !errors.Is(err, tc.wantErr) {
				t.Fatalf("got %v, want %v", err, tc.wantErr)
			}
			m.mu.RLock()
			_, marked := m.seenMessages["slack:m1"]
			m.mu.RUnlock()
			if marked {
				t.Fatalf("dedup mark leaked on failure path %q: message will be lost on redelivery", tc.name)
			}
		})
	}
}

// ---- Bug B: Slack bot mention stripping ----

func TestSlackStripBotMention(t *testing.T) {
	a := &slackAdapter{}
	a.mu.Lock()
	a.botUserID = "U123BOT"
	a.mu.Unlock()
	cases := []struct {
		in, want string
	}{
		{"<@U123BOT> /status", "/status"},
		{"<@U123BOT> y", "y"},
		{"<@U123BOT>    hello world", "hello world"},
		{"<@U123BOT|ggcode> run tests", "run tests"},
		{"<@U123BOT><@U123BOT> /deploy", "/deploy"},                  // duplicated token
		{"hey <@U999USER> check this", "hey <@U999USER> check this"}, // other mentions kept
		{"plain text", "plain text"},
		{"", ""},
	}
	for _, tc := range cases {
		got := strings.TrimSpace(a.stripBotMention(tc.in))
		if got != tc.want {
			t.Errorf("stripBotMention(%q) = %q, want %q", tc.in, got, tc.want)
		}
	}
}

func TestSlackStripBotMentionEmptyBotID(t *testing.T) {
	a := &slackAdapter{}
	if got := a.stripBotMention("<@U123> hi"); got != "<@U123> hi" {
		t.Fatalf("expected no stripping without botUserID, got %q", got)
	}
}

// ---- Bug C: DingTalk backoff reset ----

func TestBackoffForResetsOnCleanDisconnect(t *testing.T) {
	backoffs := []time.Duration{3 * time.Second, 5 * time.Second, 10 * time.Second, 30 * time.Second}
	delay, attempt := backoffFor(nil, 3, backoffs)
	if delay != backoffs[0] || attempt != 1 {
		t.Fatalf("clean disconnect should reset backoff: got delay=%v attempt=%d, want %v/1", delay, attempt, backoffs[0])
	}
	delay, attempt = backoffFor(errors.New("conn reset"), 2, backoffs)
	if delay != backoffs[2] || attempt != 3 {
		t.Fatalf("error should advance backoff: got delay=%v attempt=%d, want %v/3", delay, attempt, backoffs[2])
	}
	// Capping: attempt beyond the slice stays at the last entry.
	delay, attempt = backoffFor(errors.New("x"), 9, backoffs)
	if delay != backoffs[len(backoffs)-1] || attempt != 10 {
		t.Fatalf("backoff should cap: got delay=%v attempt=%d", delay, attempt)
	}
}

// ---- Bug D: Feishu startup token retry ----

type fakeFeishuTransport540 struct {
	mu    sync.Mutex
	calls int
	fail  int // number of initial calls that fail
}

func (t *fakeFeishuTransport540) RoundTrip(r *http.Request) (*http.Response, error) {
	t.mu.Lock()
	t.calls++
	n := t.calls
	t.mu.Unlock()
	body := `{"code":0,"tenant_access_token":"t-xyz","expire":7200}`
	if n <= t.fail {
		body = `{"code":99991668,"msg":"internal"}`
	}
	return &http.Response{
		StatusCode: http.StatusOK,
		Status:     "200 OK",
		Proto:      "HTTP/1.1",
		ProtoMajor: 1,
		ProtoMinor: 1,
		Header:     make(http.Header),
		Body:       io.NopCloser(strings.NewReader(body)),
		Request:    r,
	}, nil
}

func TestFeishuFetchTokenWithRetrySucceedsAfterFailures(t *testing.T) {
	transport := &fakeFeishuTransport540{fail: 2}
	a := &feishuAdapter{
		name:       "test",
		appID:      "app",
		appSecret:  "sec",
		httpClient: &http.Client{Transport: transport},
	}
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	if err := a.fetchTokenWithRetry(ctx, []time.Duration{time.Millisecond, 2 * time.Millisecond}); err != nil {
		t.Fatalf("expected retry loop to succeed: %v", err)
	}
	a.mu.RLock()
	tok := a.token
	a.mu.RUnlock()
	if tok != "t-xyz" {
		t.Fatalf("token not set after retry success: %q", tok)
	}
	transport.mu.Lock()
	calls := transport.calls
	transport.mu.Unlock()
	if calls != 3 {
		t.Fatalf("expected 3 token attempts (2 fail + 1 ok), got %d", calls)
	}
}

type slowFeishuTransport540 struct{}

func (slowFeishuTransport540) RoundTrip(r *http.Request) (*http.Response, error) {
	select {
	case <-r.Context().Done():
		return nil, r.Context().Err()
	case <-time.After(200 * time.Millisecond):
	}
	return &http.Response{
		StatusCode: http.StatusOK, Status: "200 OK",
		Proto: "HTTP/1.1", ProtoMajor: 1, ProtoMinor: 1,
		Header:  make(http.Header),
		Body:    io.NopCloser(strings.NewReader(`{"code":0,"tenant_access_token":"t"}`)),
		Request: r,
	}, nil
}

func TestFeishuFetchTokenWithRetryContextCancel(t *testing.T) {
	a := &feishuAdapter{
		name: "test", appID: "app", appSecret: "sec",
		httpClient: &http.Client{Transport: slowFeishuTransport540{}},
	}
	ctx, cancel := context.WithTimeout(context.Background(), 50*time.Millisecond)
	defer cancel()
	if err := a.fetchTokenWithRetry(ctx, []time.Duration{time.Millisecond}); err == nil {
		t.Fatal("expected ctx cancellation to abort retry loop")
	}
}

// ---- Bug E: TG structured mention stripping ----

func tgEntities540(pairs ...[2]int) []any {
	out := make([]any, 0, len(pairs))
	for _, p := range pairs {
		out = append(out, map[string]any{
			"type": "mention",
			"offset": func() any {
				if p[0] < 1000000 {
					return float64(p[0])
				}
				return p[0]
			}(),
			"length": func() any {
				if p[1] < 1000000 {
					return float64(p[1])
				}
				return p[1]
			}(),
		})
	}
	return out
}

func TestTGStripBotMentionEntitiesExact(t *testing.T) {
	// botUN="dev"; mention entity covers only "@dev" (offset 0, len 4).
	// "@devops" is a DIFFERENT user's mention entity and must survive.
	ents := tgEntities540([2]int{0, 4}, [2]int{5, 8}) // @dev, @devops
	got := tgStripBotMention("@dev @devops status", "dev", ents)
	if got != "@devops status" {
		t.Fatalf("entity-based strip = %q, want %q", got, "@devops status")
	}
}

func TestTGStripBotMentionFallbackWordBoundary(t *testing.T) {
	cases := []struct {
		in, want string
	}{
		{"@dev /status", "/status"},
		{"@devops status", "@devops status"},     // prefix collision preserved
		{"dev@devtools.com", "dev@devtools.com"}, // email preserved
		{"hi @dev run tests", "hi run tests"},
		{"@dev", ""},
	}
	for _, tc := range cases {
		if got := tgStripBotMention(tc.in, "dev", nil); got != tc.want {
			t.Errorf("fallback strip(%q) = %q, want %q", tc.in, got, tc.want)
		}
	}
}

func TestTGStripBotMentionEntitiesNoBotMention(t *testing.T) {
	// Entities present but none matches the bot username: text untouched.
	ents := tgEntities540([2]int{0, 8})
	if got := tgStripBotMention("@devops go", "dev", ents); got != "@devops go" {
		t.Fatalf("entities without bot mention must not alter text: %q", got)
	}
}

func TestTGStripBotMentionUTF16Offsets(t *testing.T) {
	// "⚠️ @dev run" — emoji is 2 UTF-16 units, so @dev starts at unit 3.
	ents := tgEntities540([2]int{3, 4})
	got := tgStripBotMention("\u26a0\ufe0f @dev run", "dev", ents)
	if got != "\u26a0\ufe0f run" {
		t.Fatalf("UTF-16 offset handling wrong: %q", got)
	}
}
