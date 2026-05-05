package im

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
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
	signalDefaultBaseURL    = "http://127.0.0.1:8080"
	signalRPCID             = "ggcode-signal"
	signalMaxMessageLen     = 2000
	signalConnectTimeout    = 20 * time.Second
	signalRequestTimeout    = 30 * time.Second
	signalDedupMaxSize      = 1000
	signalTypingStopMs      = 3000
	signalHealthInterval    = 30 * time.Second
	signalInitialBackoff    = 2 * time.Second
	signalBackoffMax        = 60 * time.Second
	signalMaxSentTimestamps = 100
)

// ---------------------------------------------------------------------------
// Adapter struct
// ---------------------------------------------------------------------------

type signalAdapter struct {
	name    string
	manager *Manager

	// Connection
	baseURL string
	account string // phone number like +1234567890

	// Policies
	requireMention bool
	allowedUsers   []string
	groupAllowlist []string // group IDs or ["*"] for all

	mu        sync.RWMutex
	conn      *http.Client
	connected bool
	closed    bool

	// Dedup by timestamp
	seen map[int64]time.Time

	// Echo suppression — outbound timestamps
	sentTimestamps []int64
}

func newSignalAdapter(name string, _ config.IMConfig, adapterCfg config.IMAdapterConfig, mgr *Manager) (*signalAdapter, error) {
	baseURL := strings.TrimSpace(stringValue(adapterCfg.Extra, "base_url"))
	if baseURL == "" {
		baseURL = strings.TrimSpace(os.Getenv("SIGNAL_BASE_URL"))
	}
	if baseURL == "" {
		baseURL = signalDefaultBaseURL
	}
	// Ensure http:// prefix
	if !strings.HasPrefix(baseURL, "http://") && !strings.HasPrefix(baseURL, "https://") {
		baseURL = "http://" + baseURL
	}
	baseURL = strings.TrimRight(baseURL, "/")

	account := strings.TrimSpace(stringValue(adapterCfg.Extra, "account"))
	if account == "" {
		account = strings.TrimSpace(os.Getenv("SIGNAL_ACCOUNT"))
	}
	if account == "" {
		return nil, fmt.Errorf("Signal account (phone number) is required for adapter %q (set 'account' in extra or SIGNAL_ACCOUNT env)", name)
	}

	// Mention policy — default false for DMs, configurable for groups
	requireMention := true
	if v := strings.ToLower(stringValue(adapterCfg.Extra, "require_mention")); v == "false" || v == "0" || v == "no" {
		requireMention = false
	}
	if envVal := os.Getenv("SIGNAL_REQUIRE_MENTION"); envVal != "" {
		if strings.ToLower(envVal) == "false" || envVal == "0" || strings.ToLower(envVal) == "no" {
			requireMention = false
		}
	}

	allowedUsers := parseCommaList(stringValue(adapterCfg.Extra, "allowed_users"), os.Getenv("SIGNAL_ALLOWED_USERS"))
	groupAllowlist := parseCommaList(stringValue(adapterCfg.Extra, "group_allowlist"), os.Getenv("SIGNAL_GROUP_ALLOWLIST"))

	proxy := resolveProxy(stringValue(adapterCfg.Extra, "proxy"), "SIGNAL_PROXY")
	httpClient := &http.Client{Timeout: signalRequestTimeout}
	if proxy != "" {
		proxyURL, err := url.Parse(proxy)
		if err == nil {
			httpClient.Transport = &http.Transport{Proxy: http.ProxyURL(proxyURL)}
		}
	}

	return &signalAdapter{
		name:           name,
		manager:        mgr,
		baseURL:        baseURL,
		account:        account,
		requireMention: requireMention,
		allowedUsers:   allowedUsers,
		groupAllowlist: groupAllowlist,
		conn:           httpClient,
		seen:           make(map[int64]time.Time),
	}, nil
}

func (a *signalAdapter) Name() string { return a.name }

func (a *signalAdapter) Start(ctx context.Context) {
	debug.Log("signal", "adapter=%s start", a.name)
	a.publishState(false, "connecting", "")
	safego.Go("im.signal.run", func() { a.run(ctx) })
}

func (a *signalAdapter) Close() error {
	a.mu.Lock()
	defer a.mu.Unlock()
	a.closed = true
	a.connected = false
	return nil
}

// ---------------------------------------------------------------------------
// Main run loop
// ---------------------------------------------------------------------------

func (a *signalAdapter) run(ctx context.Context) {
	backoff := signalInitialBackoff
	for {
		if ctx.Err() != nil {
			a.publishState(false, "stopped", "")
			return
		}
		if err := a.connectAndServe(ctx); err != nil {
			a.publishState(false, "error", err.Error())
			debug.Log("signal", "adapter=%s error: %v", a.name, err)
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
		case <-time.After(backoff):
		}
		if backoff < signalBackoffMax {
			backoff *= 2
			if backoff > signalBackoffMax {
				backoff = signalBackoffMax
			}
		}
	}
}

func (a *signalAdapter) connectAndServe(ctx context.Context) error {
	// Health check first
	if err := a.healthCheck(); err != nil {
		return fmt.Errorf("health check: %w", err)
	}

	a.mu.Lock()
	a.connected = true
	a.mu.Unlock()
	a.publishState(true, "connected", "")
	debug.Log("signal", "adapter=%s connected to %s (account=%s)", a.name, a.baseURL, a.account)

	defer func() {
		a.mu.Lock()
		a.connected = false
		a.mu.Unlock()
	}()

	// Start health monitor
	healthCtx, healthCancel := context.WithCancel(ctx)
	defer healthCancel()
	safego.Go("im.signal.health", func() { a.healthLoop(healthCtx) })

	// SSE loop
	return a.sseLoop(ctx)
}

// ---------------------------------------------------------------------------
// SSE (Server-Sent Events) — inbound messages
// ---------------------------------------------------------------------------

func (a *signalAdapter) sseLoop(ctx context.Context) error {
	url := a.baseURL + "/api/v1/events?account=" + a.account
	debug.Log("signal", "adapter=%s SSE connecting to %s", a.name, url)

	req, err := http.NewRequestWithContext(ctx, "GET", url, nil)
	if err != nil {
		return fmt.Errorf("SSE request: %w", err)
	}
	req.Header.Set("Accept", "text/event-stream")

	// Use a client with no timeout for SSE
	client := &http.Client{}
	resp, err := client.Do(req)
	if err != nil {
		return fmt.Errorf("SSE connect: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(io.LimitReader(resp.Body, 512))
		return fmt.Errorf("SSE status %d: %s", resp.StatusCode, string(body))
	}

	debug.Log("signal", "adapter=%s SSE stream open", a.name)

	scanner := bufio.NewScanner(resp.Body)
	// Allow large SSE data lines
	scanner.Buffer(make([]byte, 0, 64*1024), 1024*1024)

	var eventType, eventData string

	for scanner.Scan() {
		if ctx.Err() != nil {
			return nil
		}
		line := scanner.Text()

		if line == "" {
			// Blank line = flush event
			if eventData != "" {
				a.handleSSEEvent(ctx, eventType, eventData)
			}
			eventType = ""
			eventData = ""
			continue
		}

		if strings.HasPrefix(line, ":") {
			// Comment, ignore
			continue
		}

		field, value := parseSSELine(line)
		switch field {
		case "event":
			eventType = value
		case "data":
			if eventData != "" {
				eventData += "\n"
			}
			eventData += value
		}
	}

	if err := scanner.Err(); err != nil {
		return fmt.Errorf("SSE read: %w", err)
	}
	return nil
}

func parseSSELine(line string) (field, value string) {
	idx := strings.Index(line, ":")
	if idx < 0 {
		return line, ""
	}
	field = line[:idx]
	value = line[idx+1:]
	if strings.HasPrefix(value, " ") {
		value = value[1:]
	}
	return field, value
}

func (a *signalAdapter) handleSSEEvent(ctx context.Context, eventType, data string) {
	if data == "" {
		return
	}

	var envelope map[string]any
	if err := json.Unmarshal([]byte(data), &envelope); err != nil {
		debug.Log("signal", "adapter=%s invalid SSE JSON: %v", a.name, err)
		return
	}

	a.processEnvelope(ctx, envelope)
}

// ---------------------------------------------------------------------------
// Message processing
// ---------------------------------------------------------------------------

func (a *signalAdapter) processEnvelope(ctx context.Context, envelope map[string]any) {
	// Check for syncMessage (sent by this account from another device)
	syncMsg, _ := envelope["syncMessage"].(map[string]any)
	isNoteToSelf := false
	if syncMsg != nil {
		sentMsg, _ := syncMsg["sentMessage"].(map[string]any)
		if sentMsg != nil {
			dest, _ := sentMsg["destinationNumber"].(string)
			if dest != "" && dest == a.account {
				// Check echo suppression
				ts := jsonInt64(sentMsg, "timestamp")
				if ts > 0 && a.isSentTimestamp(ts) {
					a.removeSentTimestamp(ts)
					return
				}
				// Genuine Note to Self
				isNoteToSelf = true
				envelope["dataMessage"] = sentMsg
			}
		}
		if !isNoteToSelf {
			return
		}
	}

	// Extract sender
	sender, _ := envelope["sourceNumber"].(string)
	if sender == "" {
		sender, _ = envelope["source"].(string)
	}
	senderName, _ := envelope["sourceName"].(string)
	if sender == "" {
		debug.Log("signal", "adapter=%s ignoring envelope with no sender", a.name)
		return
	}

	// Self-message filtering (but allow Note to Self)
	if sender == a.account && !isNoteToSelf {
		return
	}

	// Get dataMessage (or editMessage)
	dataMessage, _ := envelope["dataMessage"].(map[string]any)
	if dataMessage == nil {
		if editMsg, _ := envelope["editMessage"].(map[string]any); editMsg != nil {
			dataMessage, _ = editMsg["dataMessage"].(map[string]any)
		}
	}
	if dataMessage == nil {
		return
	}

	// Timestamp for dedup + message ID
	ts := jsonInt64(dataMessage, "timestamp")
	if ts == 0 {
		return
	}

	// Dedup
	a.mu.Lock()
	if _, seen := a.seen[ts]; seen {
		a.mu.Unlock()
		return
	}
	a.seen[ts] = time.Now()
	if len(a.seen) > signalDedupMaxSize {
		cutoff := time.Now().Add(-5 * time.Minute)
		for k, t := range a.seen {
			if t.Before(cutoff) {
				delete(a.seen, k)
			}
		}
	}
	a.mu.Unlock()

	// Check for group
	groupInfo, _ := dataMessage["groupInfo"].(map[string]any)
	groupID, _ := groupInfo["groupId"].(string)
	isGroup := groupID != ""

	// Group allowlist check
	if isGroup {
		if len(a.groupAllowlist) == 0 {
			debug.Log("signal", "adapter=%s ignoring group message (no group_allowlist)", a.name)
			return
		}
		if !entryMatches(a.groupAllowlist, groupID) && !entryMatches(a.groupAllowlist, "*") {
			debug.Log("signal", "adapter=%s group %s not in allowlist", a.name, groupID[:min(8, len(groupID))])
			return
		}
	}

	// Allowed users check
	if len(a.allowedUsers) > 0 && !entryMatches(a.allowedUsers, sender) {
		debug.Log("signal", "adapter=%s user %s not in allowed list", a.name, sender)
		return
	}

	// Extract text
	text, _ := dataMessage["message"].(string)

	// Render mentions
	if mentions, _ := dataMessage["mentions"].([]any); len(mentions) > 0 && text != "" {
		text = renderSignalMentions(text, mentions)
	}

	// Mention gating for groups
	if isGroup && a.requireMention {
		hasMention := strings.Contains(text, a.account)
		if !hasMention {
			// Check if bot phone number mentioned without +
			if strings.Contains(text, a.account[1:]) {
				hasMention = true
			}
		}
		if !hasMention {
			return
		}
		text = stripSignalMention(text, a.account)
	}

	if strings.TrimSpace(text) == "" {
		return
	}

	// Build chat ID
	chatID := sender
	if isGroup {
		chatID = "group:" + groupID
	}

	msg := InboundMessage{
		Envelope: Envelope{
			Adapter:    a.name,
			Platform:   PlatformSignal,
			ChannelID:  chatID,
			SenderID:   sender,
			SenderName: senderName,
			MessageID:  strconv.FormatInt(ts, 10),
			ReceivedAt: time.Now(),
		},
		Text: strings.TrimSpace(text),
	}

	// Pairing flow
	if a.manager != nil {
		pairingResult, err := a.manager.HandlePairingInbound(msg)
		debug.Log("signal", "adapter=%s pairing: consumed=%v bound=%v err=%v", a.name, pairingResult.Consumed, pairingResult.Bound, err)
		if err != nil && err != ErrNoSessionBound {
			a.publishState(false, "warning", err.Error())
		}
		if pairingResult.Consumed {
			_ = a.sendText(chatID, pairingResult.ReplyText)
			return
		}
	}

	if a.manager != nil {
		a.manager.HandleInbound(ctx, msg)
	}
}

// ---------------------------------------------------------------------------
// Mention helpers
// ---------------------------------------------------------------------------

func renderSignalMentions(text string, mentions []any) string {
	// Signal uses \uFFFC (object replacement character) as mention placeholder
	// Each mention has: { start, length, [name, number, uuid] }
	// For simplicity, replace \uFFFC placeholders with @name
	for _, m := range mentions {
		mention, ok := m.(map[string]any)
		if !ok {
			continue
		}
		name, _ := mention["name"].(string)
		if name == "" {
			continue
		}
		// Replace first occurrence of \uFFFC with @name
		idx := strings.Index(text, "\ufffc")
		if idx >= 0 {
			text = text[:idx] + "@" + name + text[idx+3:]
		}
	}
	return text
}

func stripSignalMention(text, account string) string {
	// Strip @+phone
	if account != "" {
		text = strings.ReplaceAll(text, account, "")
		if len(account) > 1 && account[0] == '+' {
			text = strings.ReplaceAll(text, account[1:], "")
		}
	}
	// Clean up extra spaces
	text = strings.Join(strings.Fields(text), " ")
	return strings.TrimSpace(text)
}

// ---------------------------------------------------------------------------
// Outbound — JSON-RPC
// ---------------------------------------------------------------------------

func (a *signalAdapter) Send(ctx context.Context, binding ChannelBinding, event OutboundEvent) error {
	chatID := binding.ChannelID
	if chatID == "" {
		chatID = binding.TargetID
	}
	return a.sendText(chatID, event.Text)
}

func (a *signalAdapter) sendText(chatID, text string) error {
	if text == "" || chatID == "" {
		return nil
	}

	chunks := splitSignalMessage(text, signalMaxMessageLen)
	var lastErr error
	for _, chunk := range chunks {
		params := map[string]any{
			"account": a.account,
			"message": chunk,
		}
		if strings.HasPrefix(chatID, "group:") {
			params["groupId"] = chatID[6:]
		} else {
			params["recipient"] = []string{chatID}
		}

		result, err := a.rpc("send", params)
		if err != nil {
			lastErr = fmt.Errorf("Signal send: %w", err)
			debug.Log("signal", "adapter=%s send error to %s: %v", a.name, chatID, err)
			continue
		}
		// Track sent timestamp for echo suppression
		if ts := jsonInt64(result, "timestamp"); ts > 0 {
			a.addSentTimestamp(ts)
		}
	}
	return lastErr
}

// TriggerTyping sends a Signal typing indicator.
func (a *signalAdapter) TriggerTyping(ctx context.Context, binding ChannelBinding) error {
	chatID := strings.TrimSpace(binding.ChannelID)
	if chatID == "" {
		chatID = binding.TargetID
	}
	if chatID == "" {
		return nil
	}
	params := map[string]any{
		"account": a.account,
	}
	if strings.HasPrefix(chatID, "group:") {
		params["groupId"] = chatID[6:]
	} else {
		params["recipient"] = []string{chatID}
	}
	_, err := a.rpc("sendTyping", params)
	return err
}

func (a *signalAdapter) rpc(method string, params map[string]any) (map[string]any, error) {
	payload := map[string]any{
		"jsonrpc": "2.0",
		"id":      signalRPCID,
		"method":  method,
		"params":  params,
	}
	body, err := json.Marshal(payload)
	if err != nil {
		return nil, err
	}

	req, err := http.NewRequest("POST", a.baseURL+"/api/v1/rpc", bytes.NewReader(body))
	if err != nil {
		return nil, err
	}
	req.Header.Set("Content-Type", "application/json")

	resp, err := a.conn.Do(req)
	if err != nil {
		return nil, fmt.Errorf("RPC %s: %w", method, err)
	}
	defer resp.Body.Close()

	respBody, err := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
	if err != nil {
		return nil, fmt.Errorf("RPC %s read: %w", method, err)
	}

	if resp.StatusCode == http.StatusCreated {
		return nil, nil // signal-cli returns 201 for fire-and-forget
	}
	if resp.StatusCode >= 400 {
		return nil, fmt.Errorf("RPC %s → %d: %s", method, resp.StatusCode, string(respBody))
	}

	var result struct {
		Result map[string]any `json:"result"`
		Error  *struct {
			Code    int    `json:"code"`
			Message string `json:"message"`
		} `json:"error"`
	}
	if err := json.Unmarshal(respBody, &result); err != nil {
		return nil, fmt.Errorf("RPC %s decode: %w", method, err)
	}
	if result.Error != nil {
		return nil, fmt.Errorf("RPC %s error %d: %s", method, result.Error.Code, result.Error.Message)
	}
	return result.Result, nil
}

// ---------------------------------------------------------------------------
// Health check
// ---------------------------------------------------------------------------

func (a *signalAdapter) healthCheck() error {
	req, err := http.NewRequest("GET", a.baseURL+"/api/v1/check", nil)
	if err != nil {
		return err
	}
	resp, err := (&http.Client{Timeout: 10 * time.Second}).Do(req)
	if err != nil {
		return fmt.Errorf("health check: %w", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("health check: status %d", resp.StatusCode)
	}
	return nil
}

func (a *signalAdapter) healthLoop(ctx context.Context) {
	ticker := time.NewTicker(signalHealthInterval)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			if err := a.healthCheck(); err != nil {
				debug.Log("signal", "adapter=%s health check failed: %v", a.name, err)
				// Connection may have dropped; the SSE loop will also detect this
			}
		}
	}
}

// ---------------------------------------------------------------------------
// Echo suppression
// ---------------------------------------------------------------------------

func (a *signalAdapter) addSentTimestamp(ts int64) {
	a.mu.Lock()
	defer a.mu.Unlock()
	a.sentTimestamps = append(a.sentTimestamps, ts)
	if len(a.sentTimestamps) > signalMaxSentTimestamps {
		a.sentTimestamps = a.sentTimestamps[len(a.sentTimestamps)-signalMaxSentTimestamps:]
	}
}

func (a *signalAdapter) isSentTimestamp(ts int64) bool {
	a.mu.RLock()
	defer a.mu.RUnlock()
	for _, t := range a.sentTimestamps {
		if t == ts {
			return true
		}
	}
	return false
}

func (a *signalAdapter) removeSentTimestamp(ts int64) {
	a.mu.Lock()
	defer a.mu.Unlock()
	for i, t := range a.sentTimestamps {
		if t == ts {
			a.sentTimestamps = append(a.sentTimestamps[:i], a.sentTimestamps[i+1:]...)
			return
		}
	}
}

// ---------------------------------------------------------------------------
// Message splitting
// ---------------------------------------------------------------------------

func splitSignalMessage(text string, maxLen int) []string {
	if len(text) <= maxLen {
		return []string{text}
	}
	var chunks []string
	for len(text) > 0 {
		if len(text) <= maxLen {
			chunks = append(chunks, text)
			break
		}
		splitAt := maxLen
		if idx := strings.LastIndex(text[:maxLen], "\n"); idx > maxLen/2 {
			splitAt = idx + 1
		}
		chunks = append(chunks, text[:splitAt])
		text = text[splitAt:]
	}
	return chunks
}

// ---------------------------------------------------------------------------
// Helpers
// ---------------------------------------------------------------------------

func jsonInt64(m map[string]any, key string) int64 {
	v, ok := m[key]
	if !ok {
		return 0
	}
	switch n := v.(type) {
	case float64:
		return int64(n)
	case int64:
		return n
	case json.Number:
		i, _ := n.Int64()
		return i
	case string:
		i, _ := strconv.ParseInt(n, 10, 64)
		return i
	}
	return 0
}

func (a *signalAdapter) publishState(healthy bool, status, lastErr string) {
	if a.manager == nil {
		return
	}
	a.manager.PublishAdapterState(AdapterState{
		Name:      a.name,
		Platform:  PlatformSignal,
		Healthy:   healthy,
		Status:    status,
		LastError: lastErr,
		UpdatedAt: time.Now(),
	})
}
