package im

import (
	"bufio"
	"context"
	"crypto/tls"
	"fmt"
	"net"
	"os"
	"strconv"
	"strings"
	"sync"
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
)

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

	return &twitchAdapter{
		name:     name,
		manager:  mgr,
		token:    token,
		nick:     nick,
		channels: channels,
		proxy:    proxy,
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
	a.closed = true
	conn := a.conn
	a.connected = false
	a.mu.Unlock()

	// Send QUIT and close OUTSIDE the lock to avoid self-deadlock:
	// sendRaw acquires a.mu.RLock(), which deadlocks if we hold a.mu.Lock().
	if conn != nil {
		a.sendRaw("QUIT :ggcode shutting down")
		conn.Close()
	}
	return nil
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
		if err := a.connectAndServe(ctx); err != nil {
			a.publishState(false, "error", err.Error())
			debug.Log("twitch", "adapter=%s error: %v", a.name, err)
		}
		a.mu.RLock()
		isClosed := a.closed
		a.mu.RUnlock()
		if isClosed {
			return
		}
		select {
		case <-ctx.Done():
			a.publishState(false, "stopped", "")
			return
		case <-time.After(jitterDuration(backoff)):
		}
		if backoff < twitchMaxBackoff {
			backoff *= 2
			if backoff > twitchMaxBackoff {
				backoff = twitchMaxBackoff
			}
		}
	}
}

func (a *twitchAdapter) connectAndServe(ctx context.Context) error {
	addr := net.JoinHostPort(twitchHost, strconv.Itoa(twitchPort))
	debug.Log("twitch", "adapter=%s connecting to %s proxy=%s", a.name, addr, a.proxy)

	var conn net.Conn
	var err error
	if a.proxy != "" {
		conn, err = proxyDial(a.proxy, addr)
		if err != nil {
			return fmt.Errorf("proxy connect: %w", err)
		}
		tlsConn := tls.Client(conn, &tls.Config{ServerName: twitchHost})
		if err := tlsConn.Handshake(); err != nil {
			conn.Close()
			return fmt.Errorf("tls handshake: %w", err)
		}
		conn = tlsConn
	} else {
		conn, err = tls.DialWithDialer(&net.Dialer{Timeout: 15 * time.Second}, "tcp", addr, &tls.Config{})
		if err != nil {
			return fmt.Errorf("connect: %w", err)
		}
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

	// Read loop
	scanner := bufio.NewScanner(conn)
	scanner.Buffer(make([]byte, 0, 4096), 512*1024)

	lastPong := time.Now()

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

		switch msg.Command {
		case "PING":
			a.sendRaw(fmt.Sprintf("PONG :%s", msg.Trailing))
			lastPong = time.Now()
		case "PONG":
			lastPong = time.Now()
		case "001": // RPL_WELCOME
			debug.Log("twitch", "adapter=%s logged in as %s", a.name, a.nick)
			for _, ch := range a.channels {
				ch = strings.TrimSpace(ch)
				if ch != "" {
					a.sendRaw(fmt.Sprintf("JOIN %s", ch))
					debug.Log("twitch", "adapter=%s joining %s", a.name, ch)
				}
			}
		case "PRIVMSG":
			a.handlePRIVMSG(ctx, msg, tags)
		}

		if time.Since(lastPong) > twitchPingInterval+twitchPongTimeout {
			return fmt.Errorf("pong timeout")
		}
	}

	if err := scanner.Err(); err != nil {
		return fmt.Errorf("read: %w", err)
	}
	return nil
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
	// Twitch escapes: \s → space, \n → newline, \\ → backslash, \: → semicolon
	val = strings.ReplaceAll(val, "\\s", " ")
	val = strings.ReplaceAll(val, "\\n", "\n")
	val = strings.ReplaceAll(val, "\\\\", "\\")
	val = strings.ReplaceAll(val, "\\:", ";")
	return val
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

	// Pairing flow
	if a.manager != nil {
		pairingResult, err := a.manager.HandlePairingInbound(ircMsg)
		debug.Log("twitch", "adapter=%s pairing: consumed=%v bound=%v err=%v", a.name, pairingResult.Consumed, pairingResult.Bound, err)
		if err != nil && err != ErrNoSessionBound {
			a.publishState(false, "warning", err.Error())
		}
		if pairingResult.Consumed {
			_ = a.sendTwitchMessage(ctx, channelID, pairingResult.ReplyText)
			if err := a.manager.NotifyPreviousBindingReplaced(ctx, pairingResult); err != nil {
				a.publishState(false, "warning", err.Error())
			}
			return
		}
	}

	if a.manager != nil {
		a.manager.HandleInbound(ctx, ircMsg)
	}
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

func (a *twitchAdapter) sendRaw(line string) error {
	a.mu.RLock()
	c := a.conn
	a.mu.RUnlock()
	if c == nil {
		return fmt.Errorf("not connected")
	}
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
