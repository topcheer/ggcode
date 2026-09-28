package im

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"math"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"maunium.net/go/mautrix"
	"maunium.net/go/mautrix/event"
	"maunium.net/go/mautrix/id"
)

func TestMatrixChunkContentPlainAndHTML(t *testing.T) {
	c := matrixChunkContent("hello **world**", "")
	if c.MsgType != event.MsgText {
		t.Fatalf("msgtype = %v, want m.text", c.MsgType)
	}
	if c.Body != "hello **world**" {
		t.Fatalf("body = %q, want the raw chunk", c.Body)
	}
	if c.Format != event.FormatHTML || !strings.Contains(c.FormattedBody, "<strong>") {
		t.Fatalf("expected HTML formatting with <strong>, got format=%v formatted=%q", c.Format, c.FormattedBody)
	}
	if c.RelatesTo != nil {
		t.Fatalf("empty threadID must not set RelatesTo, got %+v", c.RelatesTo)
	}
}

func TestMatrixChunkContentThreadRelation(t *testing.T) {
	c := matrixChunkContent("chunk", "!thread:example.org")
	if c.RelatesTo == nil {
		t.Fatal("non-empty threadID must set RelatesTo")
	}
	if c.RelatesTo.Type != event.RelThread || c.RelatesTo.EventID != id.EventID("!thread:example.org") {
		t.Fatalf("relates-to = %+v, want thread → !thread:example.org", c.RelatesTo)
	}
}

func TestMatrixRetryDelay(t *testing.T) {
	fallback := matrixInterMessageDelay * 2
	cases := []struct {
		name  string
		extra map[string]any
		want  time.Duration
	}{
		{"no extra data", map[string]any{}, fallback},
		{"non-float value", map[string]any{"retry_after_ms": "soon"}, fallback},
		{"zero", map[string]any{"retry_after_ms": float64(0)}, fallback},
		{"negative", map[string]any{"retry_after_ms": float64(-5)}, fallback},
		{"positive passes through", map[string]any{"retry_after_ms": float64(250)}, 250 * time.Millisecond},
		{"inf clamps to 24h (#664)", map[string]any{"retry_after_ms": math.Inf(1)}, 24 * time.Hour},
		{"overflow clamps to 24h (#664)", map[string]any{"retry_after_ms": 1e19}, 24 * time.Hour},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := matrixRetryDelay(tc.extra); got != tc.want {
				t.Fatalf("matrixRetryDelay(%v) = %v, want %v", tc.extra, got, tc.want)
			}
		})
	}
}

func TestMatrixSleepCtxReturnsOnCancel(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	start := time.Now()
	err := matrixSleepCtx(ctx, time.Hour)
	if elapsed := time.Since(start); elapsed > time.Second {
		t.Fatalf("cancellation must return immediately, took %v", elapsed)
	}
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("err = %v, want context.Canceled", err)
	}
}

func TestMatrixSleepCtxElapsed(t *testing.T) {
	if err := matrixSleepCtx(context.Background(), time.Millisecond); err != nil {
		t.Fatalf("short wait must succeed, got %v", err)
	}
}

func newMatrixOutboundTestAdapter(t *testing.T, srvURL string) *matrixAdapter {
	t.Helper()
	mc, err := mautrix.NewClient(srvURL, "", "test-token")
	if err != nil {
		t.Skipf("cannot build matrix client in test env: %v", err)
	}
	return &matrixAdapter{name: "test", client: mc}
}

// TestMatrixSendChunkWithRetryRateLimited pins the observable 429 behavior
// through a real mautrix client. NOTE: with mautrix v0.30, HTTPError.Unwrap
// returns a VALUE RespError, so errors.As(err, **RespError) never matches and
// the M_LIMIT_EXCEEDED retry branch is unreachable dead code - a 429 breaks
// out and is wrapped immediately (behavior identical before/after this
// refactor). If a mautrix upgrade makes errors.As match, this test will fail
// and should be revisited together with the retry branch.
func TestMatrixSendChunkWithRetryRateLimited(t *testing.T) {
	var calls atomic.Int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if !strings.Contains(r.URL.Path, "/send/m.room.message/") {
			t.Errorf("unexpected path %q", r.URL.Path)
		}
		if got := r.Header.Get("Authorization"); got != "Bearer test-token" {
			t.Errorf("authorization = %q, want bearer token", got)
		}
		w.Header().Set("Content-Type", "application/json")
		calls.Add(1)
		w.WriteHeader(http.StatusTooManyRequests)
		fmt.Fprint(w, `{"errcode":"M_LIMIT_EXCEEDED","error":"limited","retry_after_ms":1}`)
	}))
	defer srv.Close()

	a := newMatrixOutboundTestAdapter(t, srv.URL)
	err := a.sendChunkWithRetry(context.Background(), a.client, "!room:example.org", matrixChunkContent("hello", ""))
	if err == nil || !strings.HasPrefix(err.Error(), "matrix send to !room:example.org: ") {
		t.Fatalf("err = %v, want wrapped 'matrix send to !room:example.org: ...'", err)
	}
	if got := calls.Load(); got != 1 {
		t.Fatalf("calls = %d, want 1 (retry branch unreachable under mautrix v0.30)", got)
	}
}

// TestMatrixSendTextMultiChunkEndToEnd pins the sendText orchestration over
// an oversized text: markdown split, per-chunk txn sequence, and exactly one
// inter-message pacing sleep (500ms, accepted once per r198 precedent).
func TestMatrixSendTextMultiChunkEndToEnd(t *testing.T) {
	var mu sync.Mutex
	var txnIDs []string
	var bodies []string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var req struct {
			MsgType string `json:"msgtype"`
			Body    string `json:"body"`
		}
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			t.Errorf("decode request body: %v", err)
		}
		parts := strings.Split(r.URL.Path, "/")
		mu.Lock()
		txnIDs = append(txnIDs, parts[len(parts)-1])
		bodies = append(bodies, req.Body)
		mu.Unlock()
		w.Header().Set("Content-Type", "application/json")
		fmt.Fprint(w, `{"event_id":"$ok"}`)
	}))
	defer srv.Close()

	a := newMatrixOutboundTestAdapter(t, srv.URL)
	text := strings.Repeat("x", 61000) // > matrixMaxMessageLen → ≥2 chunks
	if err := a.sendText(context.Background(), "!room:example.org", "", text); err != nil {
		t.Fatalf("sendText failed: %v", err)
	}
	mu.Lock()
	defer mu.Unlock()
	if len(txnIDs) < 2 {
		t.Fatalf("expected ≥2 chunk sends, got %d", len(txnIDs))
	}
	if txnIDs[0] != "ggcode-1" || txnIDs[1] != "ggcode-2" {
		t.Fatalf("txn sequence = %v, want [ggcode-1 ggcode-2]", txnIDs)
	}
	for i, b := range bodies {
		if b == "" {
			t.Errorf("chunk %d body is empty", i)
		}
	}
	if len(bodies) >= 2 && bodies[0] == bodies[1] {
		t.Fatalf("distinct chunks must carry distinct bodies")
	}
}

// TestMatrixSendTextErrorWrapped pins the hard-failure wrap: a non-rate-limit
// HTTP error surfaces as "matrix send to <room>: ...".
func TestMatrixSendTextErrorWrapped(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Error(w, "boom", http.StatusInternalServerError)
	}))
	defer srv.Close()

	a := newMatrixOutboundTestAdapter(t, srv.URL)
	err := a.sendText(context.Background(), "!room:example.org", "", "hello")
	if err == nil || !strings.HasPrefix(err.Error(), "matrix send to !room:example.org: ") {
		t.Fatalf("err = %v, want wrapped 'matrix send to !room:example.org: ...'", err)
	}
}

// TestMatrixSendTextGuards pins the not-connected guard.
func TestMatrixSendTextGuards(t *testing.T) {
	a := &matrixAdapter{name: "test"} // no client
	err := a.sendText(context.Background(), "!room:example.org", "", "hello")
	if err == nil || err.Error() != "matrix adapter not connected" {
		t.Fatalf("err = %v, want not-connected guard", err)
	}
}

// TestMatrixSendTextCancelDuringPacing pins that cancellation during the
// inter-message delay propagates BARE (no "matrix send to" wrap).
func TestMatrixSendTextCancelDuringPacing(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		fmt.Fprint(w, `{"event_id":"$ok"}`)
	}))
	defer srv.Close()

	a := newMatrixOutboundTestAdapter(t, srv.URL)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	go func() {
		time.Sleep(50 * time.Millisecond) // first chunk lands; cancel during 500ms pacing
		cancel()
	}()
	err := a.sendText(ctx, "!room:example.org", "", strings.Repeat("x", 61000))
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("err = %v, want context.Canceled", err)
	}
	if strings.Contains(err.Error(), "matrix send to") {
		t.Fatalf("cancellation must propagate bare, got %q", err.Error())
	}
}
