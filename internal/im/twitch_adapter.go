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
	"unicode"

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

type twitchAdapter struct {
	// #1565 case B: PRIVMSG pacing is per-CONNECTION, not per-call: the
	// old local `sent` flags paced only within one Send while concurrent
	// Sends (fanOutSend runs one goroutine per target) interleaved their
	// first messages freely - Twitch's 20-msg/30s limit is global per
	// user across channels, and over-limit PRIVMSGs are dropped by the
	// server silently. This pair serializes the wait-then-send step.
	privMsgMu     sync.Mutex
	lastPrivMsgAt time.Time
	name          string
	manager       *Manager

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

			a.publishState(false, "reconnecting", "")
		}
		select {
		case <-ctx.Done():
			a.publishState(false, "stopped", "")
			return
		case <-a.stopChannel():

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

		conn.Close()
		return nil
	}

	a.mu.Lock()

	if a.closed {
		a.mu.Unlock()
		conn.Close()
		return nil
	}
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

	a.sendRaw(fmt.Sprintf("PASS %s", a.token))
	a.sendRaw(fmt.Sprintf("NICK %s", a.nick))

	a.sendRaw("CAP REQ :twitch.tv/tags twitch.tv/commands")

	return a.serveIRC(ctx, conn)
}

// serveIRC runs the keepalive and read loops on an established connection.
// It returns when the connection drops, ctx is cancelled, or an auth-failure
// NOTICE arrives (errTwitchAuthFailed).
func (a *twitchAdapter) serveIRC(ctx context.Context, conn net.Conn) error {

	scanner := bufio.NewScanner(conn)
	scanner.Buffer(make([]byte, 0, 4096), 512*1024)

	var lastPongNs atomic.Int64
	lastPongNs.Store(time.Now().UnixNano())

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
	case "001":
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

		debug.Log("twitch", "adapter=%s NOTICE: %s", a.name, msg.Trailing)
	case "PRIVMSG":
		a.handlePRIVMSG(ctx, msg, tags)
	case "WHISPER":

		debug.Log("twitch", "adapter=%s inbound WHISPER from %s", a.name, msg.Prefix)
		a.handlePRIVMSG(ctx, msg, tags)
	}
	return false
}

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

func (a *twitchAdapter) handlePRIVMSG(ctx context.Context, msg *ircMessage, tags map[string]string) {
	senderNick, _, _ := parseIRCPrefix(msg.Prefix)
	if senderNick == "" {
		return
	}
	if len(msg.Params) == 0 {
		return
	}
	target := msg.Params[0]
	text := msg.Trailing

	displayName := tags["display-name"]
	if displayName == "" {
		displayName = senderNick
	}

	userID := tags["user-id"]

	if strings.TrimSpace(text) == "" {
		return
	}

	a.mu.RLock()
	currentNick := a.nick
	a.mu.RUnlock()
	if senderNick == currentNick {
		return
	}

	channelID := target
	isDM := !strings.HasPrefix(target, "#")
	if isDM {

		channelID = senderNick
	}

	if !isDM {

		if !twitchNickMentioned(text, currentNick) {
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

		debug.Log("twitch", "adapter=%s pairing error (IRC unaffected): %v", a.name, err)
	}
	if !pairingResult.Consumed {
		return false
	}
	if err := a.sendTwitchMessage(ctx, channelID, pairingResult.ReplyText); err != nil {

		debug.Log("twitch", "adapter=%s pairing reply to %s failed: %v", a.name, channelID, err)
	}
	if err := a.manager.NotifyPreviousBindingReplaced(ctx, pairingResult); err != nil {

		debug.Log("twitch", "adapter=%s notify previous binding failed: %v", a.name, err)
	}
	return true
}

// twitchNickMentioned reports whether text mentions currentNick as a
// whole TOKEN, case-insensitively.
// #1565: the old second condition was a bare substring Contains -
// "ggcodex is broken" mentioned nick "ggcode" (substring INSIDE a word),
// so messages that never addressed the bot were forwarded, and
// stripTwitchMention then amputated the substring mid-word, corrupting
// the text the agent received.
func twitchNickMentioned(text, currentNick string) bool {
	if currentNick == "" {
		return false
	}
	ln := strings.ToLower(currentNick)
	for _, tok := range strings.FieldsFunc(strings.ToLower(text), func(r rune) bool {
		return !unicode.IsLetter(r) && !unicode.IsDigit(r) && r != '_'
	}) {
		if tok == ln {
			return true
		}
	}
	return false
}

func stripTwitchMention(text, nick string) string {
	if nick == "" {
		return strings.Join(strings.Fields(text), " ")
	}

	ln := strings.ToLower(nick)
	kept := make([]string, 0, len(strings.Fields(text)))
	for _, word := range strings.Fields(text) {

		drop := false
		for _, tok := range strings.FieldsFunc(strings.ToLower(word), func(r rune) bool {
			return !unicode.IsLetter(r) && !unicode.IsDigit(r) && r != '_'
		}) {
			if tok == ln {
				drop = true
				break
			}
		}
		if !drop {
			kept = append(kept, word)
		}
	}
	return strings.Join(kept, " ")
}

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

		return a.sendWhisper(ctx, target, text)
	}

	for _, line := range strings.Split(text, "\n") {
		line = strings.TrimRight(line, "\r")
		if strings.TrimSpace(line) == "" {
			continue
		}
		chunks := splitIRCMessage(line, twitchMaxMessageLen)
		for _, chunk := range chunks {

			if err := a.pacePrivMsg(ctx); err != nil {
				return err
			}
			if err := a.sendRaw(fmt.Sprintf("PRIVMSG %s :%s", target, chunk)); err != nil {
				return fmt.Errorf("send to %s: %w", target, err)
			}
		}
	}
	return nil
}

// pacePrivMsg blocks until at least twitchInterMessageDelay has passed
// since the previous PRIVMSG on THIS connection (#1565 case B): the
// server's 20/30s budget is global per user, so pacing must be shared
// across concurrent Sends, not per-call.
func (a *twitchAdapter) pacePrivMsg(ctx context.Context) error {
	a.privMsgMu.Lock()
	wait := twitchInterMessageDelay - time.Since(a.lastPrivMsgAt)
	if wait > 0 {
		a.privMsgMu.Unlock()
		select {
		case <-time.After(wait):
		case <-ctx.Done():
			return ctx.Err()
		}
		a.privMsgMu.Lock()
		wait = twitchInterMessageDelay - time.Since(a.lastPrivMsgAt)
		if wait > 0 {
			a.privMsgMu.Unlock()
			select {
			case <-time.After(wait):
			case <-ctx.Done():
				return ctx.Err()
			}
			a.privMsgMu.Lock()
		}
	}
	a.lastPrivMsgAt = time.Now()
	a.privMsgMu.Unlock()
	return nil
}

func (a *twitchAdapter) TriggerTyping(ctx context.Context, binding ChannelBinding) error {
	return nil
}

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

	a.writeMu.Lock()
	defer a.writeMu.Unlock()

	_ = c.SetWriteDeadline(time.Now().Add(10 * time.Second))
	_, err := fmt.Fprintf(c, "%s\r\n", line)
	_ = c.SetWriteDeadline(time.Time{})
	return err
}

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
