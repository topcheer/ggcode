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
	ircDefaultHost         = "irc.libera.chat"
	ircDefaultPort         = 6697
	ircDefaultTLS          = true
	ircPingInterval        = 60 * time.Second
	ircPongTimeout         = 30 * time.Second
	ircReconnectBackoff    = 5 * time.Second
	ircMaxReconnectBackoff = 120 * time.Second
	ircMaxMessageLen       = 400
	// ircInterMessageDelay is the delay between consecutive PRIVMSG lines.
	// IRC servers enforce flood protection; 300ms is conservative.
	// Source: RFC 2812 §2.3.1, common IRC server flood protection policies.
	ircInterMessageDelay = 300 * time.Millisecond
)

// ---------------------------------------------------------------------------
// Adapter struct
// ---------------------------------------------------------------------------

type ircAdapter struct {
	name    string
	manager *Manager

	// Connection
	host     string
	port     int
	useTLS   bool
	password string
	proxy    string // HTTP/SOCKS5 proxy URL

	// Identity
	nick     string
	nickPass string
	realName string

	// Channels to join
	channels []string

	mu        sync.RWMutex
	conn      net.Conn
	connected bool
	closed    bool
}

func newIRCAdapter(name string, _ config.IMConfig, adapterCfg config.IMAdapterConfig, mgr *Manager) (*ircAdapter, error) {
	host := strings.TrimSpace(stringValue(adapterCfg.Extra, "host"))
	if host == "" {
		host = strings.TrimSpace(os.Getenv("IRC_HOST"))
	}
	if host == "" {
		host = ircDefaultHost
	}

	port := ircDefaultPort
	if v := stringValue(adapterCfg.Extra, "port"); v != "" {
		if p, err := strconv.Atoi(v); err == nil && p > 0 && p < 65536 {
			port = p
		}
	}

	useTLS := ircDefaultTLS
	if v := strings.ToLower(stringValue(adapterCfg.Extra, "tls")); v == "false" || v == "0" || v == "no" {
		useTLS = false
	}

	password := strings.TrimSpace(stringValue(adapterCfg.Extra, "password"))
	if password == "" {
		password = strings.TrimSpace(os.Getenv("IRC_PASSWORD"))
	}

	nick := strings.TrimSpace(stringValue(adapterCfg.Extra, "nick"))
	if nick == "" {
		nick = strings.TrimSpace(os.Getenv("IRC_NICK"))
	}
	if nick == "" {
		return nil, fmt.Errorf("IRC nick is required for adapter %q (set 'nick' in extra or IRC_NICK env)", name)
	}

	nickPass := strings.TrimSpace(stringValue(adapterCfg.Extra, "nick_password"))
	if nickPass == "" {
		nickPass = strings.TrimSpace(os.Getenv("IRC_NICK_PASSWORD"))
	}

	realName := strings.TrimSpace(stringValue(adapterCfg.Extra, "real_name"))
	if realName == "" {
		realName = nick
	}

	channels := parseCommaList(stringValue(adapterCfg.Extra, "channels"), os.Getenv("IRC_CHANNELS"))

	proxy := resolveProxy(stringValue(adapterCfg.Extra, "proxy"), "IRC_PROXY")

	return &ircAdapter{
		name:     name,
		manager:  mgr,
		host:     host,
		port:     port,
		useTLS:   useTLS,
		password: password,
		nick:     nick,
		nickPass: nickPass,
		realName: realName,
		channels: channels,
		proxy:    proxy,
	}, nil
}

func (a *ircAdapter) Name() string { return a.name }

func (a *ircAdapter) Start(ctx context.Context) {
	debug.Log("irc", "adapter=%s start", a.name)
	a.publishState(false, "connecting", "")
	safego.Go("im.irc.run", func() { a.run(ctx) })
}

func (a *ircAdapter) Close() error {
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

func (a *ircAdapter) run(ctx context.Context) {
	backoff := ircReconnectBackoff
	for {
		if ctx.Err() != nil {
			a.publishState(false, "stopped", "")
			return
		}
		if err := a.connectAndServe(ctx); err != nil {
			a.publishState(false, "error", err.Error())
			debug.Log("irc", "adapter=%s error: %v", a.name, err)
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
		if backoff < ircMaxReconnectBackoff {
			backoff *= 2
			if backoff > ircMaxReconnectBackoff {
				backoff = ircMaxReconnectBackoff
			}
		}
	}
}

func (a *ircAdapter) connectAndServe(ctx context.Context) error {
	addr := net.JoinHostPort(a.host, strconv.Itoa(a.port))
	debug.Log("irc", "adapter=%s connecting to %s (TLS=%v proxy=%s)", a.name, addr, a.useTLS, a.proxy)

	var conn net.Conn
	var err error
	if a.proxy != "" {
		conn, err = proxyDial(a.proxy, addr)
		if err != nil {
			return fmt.Errorf("proxy connect: %w", err)
		}
		if a.useTLS {
			tlsConn := tls.Client(conn, &tls.Config{ServerName: a.host})
			if err := tlsConn.Handshake(); err != nil {
				conn.Close()
				return fmt.Errorf("tls handshake: %w", err)
			}
			conn = tlsConn
		}
	} else {
		if a.useTLS {
			conn, err = tls.DialWithDialer(&net.Dialer{Timeout: 15 * time.Second}, "tcp", addr, &tls.Config{})
		} else {
			conn, err = net.DialTimeout("tcp", addr, 15*time.Second)
		}
	}
	if err != nil {
		return fmt.Errorf("connect: %w", err)
	}

	a.mu.Lock()
	a.conn = conn
	a.connected = true
	a.mu.Unlock()
	a.publishState(true, "connected", "")
	debug.Log("irc", "adapter=%s connected to %s", a.name, addr)

	defer func() {
		conn.Close()
		a.mu.Lock()
		a.conn = nil
		a.connected = false
		a.mu.Unlock()
	}()

	// Register
	if a.password != "" {
		a.sendRaw(fmt.Sprintf("PASS %s", a.password))
	}
	a.sendRaw(fmt.Sprintf("NICK %s", a.nick))
	a.sendRaw(fmt.Sprintf("USER %s 0 * :%s", a.nick, a.realName))

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

		msg := parseIRCLine(line)
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
			debug.Log("irc", "adapter=%s registered as %s", a.name, a.nick)
			// NickServ identify
			if a.nickPass != "" {
				a.sendRaw(fmt.Sprintf("PRIVMSG NickServ :IDENTIFY %s", a.nickPass))
			}
			// Join channels
			for _, ch := range a.channels {
				ch = strings.TrimSpace(ch)
				if ch != "" {
					a.sendRaw(fmt.Sprintf("JOIN %s", ch))
					debug.Log("irc", "adapter=%s joining %s", a.name, ch)
				}
			}
		case "433": // NICK in use
			newNick := a.nick + "_"
			debug.Log("irc", "adapter=%s nick in use, trying %s", a.name, newNick)
			a.mu.Lock()
			a.nick = newNick
			a.mu.Unlock()
			a.sendRaw(fmt.Sprintf("NICK %s", newNick))
		case "PRIVMSG":
			a.handlePRIVMSG(ctx, msg)
		}

		// Pong timeout
		if time.Since(lastPong) > ircPingInterval+ircPongTimeout {
			return fmt.Errorf("pong timeout")
		}
	}

	if err := scanner.Err(); err != nil {
		return fmt.Errorf("read: %w", err)
	}
	return nil
}

// ---------------------------------------------------------------------------
// IRC message parsing
// ---------------------------------------------------------------------------

type ircMessage struct {
	Prefix   string
	Command  string
	Params   []string
	Trailing string
	Raw      string
}

func parseIRCLine(line string) *ircMessage {
	line = strings.TrimRight(line, "\r\n")
	if line == "" {
		return nil
	}

	cursor := line
	var prefix string
	if strings.HasPrefix(cursor, ":") {
		idx := strings.Index(cursor, " ")
		if idx <= 1 {
			return nil
		}
		prefix = cursor[1:idx]
		cursor = cursor[idx+1:]
	}

	cursor = strings.TrimLeft(cursor, " ")
	if cursor == "" {
		return nil
	}

	spaceIdx := strings.Index(cursor, " ")
	var command string
	if spaceIdx == -1 {
		command = strings.ToUpper(cursor)
		cursor = ""
	} else {
		command = strings.ToUpper(cursor[:spaceIdx])
		cursor = cursor[spaceIdx+1:]
	}

	var params []string
	var trailing string
	for len(cursor) > 0 {
		cursor = strings.TrimLeft(cursor, " ")
		if cursor == "" {
			break
		}
		if strings.HasPrefix(cursor, ":") {
			trailing = cursor[1:]
			break
		}
		spaceIdx = strings.Index(cursor, " ")
		if spaceIdx == -1 {
			params = append(params, cursor)
			break
		}
		params = append(params, cursor[:spaceIdx])
		cursor = cursor[spaceIdx+1:]
	}

	return &ircMessage{
		Prefix:   prefix,
		Command:  command,
		Params:   params,
		Trailing: trailing,
		Raw:      line,
	}
}

func parseIRCPrefix(prefix string) (nick, user, host string) {
	// nick!user@host or server.name
	bangIdx := strings.Index(prefix, "!")
	if bangIdx < 0 {
		return prefix, "", ""
	}
	nick = prefix[:bangIdx]
	rest := prefix[bangIdx+1:]
	atIdx := strings.Index(rest, "@")
	if atIdx < 0 {
		return nick, rest, ""
	}
	return nick, rest[:atIdx], rest[atIdx+1:]
}

// ---------------------------------------------------------------------------
// Message handling
// ---------------------------------------------------------------------------

func (a *ircAdapter) handlePRIVMSG(ctx context.Context, msg *ircMessage) {
	senderNick, _, _ := parseIRCPrefix(msg.Prefix)
	if senderNick == "" {
		return
	}

	// Ignore messages from IRC services
	switch senderNick {
	case "NickServ", "ChanServ", "MemoServ", "OperServ", "HostServ", "BotServ", "InfoServ", "StatServ", "ALIS":
		return
	}

	// Target is first param (channel or our nick for DM)
	if len(msg.Params) == 0 {
		return
	}
	target := msg.Params[0]
	text := msg.Trailing

	if strings.TrimSpace(text) == "" {
		return
	}

	// Determine if it's a channel or DM
	channelID := target
	isDM := !strings.HasPrefix(target, "#") && !strings.HasPrefix(target, "&")
	if isDM {
		// DM — target is our nick, sender is the user
		channelID = senderNick
	}

	// Ignore messages from ourselves
	a.mu.RLock()
	currentNick := a.nick
	a.mu.RUnlock()
	if senderNick == currentNick {
		return
	}

	// Mention gating for channels (not DMs)
	if !isDM {
		// Check if we were mentioned
		if !strings.Contains(text, currentNick) {
			return
		}
		text = strings.ReplaceAll(text, currentNick, "")
		text = strings.Join(strings.Fields(text), " ")
		if text == "" {
			return
		}
	}

	ircMsg := InboundMessage{
		Envelope: Envelope{
			Adapter:    a.name,
			Platform:   PlatformIRC,
			ChannelID:  channelID,
			SenderID:   senderNick,
			SenderName: senderNick,
			MessageID:  fmt.Sprintf("%d", time.Now().UnixNano()),
			ReceivedAt: time.Now(),
		},
		Text: strings.TrimSpace(text),
	}

	// Pairing flow
	if a.manager != nil {
		pairingResult, err := a.manager.HandlePairingInbound(ircMsg)
		debug.Log("irc", "adapter=%s pairing: consumed=%v bound=%v err=%v", a.name, pairingResult.Consumed, pairingResult.Bound, err)
		if err != nil && err != ErrNoSessionBound {
			a.publishState(false, "warning", err.Error())
		}
		if pairingResult.Consumed {
			_ = a.sendIRCMessage(ctx, channelID, pairingResult.ReplyText)
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

// ---------------------------------------------------------------------------
// Outbound
// ---------------------------------------------------------------------------

func (a *ircAdapter) Send(ctx context.Context, binding ChannelBinding, event OutboundEvent) error {
	target := binding.ChannelID
	if target == "" {
		target = binding.TargetID
	}
	text := stripMarkdown(defaultOutboundText(event))
	return a.sendIRCMessage(ctx, target, text)
}

func (a *ircAdapter) sendIRCMessage(ctx context.Context, target, text string) error {
	if text == "" || target == "" {
		return nil
	}
	// Split by newlines first — each line is a separate PRIVMSG.
	// The delay applies between ALL messages (not just chunks within a line),
	// so a multi-line message doesn't burst all lines at once.
	sent := false
	for _, line := range strings.Split(text, "\n") {
		line = strings.TrimRight(line, "\r")
		if strings.TrimSpace(line) == "" {
			continue
		}
		chunks := splitIRCMessage(line, ircMaxMessageLen)
		for _, chunk := range chunks {
			if sent {
				select {
				case <-time.After(ircInterMessageDelay):
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

func (a *ircAdapter) sendRaw(line string) error {
	a.mu.RLock()
	c := a.conn
	a.mu.RUnlock()
	if c == nil {
		return fmt.Errorf("not connected")
	}
	_, err := fmt.Fprintf(c, "%s\r\n", line)
	return err
}

func splitIRCMessage(text string, maxLen int) []string {
	return splitMessageRunes(text, maxLen, false, true, true)
}

// TriggerTyping — IRC has no typing indicator.
func (a *ircAdapter) TriggerTyping(ctx context.Context, binding ChannelBinding) error {
	return nil
}

// ---------------------------------------------------------------------------
// Helpers
// ---------------------------------------------------------------------------

func (a *ircAdapter) publishState(healthy bool, status, lastErr string) {
	if a.manager == nil {
		return
	}
	a.manager.PublishAdapterState(AdapterState{
		Name:      a.name,
		Platform:  PlatformIRC,
		Healthy:   healthy,
		Status:    status,
		LastError: lastErr,
		UpdatedAt: time.Now(),
	})
}
