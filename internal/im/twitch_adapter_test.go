package im

import (
	"bufio"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/topcheer/ggcode/internal/config"
)

// ---------------------------------------------------------------------------
// Test helpers
// ---------------------------------------------------------------------------

// twitchFakeIRCServer is a minimal in-process Twitch IRC server: a TCP
// listener that greets the client, records everything it sends, and lets the
// test script the conversation.
type twitchFakeIRCServer struct {
	listener net.Listener

	mu       sync.Mutex
	received []string    // every raw line the client sent
	written  chan string // lines pushed to the client
	quit     chan struct{}
	quitOnce sync.Once

	connOpened chan struct{} // signaled on each accepted connection
}

func newTwitchFakeIRCServer(t *testing.T) *twitchFakeIRCServer {
	t.Helper()
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("listen: %v", err)
	}
	s := &twitchFakeIRCServer{
		listener:   ln,
		written:    make(chan string, 64),
		quit:       make(chan struct{}),
		connOpened: make(chan struct{}, 16),
	}
	go s.acceptLoop()
	t.Cleanup(func() { s.stop() })
	return s
}

func (s *twitchFakeIRCServer) stop() {
	s.quitOnce.Do(func() { close(s.quit) })
	s.listener.Close()
}

func (s *twitchFakeIRCServer) acceptLoop() {
	for {
		conn, err := s.listener.Accept()
		if err != nil {
			return
		}
		select {
		case s.connOpened <- struct{}{}:
		default:
		}
		go s.serveConn(conn)
	}
}

func (s *twitchFakeIRCServer) serveConn(conn net.Conn) {
	// Close client connections when the server stops, so the adapter's read
	// loop observes EOF instead of blocking forever.
	go func() {
		<-s.quit
		conn.Close()
	}()
	defer conn.Close()
	// Drain any scripted writes in the background so writes never block tests.
	go func() {
		for {
			select {
			case <-s.quit:
				return
			case line := <-s.written:
				fmt.Fprintf(conn, "%s\r\n", line)
			}
		}
	}()
	scanner := bufio.NewScanner(conn)
	for scanner.Scan() {
		line := strings.TrimRight(scanner.Text(), "\r")
		s.mu.Lock()
		s.received = append(s.received, line)
		s.mu.Unlock()
		select {
		case <-s.quit:
			return
		default:
		}
	}
}

// send pushes a raw line to the connected client.
func (s *twitchFakeIRCServer) send(line string) { s.written <- line }

func (s *twitchFakeIRCServer) lines() []string {
	s.mu.Lock()
	defer s.mu.Unlock()
	out := make([]string, len(s.received))
	copy(out, s.received)
	return out
}

// dialTo returns a dial func pointing at the fake server.
func (s *twitchFakeIRCServer) dialTo() func(addr string) (net.Conn, error) {
	return func(string) (net.Conn, error) { return net.Dial("tcp", s.listener.Addr().String()) }
}

func newTestTwitchAdapter(dial func(addr string) (net.Conn, error)) *twitchAdapter {
	return &twitchAdapter{
		name:    "test-twitch",
		manager: NewManager(),
		token:   "oauth:test-token",
		nick:    "testbot",
		dialIRC: dial,
	}
}

// ---------------------------------------------------------------------------
// Issue #972 problem 1: Close() during the backoff window must stop reconnect
// ---------------------------------------------------------------------------

// TestTwitchCloseDuringBackoffDoesNotReconnect verifies that Close() called
// while the run loop is sleeping in its reconnect backoff window prevents any
// further connection attempts (issue #972 problem 1: closed=true but conn=nil
// made QUIT a no-op and the adapter reconnected forever).
func TestTwitchCloseDuringBackoffDoesNotReconnect(t *testing.T) {
	srv := newTwitchFakeIRCServer(t)
	adapter := newTestTwitchAdapter(srv.dialTo())

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	done := make(chan struct{})
	go func() { adapter.run(ctx); close(done) }()

	// Wait for the first connection, then kill it server-side so the run
	// loop enters the backoff window.
	select {
	case <-srv.connOpened:
	case <-time.After(3 * time.Second):
		t.Fatal("adapter never connected to fake IRC server")
	}

	// Force a disconnect: stopping the server closes the conn; scanner EOF
	// makes connectAndServe return and the run loop enter backoff.
	srv.stop()

	// Give the run loop a moment to land inside the backoff sleep
	// (backoff ≥ 5s * 0.75 jitter, so a short sleep is safely inside it).
	time.Sleep(300 * time.Millisecond)

	// Close during the backoff window.
	if err := adapter.Close(); err != nil {
		t.Fatalf("Close: %v", err)
	}

	// The run loop must exit promptly (well before the ~3.75s lower-bound of
	// the jittered 5s backoff expires) — proves the stopCh interrupt worked
	// rather than the timer simply firing.
	select {
	case <-done:
		// success
	case <-time.After(2 * time.Second):
		t.Fatal("run loop did not exit after Close() during backoff window")
	}

	// And it must STAY exited — no zombie reconnect goroutine.
	time.Sleep(500 * time.Millisecond)
	select {
	case <-done:
	default:
		t.Fatal("done channel closed then reopened — impossible, but guard anyway")
	}
	adapter.mu.RLock()
	closed := adapter.closed
	adapter.mu.RUnlock()
	if !closed {
		t.Fatal("adapter.closed should remain true")
	}
}

// ---------------------------------------------------------------------------
// Issue #972 problem 3: auth-failure NOTICE terminates the run loop
// ---------------------------------------------------------------------------

// TestTwitchAuthFailureNOTICETerminatesRun verifies that a NOTICE
// "Login authentication failed" makes connectAndServe return
// errTwitchAuthFailed and the run loop exit with a published error state
// instead of reconnecting forever with a dead token.
func TestTwitchAuthFailureNOTICETerminatesRun(t *testing.T) {
	srv := newTwitchFakeIRCServer(t)
	adapter := newTestTwitchAdapter(srv.dialTo())

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	done := make(chan struct{})
	go func() { adapter.run(ctx); close(done) }()

	select {
	case <-srv.connOpened:
	case <-time.After(3 * time.Second):
		t.Fatal("adapter never connected")
	}

	// Twitch rejects the credentials with a NOTICE and then disconnects.
	srv.send(":tmi.twitch.tv NOTICE * :Login authentication failed")

	select {
	case <-done:
		// run loop exited — correct
	case <-time.After(3 * time.Second):
		t.Fatal("run loop did not terminate after auth-failure NOTICE")
	}

	// The final published state must carry the explicit auth error.
	mgr := adapter.manager
	mgr.mu.RLock()
	state, ok := mgr.adapters[adapter.name]
	mgr.mu.RUnlock()
	if !ok {
		t.Fatal("no adapter state published")
	}
	if state.Healthy {
		t.Fatalf("expected Healthy=false after auth failure, got %+v", state)
	}
	if !strings.Contains(strings.ToLower(state.LastError), "authentication failed") {
		t.Fatalf("expected LastError to mention authentication failure, got %q", state.LastError)
	}
}

// TestTwitchIsAuthFailureNOTICE covers the NOTICE classifier.
func TestTwitchIsAuthFailureNOTICE(t *testing.T) {
	cases := []struct {
		trailing string
		want     bool
	}{
		{":tmi.twitch.tv NOTICE * :Login authentication failed", true},
		{"Login authentication failed", true},
		{"Improperly formatted auth", true},
		{"Your message was not sent because you are sending messages too quickly", false},
		{"", false},
	}
	for _, tc := range cases {
		msg := parseIRCLine(":tmi.twitch.tv NOTICE * :" + tc.trailing)
		if msg == nil {
			msg = &ircMessage{Command: "NOTICE", Trailing: tc.trailing}
		}
		if got := isTwitchAuthFailureNOTICE(msg); got != tc.want {
			t.Errorf("isTwitchAuthFailureNOTICE(%q) = %v, want %v", tc.trailing, got, tc.want)
		}
	}
	if isTwitchAuthFailureNOTICE(nil) {
		t.Error("isTwitchAuthFailureNOTICE(nil) must be false")
	}
}

// ---------------------------------------------------------------------------
// Issue #972 problem 2: DMs go through the Helix API
// ---------------------------------------------------------------------------

// TestTwitchDMViaHelixAPI verifies that a non-# target is delivered via
// POST /helix/messages (whisper) with a preceding users?login= lookup, and
// that no PRIVMSG-to-nick is ever issued (issue #972 problem 2).
func TestTwitchDMViaHelixAPI(t *testing.T) {
	var (
		mu          sync.Mutex
		usersCalls  int
		msgCalls    int
		lastWhisper struct {
			from   string
			body   string
			auth   string
			client string
		}
	)
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		mu.Lock()
		defer mu.Unlock()
		switch {
		case r.URL.Path == "/helix/users":
			usersCalls++
			if r.Header.Get("Authorization") != "Bearer test-token" {
				t.Errorf("users: Authorization = %q, want Bearer test-token", r.Header.Get("Authorization"))
			}
			logins := r.URL.Query()["login"]
			want := map[string]bool{"testbot": true, "viewer": true}
			for _, l := range logins {
				delete(want, l)
			}
			if len(want) > 0 {
				t.Errorf("users: unexpected/missing logins: %v", logins)
			}
			w.Header().Set("Content-Type", "application/json")
			fmt.Fprint(w, `{"data":[{"id":"111","login":"testbot"},{"id":"222","login":"viewer"}]}`)
		case r.URL.Path == "/helix/messages":
			msgCalls++
			lastWhisper.from = r.URL.Query().Get("user_id")
			lastWhisper.auth = r.Header.Get("Authorization")
			lastWhisper.client = r.Header.Get("Client-Id")
			var body map[string]string
			_ = json.NewDecoder(r.Body).Decode(&body)
			lastWhisper.body = body["message"]
			if body["recipient_id"] != "222" {
				t.Errorf("messages: recipient_id = %q, want 222", body["recipient_id"])
			}
			w.WriteHeader(http.StatusNoContent)
		default:
			t.Errorf("unexpected Helix request: %s %s", r.Method, r.URL.Path)
			w.WriteHeader(http.StatusNotFound)
		}
	}))
	defer srv.Close()

	adapter := &twitchAdapter{
		name:         "test-twitch",
		manager:      NewManager(),
		token:        "oauth:test-token",
		nick:         "testbot",
		clientID:     "my-client",
		helixBaseURL: srv.URL,
	}

	if err := adapter.sendTwitchMessage(context.Background(), "viewer", "hello DM"); err != nil {
		t.Fatalf("sendTwitchMessage DM: %v", err)
	}

	mu.Lock()
	defer mu.Unlock()
	if usersCalls != 1 {
		t.Errorf("expected 1 users lookup, got %d", usersCalls)
	}
	if msgCalls != 1 {
		t.Errorf("expected 1 whisper POST, got %d", msgCalls)
	}
	if lastWhisper.from != "111" {
		t.Errorf("whisper user_id (sender) = %q, want 111", lastWhisper.from)
	}
	if lastWhisper.body != "hello DM" {
		t.Errorf("whisper message = %q, want %q", lastWhisper.body, "hello DM")
	}
	if lastWhisper.auth != "Bearer test-token" {
		t.Errorf("whisper Authorization = %q", lastWhisper.auth)
	}
	if lastWhisper.client != "my-client" {
		t.Errorf("whisper Client-Id = %q", lastWhisper.client)
	}
}

// TestTwitchDMHelixFailurePublishesState verifies a Helix failure is surfaced
// via publishState instead of being silently dropped.
func TestTwitchDMHelixFailurePublishesState(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Error(w, `{"message":"missing scope"}`, http.StatusForbidden)
	}))
	defer srv.Close()

	mgr := NewManager()
	adapter := &twitchAdapter{
		name:         "test-twitch",
		manager:      mgr,
		token:        "oauth:test-token",
		nick:         "testbot",
		helixBaseURL: srv.URL,
	}

	err := adapter.sendTwitchMessage(context.Background(), "viewer", "hello")
	if err == nil {
		t.Fatal("expected error on Helix failure")
	}

	mgr.mu.RLock()
	state, ok := mgr.adapters[adapter.name]
	mgr.mu.RUnlock()
	if !ok {
		t.Fatal("no state published on Helix failure")
	}
	if !strings.Contains(state.LastError, "whisper") && !strings.Contains(state.LastError, "Helix") {
		t.Fatalf("expected whisper/Helix error in state, got %q", state.LastError)
	}
}

// TestTwitchChannelMessageStillUsesPRIVMSG guards the non-DM path: channel
// targets keep using IRC PRIVMSG.
func TestTwitchChannelMessageStillUsesPRIVMSG(t *testing.T) {
	srv := newTwitchFakeIRCServer(t)
	adapter := newTestTwitchAdapter(srv.dialTo())

	// Wire the conn so sendRaw works.
	client, err := net.Dial("tcp", srv.listener.Addr().String())
	if err != nil {
		t.Fatalf("dial: %v", err)
	}
	defer client.Close()
	adapter.mu.Lock()
	adapter.conn = client
	adapter.mu.Unlock()

	// DM target must NOT hit the fake IRC server as PRIVMSG; a channel must.
	if err := adapter.sendTwitchMessage(context.Background(), "#mychan", "hi"); err != nil {
		t.Fatalf("channel send: %v", err)
	}
	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		for _, l := range srv.lines() {
			if strings.HasPrefix(l, "PRIVMSG #mychan :hi") {
				return // success
			}
		}
		time.Sleep(20 * time.Millisecond)
	}
	t.Fatal("PRIVMSG to #mychan was never sent")
}

// ---------------------------------------------------------------------------
// Issue #972 side items
// ---------------------------------------------------------------------------

// TestTwitchBackoffResetAfterHealthyCycle covers twitchNextBackoff: a serve
// cycle that lasted >= twitchBackoffResetAfter resets the backoff to its
// initial value; short flapping cycles still grow up to the cap.
func TestTwitchBackoffResetAfterHealthyCycle(t *testing.T) {
	// Healthy cycle: resets to initial regardless of accumulated backoff.
	if got := twitchNextBackoff(twitchMaxBackoff, twitchReconnectBackoff, twitchMaxBackoff, twitchBackoffResetAfter); got != twitchReconnectBackoff {
		t.Errorf("healthy cycle: got %v, want reset to %v", got, twitchReconnectBackoff)
	}
	// Flapping: doubles and caps.
	if got := twitchNextBackoff(5*time.Second, 5*time.Second, 120*time.Second, time.Second); got != 10*time.Second {
		t.Errorf("flap 1: got %v, want 10s", got)
	}
	if got := twitchNextBackoff(80*time.Second, 5*time.Second, 120*time.Second, time.Second); got != 120*time.Second {
		t.Errorf("cap: got %v, want 120s", got)
	}
	// Just under the reset threshold still grows.
	if got := twitchNextBackoff(10*time.Second, 5*time.Second, 120*time.Second, twitchBackoffResetAfter-time.Second); got != 20*time.Second {
		t.Errorf("below threshold: got %v, want 20s", got)
	}
}

// TestTwitchUnescapeTagOrder covers the single-pass unescaping: the literal
// sequence `\s`-produced-by-`\\` must not corrupt (issue #972 side item).
func TestTwitchUnescapeTagOrder(t *testing.T) {
	cases := []struct{ in, want string }{
		{`hello\sword`, "hello word"},    // \s → space
		{`line1\nline2`, "line1\nline2"}, // \n → newline
		{`a\\sb`, `a\sb`},                // \\ then 's' → backslash + s (sequential ReplaceAll corrupted this)
		{`semi\:colon`, "semi;colon"},    // \: → semicolon
		{`back\\slash`, `back\slash`},    // \\ → backslash
		{"plain", "plain"},               // fast path, no escapes
		{`trailing\`, `trailing\`},       // dangling backslash kept
		{`unknown\q`, `unknown\q`},       // unknown escape kept literally
	}
	for _, tc := range cases {
		if got := unescapeTwitchTag(tc.in); got != tc.want {
			t.Errorf("unescapeTwitchTag(%q) = %q, want %q", tc.in, got, tc.want)
		}
	}
}

// TestTwitchParseTagsRoundTrip sanity-checks tag parsing with escapes.
func TestTwitchParseTagsRoundTrip(t *testing.T) {
	tags := parseTwitchTags("display-name=Nice\\sName;user-id=42")
	if tags["display-name"] != "Nice Name" {
		t.Errorf("display-name = %q, want %q", tags["display-name"], "Nice Name")
	}
	if tags["user-id"] != "42" {
		t.Errorf("user-id = %q", tags["user-id"])
	}
}

// Unused import guard (keeps io referenced if the file evolves).
var _ = io.Discard

// ---------------------------------------------------------------------------
// Pre-existing constructor / helper tests (restored from HEAD)
// ---------------------------------------------------------------------------

func TestNewTwitchAdapter_MissingToken(t *testing.T) {
	_, err := newTwitchAdapter("test", config.IMConfig{}, config.IMAdapterConfig{
		Enabled: true, Platform: "twitch", Extra: map[string]interface{}{},
	}, nil)
	if err == nil || !strings.Contains(err.Error(), "token") {
		t.Errorf("expected token error: %v", err)
	}
}

func TestNewTwitchAdapter_MissingNick(t *testing.T) {
	_, err := newTwitchAdapter("test", config.IMConfig{}, config.IMAdapterConfig{
		Enabled: true, Platform: "twitch", Extra: map[string]interface{}{"token": "oauth:xxx"},
	}, nil)
	if err == nil || !strings.Contains(err.Error(), "nick") {
		t.Errorf("expected nick error: %v", err)
	}
}

func TestNewTwitchAdapter_ValidConfig(t *testing.T) {
	a, err := newTwitchAdapter("test", config.IMConfig{}, config.IMAdapterConfig{
		Enabled: true, Platform: "twitch",
		Extra: map[string]interface{}{
			"token":    "oauth:abc123",
			"nick":     "MyBot",
			"channels": "channel1,channel2",
		},
	}, nil)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if a.nick != "mybot" {
		t.Errorf("nick = %q, want %q (should lowercase)", a.nick, "mybot")
	}
	if a.token != "oauth:abc123" {
		t.Errorf("token = %q", a.token)
	}
	if len(a.channels) != 2 {
		t.Fatalf("channels len = %d, want 2", len(a.channels))
	}
	if a.channels[0] != "#channel1" {
		t.Errorf("channels[0] = %q, want #channel1", a.channels[0])
	}
	if a.channels[1] != "#channel2" {
		t.Errorf("channels[1] = %q, want #channel2", a.channels[1])
	}
}

func TestNewTwitchAdapter_TokenPrefix(t *testing.T) {
	a, err := newTwitchAdapter("test", config.IMConfig{}, config.IMAdapterConfig{
		Enabled: true, Platform: "twitch",
		Extra: map[string]interface{}{
			"token": "abc123",
			"nick":  "bot",
		},
	}, nil)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !strings.HasPrefix(a.token, "oauth:") {
		t.Errorf("token should have oauth: prefix, got %q", a.token)
	}
}

func TestNewTwitchAdapter_ChannelHashPrefix(t *testing.T) {
	a, err := newTwitchAdapter("test", config.IMConfig{}, config.IMAdapterConfig{
		Enabled: true, Platform: "twitch",
		Extra: map[string]interface{}{
			"token":    "oauth:xxx",
			"nick":     "bot",
			"channels": "#already,needsHash",
		},
	}, nil)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if a.channels[0] != "#already" {
		t.Errorf("channels[0] = %q, should keep #", a.channels[0])
	}
	if a.channels[1] != "#needsHash" {
		t.Errorf("channels[1] = %q, should add #", a.channels[1])
	}
}

func TestNewTwitchAdapter_AdapterName(t *testing.T) {
	a, err := newTwitchAdapter("my-twitch", config.IMConfig{}, config.IMAdapterConfig{
		Enabled: true, Platform: "twitch",
		Extra: map[string]interface{}{"token": "oauth:xxx", "nick": "bot"},
	}, nil)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if a.Name() != "my-twitch" {
		t.Errorf("Name() = %q, want %q", a.Name(), "my-twitch")
	}
}

func TestNewTwitchAdapter_TriggerTypingNoop(t *testing.T) {
	a, _ := newTwitchAdapter("test", config.IMConfig{}, config.IMAdapterConfig{
		Enabled: true, Platform: "twitch",
		Extra: map[string]interface{}{"token": "oauth:xxx", "nick": "bot"},
	}, nil)
	if err := a.TriggerTyping(nil, ChannelBinding{}); err != nil {
		t.Errorf("TriggerTyping should be noop, got: %v", err)
	}
}

func TestParseTwitchTags(t *testing.T) {
	tests := []struct {
		name  string
		input string
		want  map[string]string
	}{
		{
			name:  "standard tags",
			input: "display-name=TestUser;user-id=12345",
			want:  map[string]string{"display-name": "TestUser", "user-id": "12345"},
		},
		{
			name:  "escaped spaces",
			input: "display-name=Hello\\sWorld",
			want:  map[string]string{"display-name": "Hello World"},
		},
		{
			name:  "escaped newline",
			input: "msg=Hello\\nWorld",
			want:  map[string]string{"msg": "Hello\nWorld"},
		},
		{
			name:  "escaped backslash and semicolon",
			input: "msg=path\\\\to\\\\file\\:name",
			want:  map[string]string{"msg": "path\\to\\file;name"},
		},
		{
			name:  "empty value",
			input: "key1=;key2=value",
			want:  map[string]string{"key1": "", "key2": "value"},
		},
		{
			name:  "no equals",
			input: "badtag",
			want:  map[string]string{},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := parseTwitchTags(tt.input)
			if len(got) != len(tt.want) {
				t.Fatalf("tags count = %d, want %d", len(got), len(tt.want))
			}
			for k, v := range tt.want {
				if got[k] != v {
					t.Errorf("tags[%q] = %q, want %q", k, got[k], v)
				}
			}
		})
	}
}

func TestUnescapeTwitchTag(t *testing.T) {
	tests := []struct {
		input, want string
	}{
		{"hello\\sworld", "hello world"},
		{"line1\\nline2", "line1\nline2"},
		{"path\\\\file", "path\\file"},
		{"semi\\:colon", "semi;colon"},
		{"no escapes", "no escapes"},
		{"", ""},
	}
	for _, tt := range tests {
		got := unescapeTwitchTag(tt.input)
		if got != tt.want {
			t.Errorf("unescapeTwitchTag(%q) = %q, want %q", tt.input, got, tt.want)
		}
	}
}

func TestStripTwitchMention(t *testing.T) {
	tests := []struct {
		text, nick, want string
	}{
		{"@bot hello", "bot", "hello"},
		{"bot hello world", "bot", "hello world"},
		{"hello world", "bot", "hello world"},
		{"@Bot hello", "bot", "@Bot hello"}, // case-sensitive, @Bot not stripped when nick is lowercase
		{"@bot @bot double", "bot", "double"},
	}
	for _, tt := range tests {
		got := stripTwitchMention(tt.text, tt.nick)
		if got != tt.want {
			t.Errorf("stripTwitchMention(%q, %q) = %q, want %q", tt.text, tt.nick, got, tt.want)
		}
	}
}
