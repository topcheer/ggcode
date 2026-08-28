package im

import (
	"bufio"
	"bytes"
	"context"
	"crypto/tls"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
	"os"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/topcheer/ggcode/internal/config"
	"github.com/topcheer/ggcode/internal/debug"
	"github.com/topcheer/ggcode/internal/safego"
)

const (
	twitchHost             = "irc.chat.twitch.tv"
	twitchPort             = 6697
	twitchPingInterval     = 60 * time.Second
	twitchPongTimeout      = 30 * time.Second
	twitchReconnectBackoff = 5 * time.Second
	twitchMaxBackoff       = 120 * time.Second
	twitchMaxMessageLen    = 500
	// twitchInterMessageDelay is the delay between consecutive PRIVMSG lines.
	// Twitch limits non-VIP/mod accounts to 20 messages per 30 seconds
	// (= 1500ms minimum between messages) and 1 message per second per channel.
	// Source: https://dev.twitch.tv/docs/irc/#rate-limits
	twitchInterMessageDelay = 1500 * time.Millisecond

	// twitchHelixBaseURL is the Twitch Helix API root used for DMs (whispers).
	// Twitch chat IRC has no whisper channel: a PRIVMSG addressed to a nick is
	// not a valid DM route, so outbound DMs must go through Helix (issue #972).
	twitchHelixBaseURL = "https://api.twitch.tv"

	// twitchBackoffResetAfter is how long a connection must have served before
	// the reconnect backoff resets to its initial value. A connection that
	// lasted this long counts as a healthy cycle, not a flapping one (aligned
	// with the mattermost/nostr adapters).
	twitchBackoffResetAfter = 60 * time.Second
)

// errTwitchAuthFailed is returned by the read loop when Twitch rejects our
// credentials (NOTICE "Login authentication failed"). The run loop terminates
// instead of retrying forever with a dead token (issue #972).
var errTwitchAuthFailed = errors.New("twitch: login authentication failed")

// twitchDefaultHelixClient is the shared HTTP client for Helix API calls.
var twitchDefaultHelixClient = &http.Client{Timeout: 20 * time.Second}

// ---------------------------------------------------------------------------
// Adapter struct
// ---------------------------------------------------------------------------

type twitchAdapter struct {
	name    string
	manager *Manager

	// Connection
	token    string // OAuth token (oauth:xxxxx)
	nick     string // username (lowercase)
	channels []string
	proxy    string // HTTP/SOCKS5 proxy URL

	mu        sync.RWMutex
	conn      net.Conn
	connected bool
	closed    bool
	// writeMu serializes socket writes (#1249): sendRaw only held mu.RLock
	// while writing, but four real callers (read-loop PONG/PRIVMSG, keepalive
	// PING, Send, Close QUIT) can race. net.Conn does not guarantee line
	// atomicity, so a long PRIVMSG crossing a PING packet interleaved into a
	// corrupt IRC line and a server disconnect. mu stays for conn lifecycle.
	writeMu sync.Mutex

	// stopCh is closed by Close() and wakes the backoff sleep, so a Close()
	// issued during the reconnect window cannot leave the adapter running
	// (issue #972: closed=true but conn=nil made QUIT a no-op and the sleep
	// expired into a fresh reconnect that then served forever).
	stopCh chan struct{}
	// dialIRC is injectable for tests; nil means the default TLS dialer.
	dialIRC func(addr string) (net.Conn, error)

	// Helix API state for DMs (whispers).
	clientID     string // optional; Helix infers it from the Bearer token when empty
	helixBaseURL string // empty → twitchHelixBaseURL; overridable in tests
	httpClient   *http.Client
	userIDs      map[string]string // login (lowercase) → twitch user id cache
	userIDsMu    sync.Mutex
}

func newTwitchAdapter(name string, _ config.IMConfig, adapterCfg config.IMAdapterConfig, mgr *Manager) (*twitchAdapter, error) {
	token := strings.TrimSpace(stringValue(adapterCfg.Extra, "token"))
	if token == "" {
		token = strings.TrimSpace(os.Getenv("TWITCH_TOKEN"))
	}
	if token == "" {
		return nil, fmt.Errorf("Twitch OAuth token is required for adapter %q (set 'token' in extra or TWITCH_TOKEN env)", name)
	}
	if !strings.HasPrefix(token, "oauth:") {
		token = "oauth:" + token
	}

	nick := strings.TrimSpace(stringValue(adapterCfg.Extra, "nick"))
	if nick == "" {
		nick = strings.TrimSpace(os.Getenv("TWITCH_NICK"))
	}
	if nick == "" {
		return nil, fmt.Errorf("Twitch nick (username) is required for adapter %q (set 'nick' in extra or TWITCH_NICK env)", name)
	}
	nick = strings.ToLower(nick)

	channels := parseCommaList(stringValue(adapterCfg.Extra, "channels"), os.Getenv("TWITCH_CHANNELS"))
	for i, ch := range channels {
		ch = strings.TrimSpace(ch)
		if ch != "" && !strings.HasPrefix(ch, "#") {
			channels[i] = "#" + ch
		}
	}

	proxy := resolveProxy(stringValue(adapterCfg.Extra, "proxy"), "TWITCH_PROXY")

	// Client ID is optional for Helix: a user OAuth token already identifies
	// the client, but sending it explicitly is harmless and avoids issues with
	// some token types.
	clientID := strings.TrimSpace(stringValue(adapterCfg.Extra, "client_id"))
	if clientID == "" {
		clientID = strings.TrimSpace(os.Getenv("TWITCH_CLIENT_ID"))
	}

	return &twitchAdapter{
		name:     name,
		manager:  mgr,
		token:    token,
		nick:     nick,
		channels: channels,
		proxy:    proxy,
		clientID: clientID,
	}, nil
}

func (a *twitchAdapter) Name() string { return a.name }

func (a *twitchAdapter) Start(ctx context.Context) {
	debug.Log("twitch", "adapter=%s start nick=%s channels=%v", a.name, a.nick, a.channels)
	a.publishState(false, "connecting", "")
	safego.Go("im.twitch.run", func() { a.run(ctx) })
}

func (a *twitchAdapter) Close() error {
	a.mu.Lock()
	if a.closed {
		// Idempotent: a second Close only needs to drop the conn again.
		conn := a.conn
		a.mu.Unlock()
		if conn != nil {
			conn.Close()
		}
		return nil
	}
	a.closed = true
	conn := a.conn
	a.connected = false
	if a.stopCh == nil {
		a.stopCh = make(chan struct{})
	}
	close(a.stopCh)
	a.mu.Unlock()

	// Send QUIT and close OUTSIDE the lock to avoid self-deadlock:
	// sendRaw acquires a.mu.RLock(), which deadlocks if we hold a.mu.Lock().
	if conn != nil {
		a.sendRaw("QUIT :ggcode shutting down")
		conn.Close()
	}
	return nil
}

// stopChannel returns the close-signaled channel, creating it lazily so
// adapters constructed directly (tests) still work.
func (a *twitchAdapter) stopChannel() chan struct{} {
	a.mu.Lock()
	defer a.mu.Unlock()
	if a.stopCh == nil {
		a.stopCh = make(chan struct{})
	}
	return a.stopCh
}

// ---------------------------------------------------------------------------
// Main run loop
// ---------------------------------------------------------------------------

func (a *twitchAdapter) run(ctx context.Context) {
	backoff := twitchReconnectBackoff
	for {
		if ctx.Err() != nil {
			a.publishState(false, "stopped", "")
			return
		}
		started := time.Now()
		err := a.connectAndServe(ctx)
		served := time.Since(started)
		if err != nil {
			if errors.Is(err, errTwitchAuthFailed) {
				// Token rejected by Twitch: retrying with the same dead token
				// forever is pointless — terminate with an explicit error state
				// (issue #972).
				a.publishState(false, "error", "Twitch authentication failed: OAuth token rejected (NOTICE \"Login authentication failed\"). Update extra.token / TWITCH_TOKEN and restart the adapter.")
				debug.Log("twitch", "adapter=%s terminating after authentication failure", a.name)
				return
			}
			a.publishState(false, "error", err.Error())
			debug.Log("twitch", "adapter=%s error: %v", a.name, err)
		}
		a.mu.RLock()
		isClosed := a.closed
		a.mu.RUnlock()
		if isClosed {
			return
		}
		if err == nil && ctx.Err() == nil {
			// Clean EOF without an error — surface it instead of leaving the
			// state stuck on "connected" (issue #972).
			a.publishState(false, "reconnecting", "")
		}
		select {
		case <-ctx.Done():
			a.publishState(false, "stopped", "")
			return
		case <-a.stopChannel():
			// Close() was called during the reconnect window — do not
			// reconnect (issue #972).
			return
		case <-time.After(jitterDuration(backoff)):
		}
		backoff = twitchNextBackoff(backoff, twitchReconnectBackoff, twitchMaxBackoff, served)
	}
}

// twitchNextBackoff computes the backoff for the next reconnect attempt.
// A serve cycle that lasted at least twitchBackoffResetAfter counts as a
// healthy connection, so the backoff resets to its initial value instead of
// growing forever (issue #972: backoff never reset).
func twitchNextBackoff(cur, initial, max time.Duration, served time.Duration) time.Duration {
	if served >= twitchBackoffResetAfter {
		return initial
	}
	next := cur * 2
	if next > max {
		next = max
	}
	return next
}

func (a *twitchAdapter) connectAndServe(ctx context.Context) error {
	// Issue #972: never dial after Close(). The run loop's backoff select
	// handles Close during the sleep window; this check (plus the one after
	// the dial) covers a Close racing with connection establishment.
	a.mu.RLock()
	closed := a.closed
	a.mu.RUnlock()
	if closed {
		return nil
	}

	addr := net.JoinHostPort(twitchHost, strconv.Itoa(twitchPort))
	debug.Log("twitch", "adapter=%s connecting to %s proxy=%s", a.name, addr, a.proxy)

	conn, err := a.dialFunc()(addr)
	if err != nil {
		return err
	}
	a.mu.RLock()
	closed = a.closed
	a.mu.RUnlock()
	if closed {
		// Close() ran while we were dialing — drop the fresh connection.
		conn.Close()
		return nil
	}

	a.mu.Lock()
	a.conn = conn
	a.connected = true
	a.mu.Unlock()
	a.publishState(true, "connected", "")
	debug.Log("twitch", "adapter=%s connected", a.name)

	defer func() {
		conn.Close()
		a.mu.Lock()
		a.conn = nil
		a.connected = false
		a.mu.Unlock()
	}()

	// Register
	a.sendRaw(fmt.Sprintf("PASS %s", a.token))
	a.sendRaw(fmt.Sprintf("NICK %s", a.nick))
	// Request Twitch-specific tags
	a.sendRaw("CAP REQ :twitch.tv/tags twitch.tv/commands")

	return a.serveIRC(ctx, conn)
}

// serveIRC runs the keepalive and read loops on an established connection.
// It returns when the connection drops, ctx is cancelled, or an auth-failure
// NOTICE arrives (errTwitchAuthFailed).
func (a *twitchAdapter) serveIRC(ctx context.Context, conn net.Conn) error {
	// Read loop
	scanner := bufio.NewScanner(conn)
	scanner.Buffer(make([]byte, 0, 4096), 512*1024)

	var lastPongNs atomic.Int64
	lastPongNs.Store(time.Now().UnixNano())

	// Keepalive: send periodic PING and detect dead connections.
	// scanner.Scan() blocks indefinitely when the server goes silent,
	// so the timeout must run in a separate goroutine.
	keepAliveDone := make(chan struct{})
	safego.Go("im.twitch.keepalive", func() {
		ticker := time.NewTicker(twitchPingInterval)
		defer ticker.Stop()
		for {
			select {
			case <-keepAliveDone:
				return
			case <-ticker.C:
				if time.Since(time.Unix(0, lastPongNs.Load())) > twitchPingInterval+twitchPongTimeout {
					debug.Log("twitch", "adapter=%s pong timeout, closing connection", a.name)
					conn.Close()
					return
				}
				a.sendRaw("PING :ggcode")
			}
		}
	})
	defer close(keepAliveDone)

	for scanner.Scan() {
		if ctx.Err() != nil {
			return nil
		}
		line := strings.TrimRight(scanner.Text(), "\r\n")
		if line == "" {
			continue
		}

		// Parse Twitch IRC message (may have @tags prefix)
		tags := make(map[string]string)
		ircLine := line

		if strings.HasPrefix(ircLine, "@") {
			tagEnd := strings.Index(ircLine, " ")
			if tagEnd > 0 {
				tags = parseTwitchTags(ircLine[1:tagEnd])
				ircLine = ircLine[tagEnd+1:]
			}
		}

		msg := parseIRCLine(ircLine)
		if msg == nil {
			continue
		}

		if a.handleIRCLine(ctx, msg, tags, &lastPongNs) {
			return errTwitchAuthFailed
		}
	}

	// Connection closed (by keepalive goroutine on timeout, or by server)

	if err := scanner.Err(); err != nil {
		return fmt.Errorf("read: %w", err)
	}
	return nil
}

// handleIRCLine dispatches a parsed IRC message. It reports whether the
// connection must be torn down because Twitch rejected our credentials
// (issue #972).
func (a *twitchAdapter) handleIRCLine(ctx context.Context, msg *ircMessage, tags map[string]string, lastPongNs *atomic.Int64) bool {
	switch msg.Command {
	case "PING":
		a.sendRaw(fmt.Sprintf("PONG :%s", msg.Trailing))
		lastPongNs.Store(time.Now().UnixNano())
	case "PONG":
		lastPongNs.Store(time.Now().UnixNano())
	case "001": // RPL_WELCOME
		debug.Log("twitch", "adapter=%s logged in as %s", a.name, a.nick)
		for _, ch := range a.channels {
			ch = strings.TrimSpace(ch)
			if ch != "" {
				a.sendRaw(fmt.Sprintf("JOIN %s", ch))
				debug.Log("twitch", "adapter=%s joining %s", a.name, ch)
			}
		}
	case "NOTICE":
		if isTwitchAuthFailureNOTICE(msg) {
			debug.Log("twitch", "adapter=%s credentials rejected by server (NOTICE)", a.name)
			return true
		}
		// Other NOTICEs (rate limits, whispers, moderation) are logged so
		// they are no longer silently dropped (issue #972).
		debug.Log("twitch", "adapter=%s NOTICE: %s", a.name, msg.Trailing)
	case "PRIVMSG":
		a.handlePRIVMSG(ctx, msg, tags)
	case "WHISPER":
		// Twitch delivers DMs as WHISPER commands (we CAP REQ'd
		// twitch.tv/commands), NOT as PRIVMSG addressed to our nick — the
		// isDM branch inside handlePRIVMSG was dead code on Twitch and every
		// inbound DM was silently dropped here, pairing included (#1247).
		// A WHISPER's Params[0] is OUR nick (no # prefix), so routing it
		// through handlePRIVMSG activates the DM path: channelID=senderNick,
		// no channel mention gating.
		debug.Log("twitch", "adapter=%s inbound WHISPER from %s", a.name, msg.Prefix)
		a.handlePRIVMSG(ctx, msg, tags)
	}
	return false
}

// ---------------------------------------------------------------------------
// Twitch tag parsing
// ---------------------------------------------------------------------------

func parseTwitchTags(tagStr string) map[string]string {
	tags := make(map[string]string)
	for _, pair := range strings.Split(tagStr, ";") {
		idx := strings.Index(pair, "=")
		if idx < 0 {
			continue
		}
		key := pair[:idx]
		val := pair[idx+1:]
		tags[key] = unescapeTwitchTag(val)
	}
	return tags
}

func unescapeTwitchTag(val string) string {
	// Twitch escapes: \s → space, \n → newline, \\ → backslash, \: → semicolon.
	// Must be a single left-to-right pass: sequential ReplaceAll calls
	// re-process earlier substitutions, so the literal sequence `\\s`
	// (escaped backslash followed by plain 's') was corrupted into `\ `
	// because the \s replacement ran first (issue #972).
	if !strings.Contains(val, "\\") {
		return val
	}
	var b strings.Builder
	b.Grow(len(val))
	for i := 0; i < len(val); i++ {
		c := val[i]
		if c != '\\' || i+1 >= len(val) {
			b.WriteByte(c)
			continue
		}
		i++
		switch val[i] {
		case 's':
			b.WriteByte(' ')
		case 'n':
			b.WriteByte('\n')
		case ':':
			b.WriteByte(';')
		case '\\':
			b.WriteByte('\\')
		default:
			// Unknown escape: keep the backslash and the character literally.
			b.WriteByte('\\')
			b.WriteByte(val[i])
		}
	}
	return b.String()
}

// isTwitchAuthFailureNOTICE reports whether a NOTICE indicates our OAuth
// token was rejected (issue #972: these were silently dropped and the run
// loop reconnected forever with a dead token).
func isTwitchAuthFailureNOTICE(msg *ircMessage) bool {
	if msg == nil {
		return false
	}
	text := strings.ToLower(msg.Trailing)
	return strings.Contains(text, "login authentication failed") ||
		strings.Contains(text, "improperly formatted auth")
}

// ---------------------------------------------------------------------------
// Message handling
// ---------------------------------------------------------------------------

func (a *twitchAdapter) handlePRIVMSG(ctx context.Context, msg *ircMessage, tags map[string]string) {
	senderNick, _, _ := parseIRCPrefix(msg.Prefix)
	if senderNick == "" {
		return
	}
	if len(msg.Params) == 0 {
		return
	}
	target := msg.Params[0] // #channel
	text := msg.Trailing

	// Get display name from tags
	displayName := tags["display-name"]
	if displayName == "" {
		displayName = senderNick
	}

	// User ID from tags
	userID := tags["user-id"]

	if strings.TrimSpace(text) == "" {
		return
	}

	// Ignore self
	a.mu.RLock()
	currentNick := a.nick
	a.mu.RUnlock()
	if senderNick == currentNick {
		return
	}

	// Determine channel
	channelID := target
	isDM := !strings.HasPrefix(target, "#")
	if isDM {
		// Twitch whisper (DM)
		channelID = senderNick
	}

	// Mention gating for channels
	if !isDM {
		if !strings.Contains(strings.ToLower(text), "@"+currentNick) && !strings.Contains(text, currentNick) {
			return
		}
		text = stripTwitchMention(text, currentNick)
		if text == "" {
			return
		}
	}

	ircMsg := InboundMessage{
		Envelope: Envelope{
			Adapter:    a.name,
			Platform:   PlatformTwitch,
			ChannelID:  channelID,
			SenderID:   firstNonEmpty(userID, senderNick),
			SenderName: displayName,
			MessageID:  tags["id"],
			ReceivedAt: time.Now(),
		},
		Text: strings.TrimSpace(text),
	}

	if a.handlePairing(ctx, ircMsg, channelID) {
		return
	}

	if a.manager != nil {
		a.manager.HandleInbound(ctx, ircMsg)
	}
}

// handlePairing runs the pairing flow for an inbound message and reports
// whether the message was consumed (the caller must not forward it further).
func (a *twitchAdapter) handlePairing(ctx context.Context, ircMsg InboundMessage, channelID string) bool {
	if a.manager == nil {
		return false
	}
	pairingResult, err := a.manager.HandlePairingInbound(ircMsg)
	debug.Log("twitch", "adapter=%s pairing: consumed=%v bound=%v err=%v", a.name, pairingResult.Consumed, pairingResult.Bound, err)
	if err != nil && err != ErrNoSessionBound {
		// #1248: message-level errors do not flip the adapter state - connected
		// is only published once on IRC login and nothing re-publishes it
		// while the connection lives, so one transient pairing hiccup pinned a
		// healthy adapter at warning forever. Log only (#1238/#1243 family).
		debug.Log("twitch", "adapter=%s pairing error (IRC unaffected): %v", a.name, err)
	}
	if !pairingResult.Consumed {
		return false
	}
	if err := a.sendTwitchMessage(ctx, channelID, pairingResult.ReplyText); err != nil {
		// DM replies go through Helix; make failures visible instead of
		// silently dropping the pairing reply (issue #972).
		debug.Log("twitch", "adapter=%s pairing reply to %s failed: %v", a.name, channelID, err)
	}
	if err := a.manager.NotifyPreviousBindingReplaced(ctx, pairingResult); err != nil {
		// #1248: same message-level downgrade as above.
		debug.Log("twitch", "adapter=%s notify previous binding failed: %v", a.name, err)
	}
	return true
}

func stripTwitchMention(text, nick string) string {
	text = strings.ReplaceAll(text, "@"+nick, "")
	text = strings.ReplaceAll(text, nick, "")
	return strings.Join(strings.Fields(text), " ")
}

// ---------------------------------------------------------------------------
// Outbound
// ---------------------------------------------------------------------------

func (a *twitchAdapter) Send(ctx context.Context, binding ChannelBinding, event OutboundEvent) error {
	target := binding.ChannelID
	if target == "" {
		target = binding.TargetID
	}
	return a.sendTwitchMessage(ctx, target, stripMarkdown(defaultOutboundText(event)))
}

func (a *twitchAdapter) sendTwitchMessage(ctx context.Context, target, text string) error {
	if text == "" || target == "" {
		return nil
	}
	if !strings.HasPrefix(target, "#") {
		// Twitch chat IRC (a modified RFC1459) has no whisper channel: a
		// PRIVMSG addressed to a nick is rejected/ignored by the server, so DM
		// replies used to be lost 100% of the time with no error (issue #972).
		// DMs go through the Helix API instead.
		return a.sendWhisper(ctx, target, text)
	}
	// Split by newlines first — IRC uses CRLF as message delimiter, so
	// embedded \n would prematurely terminate the PRIVMSG. Each line is
	// sent as a separate PRIVMSG, matching the standard IRC pattern.
	// The delay applies between ALL messages (not just chunks within a line),
	// so a multi-line message doesn't burst all lines at once.
	sent := false
	for _, line := range strings.Split(text, "\n") {
		line = strings.TrimRight(line, "\r")
		if strings.TrimSpace(line) == "" {
			continue
		}
		chunks := splitIRCMessage(line, twitchMaxMessageLen)
		for _, chunk := range chunks {
			if sent {
				select {
				case <-time.After(twitchInterMessageDelay):
				case <-ctx.Done():
					return ctx.Err()
				}
			}
			if err := a.sendRaw(fmt.Sprintf("PRIVMSG %s :%s", target, chunk)); err != nil {
				return fmt.Errorf("send to %s: %w", target, err)
			}
			sent = true
		}
	}
	return nil
}

func (a *twitchAdapter) TriggerTyping(ctx context.Context, binding ChannelBinding) error {
	return nil // Twitch has no typing indicator via IRC
}

// ---------------------------------------------------------------------------
// Connection dialing
// ---------------------------------------------------------------------------

// dialFunc returns the connection establishment function, allowing tests to
// inject a fake transport. The default implements the proxy/TLS logic.
func (a *twitchAdapter) dialFunc() func(addr string) (net.Conn, error) {
	a.mu.RLock()
	fn := a.dialIRC
	a.mu.RUnlock()
	if fn != nil {
		return fn
	}
	return a.defaultDialIRC
}

func (a *twitchAdapter) defaultDialIRC(addr string) (net.Conn, error) {
	if a.proxy != "" {
		conn, err := proxyDial(a.proxy, addr)
		if err != nil {
			return nil, fmt.Errorf("proxy connect: %w", err)
		}
		tlsConn := tls.Client(conn, &tls.Config{ServerName: twitchHost})
		if err := tlsConn.Handshake(); err != nil {
			conn.Close()
			return nil, fmt.Errorf("tls handshake: %w", err)
		}
		return tlsConn, nil
	}
	conn, err := tls.DialWithDialer(&net.Dialer{Timeout: 15 * time.Second}, "tcp", addr, &tls.Config{})
	if err != nil {
		return nil, fmt.Errorf("connect: %w", err)
	}
	return conn, nil
}

// ---------------------------------------------------------------------------
// DMs via the Helix API (whispers)
// ---------------------------------------------------------------------------

// sendWhisper delivers a DM through the Helix API. target is the recipient's
// login (nick). Failures are returned to the caller (who logs them) — they
// must NOT flip the adapter state: Helix hiccups are independent of the IRC
// connection, and connected is only re-published after a reconnect, so a
// warning was sticky forever (#1248; visibility retained from #972 via the
// returned error + debug logs).
func (a *twitchAdapter) sendWhisper(ctx context.Context, recipientLogin, text string) error {
	recipientLogin = strings.TrimPrefix(strings.TrimSpace(recipientLogin), "#")
	ids, err := a.helixResolveUserIDs(ctx, a.nick, recipientLogin)
	if err != nil {
		debug.Log("twitch", "adapter=%s DM via Helix failed (resolve user): %v", a.name, err)
		return fmt.Errorf("whisper resolve %s: %w", recipientLogin, err)
	}
	fromID, okFrom := ids[strings.ToLower(a.nick)]
	toID, okTo := ids[strings.ToLower(recipientLogin)]
	if !okFrom || !okTo {
		err := fmt.Errorf("could not resolve Twitch user id for whisper (self=%v recipient=%v)", okFrom, okTo)
		debug.Log("twitch", "adapter=%s DM via Helix failed: %v", a.name, err)
		return err
	}

	sent := false
	for _, line := range strings.Split(text, "\n") {
		line = strings.TrimRight(line, "\r")
		if strings.TrimSpace(line) == "" {
			continue
		}
		for _, chunk := range splitIRCMessage(line, twitchMaxMessageLen) {
			if sent {
				select {
				case <-time.After(twitchInterMessageDelay):
				case <-ctx.Done():
					return ctx.Err()
				}
			}
			if err := a.helixSendWhisper(ctx, fromID, toID, chunk); err != nil {
				debug.Log("twitch", "adapter=%s DM via Helix failed: %v", a.name, err)
				return fmt.Errorf("whisper to %s: %w", recipientLogin, err)
			}
			sent = true
		}
	}
	return nil
}

// helixResolveUserIDs maps logins to Twitch user ids via
// GET /helix/users?login=...&login=..., with a small in-memory cache.
func (a *twitchAdapter) helixResolveUserIDs(ctx context.Context, logins ...string) (map[string]string, error) {
	out := make(map[string]string, len(logins))
	var need []string
	a.userIDsMu.Lock()
	if a.userIDs == nil {
		a.userIDs = make(map[string]string)
	}
	for _, l := range logins {
		l = strings.ToLower(strings.TrimSpace(l))
		if l == "" {
			continue
		}
		if id, ok := a.userIDs[l]; ok {
			out[l] = id
			continue
		}
		need = append(need, l)
	}
	a.userIDsMu.Unlock()
	if len(need) == 0 {
		return out, nil
	}

	q := url.Values{}
	for _, l := range need {
		q.Add("login", l)
	}
	body, err := a.helixRequest(ctx, http.MethodGet, "/helix/users?"+q.Encode(), nil)
	if err != nil {
		return out, err
	}
	var resp struct {
		Data []struct {
			ID    string `json:"id"`
			Login string `json:"login"`
		} `json:"data"`
	}
	if err := json.Unmarshal(body, &resp); err != nil {
		return out, fmt.Errorf("decode helix users response: %w", err)
	}
	a.userIDsMu.Lock()
	for _, u := range resp.Data {
		login := strings.ToLower(u.Login)
		a.userIDs[login] = u.ID
		out[login] = u.ID
	}
	a.userIDsMu.Unlock()
	return out, nil
}

// helixSendWhisper posts a single whisper:
// POST /helix/messages?user_id=<sender> with {"recipient_id","message"}.
func (a *twitchAdapter) helixSendWhisper(ctx context.Context, fromID, toID, message string) error {
	payload, err := json.Marshal(map[string]string{
		"recipient_id": toID,
		"message":      message,
	})
	if err != nil {
		return err
	}
	_, err = a.helixRequest(ctx, http.MethodPost, "/helix/messages?user_id="+url.QueryEscape(fromID), payload)
	return err
}

func (a *twitchAdapter) helixRequest(ctx context.Context, method, path string, body []byte) ([]byte, error) {
	base := a.helixBaseURL
	if base == "" {
		base = twitchHelixBaseURL
	}
	var rdr io.Reader
	if body != nil {
		rdr = bytes.NewReader(body)
	}
	req, err := http.NewRequestWithContext(ctx, method, base+path, rdr)
	if err != nil {
		return nil, err
	}
	req.Header.Set("Authorization", "Bearer "+strings.TrimPrefix(a.token, "oauth:"))
	if a.clientID != "" {
		req.Header.Set("Client-Id", a.clientID)
	}
	if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	client := a.helixHTTPClient()
	resp, err := client.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	data, err := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
	if err != nil {
		return nil, err
	}
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		msg := strings.TrimSpace(string(data))
		var apiErr struct {
			Message string `json:"message"`
		}
		if json.Unmarshal(data, &apiErr) == nil && apiErr.Message != "" {
			msg = apiErr.Message
		}
		return nil, fmt.Errorf("helix %s %s: HTTP %d: %s", method, path, resp.StatusCode, msg)
	}
	return data, nil
}

func (a *twitchAdapter) helixHTTPClient() *http.Client {
	a.mu.RLock()
	c := a.httpClient
	a.mu.RUnlock()
	if c != nil {
		return c
	}
	return twitchDefaultHelixClient
}

func (a *twitchAdapter) sendRaw(line string) error {
	a.mu.RLock()
	c := a.conn
	a.mu.RUnlock()
	if c == nil {
		return fmt.Errorf("not connected")
	}
	// #1249: serialize writes — read-loop PONGs, keepalive PINGs, Sends and
	// the Close QUIT all race here; an interleaved write corrupts the IRC
	// line and the server drops the connection.
	a.writeMu.Lock()
	defer a.writeMu.Unlock()
	_, err := fmt.Fprintf(c, "%s\r\n", line)
	return err
}

// ---------------------------------------------------------------------------
// State
// ---------------------------------------------------------------------------

func (a *twitchAdapter) publishState(healthy bool, status, lastErr string) {
	if a.manager == nil {
		return
	}
	a.manager.PublishAdapterState(AdapterState{
		Name:      a.name,
		Platform:  PlatformTwitch,
		Healthy:   healthy,
		Status:    status,
		LastError: lastErr,
		UpdatedAt: time.Now(),
	})
}
