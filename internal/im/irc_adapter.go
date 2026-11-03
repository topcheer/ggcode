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
	"sync/atomic"
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
	ircHealthySession      = 60 * time.Second
	ircMaxMessageLen       = 400
	ircInterMessageDelay   = 300 * time.Millisecond
)

type ircAdapter struct {
	name    string
	manager *Manager

	host     string
	port     int
	useTLS   bool
	password string
	proxy    string

	nick     string
	nickPass string
	realName string

	channels []string

	mu           sync.RWMutex
	conn         net.Conn
	connected    bool
	closed       bool
	healthySince time.Time

	writeMu sync.Mutex
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

	if conn != nil {
		a.sendRaw("QUIT :ggcode shutting down")
		conn.Close()
	}
	return nil
}

func ircBackoffAfterDisconnect(current, healthyFor time.Duration) time.Duration {
	if healthyFor >= ircHealthySession {
		return ircReconnectBackoff
	}
	return current
}

func (a *ircAdapter) run(ctx context.Context) {
	backoff := ircReconnectBackoff
	for {
		if ctx.Err() != nil {
			a.publishState(false, "stopped", "")
			return
		}
		a.healthySince = time.Time{}
		if err := a.connectAndServe(ctx); err != nil {
			a.publishState(false, "error", err.Error())
			debug.Log("irc", "adapter=%s error: %v", a.name, err)
		}

		if !a.healthySince.IsZero() {
			backoff = ircBackoffAfterDisconnect(backoff, time.Since(a.healthySince))
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
	a.healthySince = time.Now()
	a.publishState(true, "connected", "")
	debug.Log("irc", "adapter=%s connected to %s", a.name, addr)

	defer func() {
		conn.Close()
		a.mu.Lock()
		a.conn = nil
		a.connected = false
		a.mu.Unlock()
	}()

	if a.password != "" {
		a.sendRaw(fmt.Sprintf("PASS %s", a.password))
	}
	a.sendRaw(fmt.Sprintf("NICK %s", a.nick))
	a.sendRaw(fmt.Sprintf("USER %s 0 * :%s", a.nick, a.realName))

	scanner := bufio.NewScanner(conn)
	scanner.Buffer(make([]byte, 0, 4096), 512*1024)

	var lastPongNs atomic.Int64
	lastPongNs.Store(time.Now().UnixNano())

	keepAliveDone := make(chan struct{})
	safego.Go("im.irc.keepalive", func() {
		ticker := time.NewTicker(ircPingInterval)
		defer ticker.Stop()
		for {
			select {
			case <-keepAliveDone:
				return
			case <-ticker.C:
				if time.Since(time.Unix(0, lastPongNs.Load())) > ircPingInterval+ircPongTimeout {
					debug.Log("irc", "adapter=%s pong timeout, closing connection", a.name)
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

		msg := parseIRCLine(line)
		if msg == nil {
			continue
		}

		switch msg.Command {
		case "PING":
			a.sendRaw(fmt.Sprintf("PONG :%s", msg.Trailing))
			lastPongNs.Store(time.Now().UnixNano())
		case "PONG":
			lastPongNs.Store(time.Now().UnixNano())
		case "001":
			debug.Log("irc", "adapter=%s registered as %s", a.name, a.nick)

			if a.nickPass != "" {
				a.sendRaw(fmt.Sprintf("PRIVMSG NickServ :IDENTIFY %s", a.nickPass))
			}

			for _, ch := range a.channels {
				ch = strings.TrimSpace(ch)
				if ch != "" {
					a.sendRaw(fmt.Sprintf("JOIN %s", ch))
					debug.Log("irc", "adapter=%s joining %s", a.name, ch)
				}
			}
		case "433":
			newNick := a.nick + "_"
			debug.Log("irc", "adapter=%s nick in use, trying %s", a.name, newNick)
			a.mu.Lock()
			a.nick = newNick
			a.mu.Unlock()
			a.sendRaw(fmt.Sprintf("NICK %s", newNick))
		case "PRIVMSG":
			a.handlePRIVMSG(ctx, msg)
		}

	}

	if err := scanner.Err(); err != nil {
		return fmt.Errorf("read: %w", err)
	}
	return nil
}

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

func (a *ircAdapter) handlePRIVMSG(ctx context.Context, msg *ircMessage) {
	senderNick, _, _ := parseIRCPrefix(msg.Prefix)
	if senderNick == "" {
		return
	}

	switch senderNick {
	case "NickServ", "ChanServ", "MemoServ", "OperServ", "HostServ", "BotServ", "InfoServ", "StatServ", "ALIS":
		return
	}

	if len(msg.Params) == 0 {
		return
	}
	target := msg.Params[0]
	text := msg.Trailing

	if strings.TrimSpace(text) == "" {
		return
	}

	channelID := target
	isDM := !strings.HasPrefix(target, "#") && !strings.HasPrefix(target, "&")
	if isDM {

		channelID = senderNick
	}

	a.mu.RLock()
	currentNick := a.nick
	a.mu.RUnlock()
	if strings.EqualFold(senderNick, currentNick) {
		return
	}

	if !isDM {

		if !ircTextContainsNick(text, currentNick) {
			return
		}
		text = ircRemoveNick(text, currentNick)
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

	a.writeMu.Lock()
	defer a.writeMu.Unlock()
	_, err := fmt.Fprintf(c, "%s\r\n", line)
	return err
}

func splitIRCMessage(text string, maxLen int) []string {

	return splitMessageBytes(text, maxLen, false)
}

func ircTextContainsNick(text, nick string) bool {
	if nick == "" {
		return false
	}
	return strings.Contains(strings.ToLower(text), strings.ToLower(nick))
}

func ircRemoveNick(text, nick string) string {
	if nick == "" {
		return text
	}
	var b strings.Builder
	for _, field := range strings.Fields(text) {

		core := strings.Trim(field, ".,:;!?<>\"'()[]")
		if strings.EqualFold(core, nick) {
			continue
		}
		if b.Len() > 0 {
			b.WriteByte(' ')
		}
		b.WriteString(field)
	}
	return b.String()
}

func (a *ircAdapter) TriggerTyping(ctx context.Context, binding ChannelBinding) error {
	return nil
}

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
