package im

// Companion tests for connection-layer fixes #964 (nostr) and #963 (mattermost).

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/gorilla/websocket"
	"github.com/nbd-wtf/go-nostr"
)

// ---------------------------------------------------------------------------
// #964 nostr: backoff reset semantics
// ---------------------------------------------------------------------------

func TestNostrBackoffNextResetsAfterServedDisconnect(t *testing.T) {
	// Connect-phase failures double up to the cap.
	b := nostrReconnectBackoff // 5s
	for want := 2 * nostrReconnectBackoff; want <= nostrMaxBackoff; want *= 2 {
		if want > nostrMaxBackoff {
			want = nostrMaxBackoff
		}
		b = nostrBackoffNext(b, errors.New("connect failed"))
		if b != want {
			t.Fatalf("connect failure: backoff = %v, want %v", b, want)
		}
		if want == nostrMaxBackoff {
			break
		}
	}
	// Capped at max.
	if got := nostrBackoffNext(nostrMaxBackoff, errors.New("connect failed")); got != nostrMaxBackoff {
		t.Fatalf("cap: backoff = %v, want %v", got, nostrMaxBackoff)
	}
	// Serve-phase disconnect (watchdog / subscription closed) resets to the
	// short initial delay instead of staying pinned at 120s.
	if got := nostrBackoffNext(nostrMaxBackoff, fmt.Errorf("watchdog: %w", errNostrServed)); got != nostrReconnectBackoff {
		t.Fatalf("served disconnect: backoff = %v, want reset to %v", got, nostrReconnectBackoff)
	}
	// Clean exit (nil error, ctx canceled) also resets.
	if got := nostrBackoffNext(nostrMaxBackoff, nil); got != nostrReconnectBackoff {
		t.Fatalf("nil error: backoff = %v, want reset to %v", got, nostrReconnectBackoff)
	}
	// errors.Is must see the sentinel through wrapping.
	if !errors.Is(fmt.Errorf("subscription closed: %w", errNostrServed), errNostrServed) {
		t.Fatal("errors.Is should detect errNostrServed through wrapping")
	}
}

// ---------------------------------------------------------------------------
// #964 nostr: empty relay conns must surface an error, not nil
// ---------------------------------------------------------------------------

func TestNostrSendNostrDMNoRelaysReturnsError(t *testing.T) {
	priv := nostr.GeneratePrivateKey()
	pub, err := nostr.GetPublicKey(priv)
	if err != nil {
		t.Fatal(err)
	}
	a := &nostrAdapter{
		name:    "test",
		privKey: priv,
		pubKey:  pub,
		seen:    make(map[string]time.Time),
	}
	err = a.sendNostrDM(context.Background(), pub, "hello")
	if err == nil {
		t.Fatal("sendNostrDM with zero relay connections returned nil - message silently dropped (caller sendWithTimeout would skip retry)")
	}
	if !strings.Contains(err.Error(), "no relay connections") {
		t.Fatalf("unexpected error: %v", err)
	}
}

// ---------------------------------------------------------------------------
// #963 mattermost: file_ids must be in the POST payload
// ---------------------------------------------------------------------------

func TestMattermostSendTextWithFilesIncludesFileIDsInPost(t *testing.T) {
	var mu sync.Mutex
	var payloads []map[string]any
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/api/v4/posts" {
			w.Header().Set("Content-Type", "application/json")
			fmt.Fprintln(w, `{"id":"u1","username":"bot"}`)
			return
		}
		var p map[string]any
		_ = json.NewDecoder(r.Body).Decode(&p)
		mu.Lock()
		payloads = append(payloads, p)
		mu.Unlock()
		w.Header().Set("Content-Type", "application/json")
		fmt.Fprintln(w, `{"status":"OK","id":"post1"}`)
	}))
	defer srv.Close()

	a := &mattermostAdapter{
		name:      "test",
		baseURL:   srv.URL,
		token:     "tok",
		conn:      srv.Client(),
		seen:      make(map[string]time.Time),
		replyMode: "off",
	}
	if err := a.sendTextWithFiles(context.Background(), "chan1", "", "see attachment", []string{"file1", "file2"}); err != nil {
		t.Fatalf("sendTextWithFiles: %v", err)
	}
	mu.Lock()
	defer mu.Unlock()
	if len(payloads) != 1 {
		t.Fatalf("got %d posts, want 1", len(payloads))
	}
	ids, ok := payloads[0]["file_ids"].([]any)
	if !ok || len(ids) != 2 || ids[0] != "file1" || ids[1] != "file2" {
		t.Fatalf("first POST payload file_ids = %v, want [file1 file2] - files would be orphaned server-side", payloads[0]["file_ids"])
	}
}

// ---------------------------------------------------------------------------
// #963 mattermost: read deadline must be renewed on every message
// ---------------------------------------------------------------------------

func TestMattermostReadDeadlineRenewedOnMessages(t *testing.T) {
	saved := mattermostWSReadTimeout
	mattermostWSReadTimeout = 400 * time.Millisecond
	defer func() { mattermostWSReadTimeout = saved }()

	upgrader := websocket.Upgrader{}
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/api/v4/users/me":
			w.Header().Set("Content-Type", "application/json")
			fmt.Fprintln(w, `{"id":"bot1","username":"bot"}`)
			return
		case "/api/v4/users/me/teams":
			w.Header().Set("Content-Type", "application/json")
			fmt.Fprintln(w, `[{"name":"team"}]`)
			return
		case "/api/v4/websocket":
			c, err := upgrader.Upgrade(w, r, nil)
			if err != nil {
				return
			}
			defer c.Close()
			// Consume the auth frame, then stream app-level text frames far
			// more often than the shortened read timeout. If the read loop
			// only set the deadline at connect time (absolute deadline), the
			// client would time out at ~400ms despite this traffic.
			if _, _, err := c.ReadMessage(); err != nil {
				return
			}
			ticker := time.NewTicker(50 * time.Millisecond)
			defer ticker.Stop()
			for i := 0; ; i++ {
				if err := c.WriteMessage(websocket.TextMessage, []byte(fmt.Sprintf(`{"status":"OK","seq_reply":%d}`, i))); err != nil {
					return
				}
				if !sleepWS(ticker.C) {
					return
				}
			}
		default:
			http.NotFound(w, r)
		}
	}))
	defer srv.Close()

	a := &mattermostAdapter{
		name:      "test",
		baseURL:   srv.URL,
		token:     "tok",
		conn:      srv.Client(),
		seen:      make(map[string]time.Time),
		replyMode: "off",
	}

	ctx, cancel := context.WithTimeout(context.Background(), 900*time.Millisecond)
	defer cancel()

	errCh := make(chan error, 1)
	start := time.Now()
	go func() { errCh <- a.connectAndServe(ctx) }()

	select {
	case err := <-errCh:
		// Ctx cancellation returns nil; a deadline bug returns "ws read: timeout".
		if err != nil && strings.Contains(err.Error(), "timeout") {
			t.Fatalf("read deadline expired despite message traffic (not renewed): %v", err)
		}
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("connectAndServe did not return after ctx cancel")
	}
	if elapsed := time.Since(start); elapsed < 700*time.Millisecond {
		t.Fatalf("connection ended too early (%v) - deadline likely not renewed", elapsed)
	}
}

func sleepWS(tick <-chan time.Time) bool {
	select {
	case <-tick:
		return true
	case <-time.After(3 * time.Second):
		return false
	}
}

// ---------------------------------------------------------------------------
// #963 mattermost incidental: mention word boundary
// ---------------------------------------------------------------------------

func TestMattermostMentionWordBoundary(t *testing.T) {
	a := &mattermostAdapter{botUsername: "al", botUserID: "botid123"}
	cases := []struct {
		text string
		want bool
	}{
		{"@al hello", true},
		{"@al, hello", true},
		{"@alex hello", false},
		{"please email @alice", false},
		{"@botid123 ping", true},
		{"@botid1234 ping", false},
	}
	for _, c := range cases {
		if got := a.hasMention(c.text); got != c.want {
			t.Errorf("hasMention(%q) = %v, want %v", c.text, got, c.want)
		}
	}
	if got := a.stripMention("@alex hello"); got != "@alex hello" {
		t.Errorf("stripMention must not touch @alex, got %q", got)
	}
	if got := a.stripMention("@al hello"); got != "hello" {
		t.Errorf("stripMention(@al hello) = %q, want hello", got)
	}
}
