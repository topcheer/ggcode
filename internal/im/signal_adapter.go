package im

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/topcheer/ggcode/internal/config"
	"github.com/topcheer/ggcode/internal/debug"
	"github.com/topcheer/ggcode/internal/safego"
	"github.com/topcheer/ggcode/internal/util"

	imagepkg "github.com/topcheer/ggcode/internal/image"
)

const (
	signalDefaultBaseURL    = "http://127.0.0.1:8080"
	signalMaxMessageLen     = 16000
	signalConnectTimeout    = 20 * time.Second
	signalRequestTimeout    = 30 * time.Second
	signalDedupMaxSize      = 1000
	signalTypingStopMs      = 3000
	signalHealthInterval    = 30 * time.Second
	signalInitialBackoff    = 2 * time.Second
	signalBackoffMax        = 60 * time.Second
	signalMaxSentTimestamps = 100

	// signalInterMessageDelay is the delay between consecutive messages.
	// signal-cli-rest-api can return HTTP 429 (rate limit) on rapid sends.
	// Source: https://github.com/AsamK/signal-cli/discussions/1513
	signalInterMessageDelay = 500 * time.Millisecond
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
	requireMention := false
	if v := strings.ToLower(stringValue(adapterCfg.Extra, "require_mention")); v == "true" || v == "1" || v == "yes" {
		requireMention = true
	}
	if envVal := os.Getenv("SIGNAL_REQUIRE_MENTION"); envVal != "" {
		if strings.ToLower(envVal) == "true" || envVal == "1" || strings.ToLower(envVal) == "yes" {
			requireMention = true
		}
	}

	allowedUsers := parseCommaList(stringValue(adapterCfg.Extra, "allowed_users"), os.Getenv("SIGNAL_ALLOWED_USERS"))
	groupAllowlist := parseCommaList(stringValue(adapterCfg.Extra, "group_allowlist"), os.Getenv("SIGNAL_GROUP_ALLOWLIST"))

	proxy := resolveProxy(stringValue(adapterCfg.Extra, "proxy"), "SIGNAL_PROXY")
	httpClient := util.NewInsecureAwareClient(signalRequestTimeout)
	if proxy != "" {
		proxyURL, err := url.Parse(proxy)
		if err == nil {
			if base, ok := httpClient.Transport.(*http.Transport); ok && base != nil {
				base.Proxy = http.ProxyURL(proxyURL)
			} else {
				httpClient.Transport = &http.Transport{Proxy: http.ProxyURL(proxyURL)}
			}
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
		startedAt := time.Now()
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
		// #432: after a HEALTHY connection (stayed up for a while), the next
		// failure is a fresh transient — reset backoff (#389 Discord pattern)
		// instead of resuming the accumulated value after hours of uptime.
		if time.Since(startedAt) >= 60*time.Second {
			backoff = signalInitialBackoff
		}
		select {
		case <-ctx.Done():
			a.publishState(false, "stopped", "")
			return
		case <-time.After(jitterDuration(backoff)):
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
// Receive loop — polling /v1/receive/{number}
// ---------------------------------------------------------------------------

func (a *signalAdapter) sseLoop(ctx context.Context) error {
	receiveURL := a.baseURL + "/v1/receive/" + url.PathEscape(a.account)
	debug.Log("signal", "adapter=%s long-polling %s", a.name, receiveURL)

	// /v1/receive/ is a long-poll endpoint — it blocks until a message
	// arrives (or times out server-side). Use a long client timeout and
	// issue requests sequentially, not on a ticker.
	client := util.NewInsecureAwareClient(300 * time.Second)

	for {
		if ctx.Err() != nil {
			return nil
		}

		body, outcome := a.receivePollOnce(ctx, client, receiveURL)
		switch outcome {
		case ssePollStop:
			return nil
		case ssePollRetry:
			if !sseBackoff(ctx) {
				return nil
			}
			continue
		}

		envelopes, ok := a.parseReceiveEnvelopes(body)
		if !ok {
			if !sseBackoff(ctx) {
				return nil
			}
			continue
		}

		debug.Log("signal", "adapter=%s received %d envelope(s)", a.name, len(envelopes))

		for _, env := range envelopes {
			a.processEnvelope(ctx, env)
		}
	}
}

// signalReceiveBackoff is the fixed backoff applied after every failed round
// of the signal receive loop (#432: prevents hot-looping against the
// signal-cli/proxy server).
const signalReceiveBackoff = 5 * time.Second

// ssePollOutcome classifies one long-poll round of the receive loop.
type ssePollOutcome int

const (
	ssePollOK    ssePollOutcome = iota // a 200/204 body is ready to parse
	ssePollRetry                       // transient failure: back off, then retry
	ssePollStop                        // ctx cancelled: stop the loop with nil
)

// sseBackoffWait blocks for d or until ctx is done. It returns false when ctx
// was cancelled while waiting — the receive loop must then stop (returning
// nil, matching its historical shutdown semantics).
func sseBackoffWait(ctx context.Context, d time.Duration) bool {
	select {
	case <-time.After(d):
		return true
	case <-ctx.Done():
		return false
	}
}

// sseBackoff applies the receive loop's fixed backoff.
func sseBackoff(ctx context.Context) bool {
	return sseBackoffWait(ctx, signalReceiveBackoff)
}

// receivePollOnce performs one long-poll round against /v1/receive/ and
// classifies the outcome. Every failure branch keeps its historical debug
// log verbatim; the caller owns the backoff/stop decision.
func (a *signalAdapter) receivePollOnce(ctx context.Context, client *http.Client, receiveURL string) ([]byte, ssePollOutcome) {
	req, err := http.NewRequestWithContext(ctx, "GET", receiveURL, nil)
	if err != nil {
		debug.Log("signal", "adapter=%s receive request error: %v", a.name, err)
		return nil, ssePollRetry
	}

	resp, err := client.Do(req)
	if err != nil {
		if ctx.Err() != nil {
			return nil, ssePollStop
		}
		debug.Log("signal", "adapter=%s receive error: %v", a.name, err)
		return nil, ssePollRetry
	}

	// #405: migrate to imagepkg.ReadLimited (missed in the #388 sweep) —
	// bare LimitReader silently truncated >2MB attachments into corrupt
	// data; the limit is also unified to the shared 20MB.
	body, err := imagepkg.ReadLimited(resp.Body, imagepkg.MaxSize)
	resp.Body.Close()
	if err != nil {
		// #432: oversized-body / read errors must back off like every
		// other error path in this loop - a bare continue hot-looped the
		// CPU and the signal-cli/proxy server when the endpoint kept
		// returning huge bodies.
		debug.Log("signal", "adapter=%s receive read error: %v", a.name, err)
		return nil, ssePollRetry
	}

	if resp.StatusCode != http.StatusOK && resp.StatusCode != http.StatusNoContent {
		debug.Log("signal", "adapter=%s receive status %d: %s", a.name, resp.StatusCode, string(body))
		return nil, ssePollRetry
	}

	return body, ssePollOK
}

// parseReceiveEnvelopes decodes one long-poll response body. A non-JSON body
// (e.g. a proxy injecting an HTML error page) is a retryable failure
// (#432/#968: it must back off like every other error path, not hot-loop).
func (a *signalAdapter) parseReceiveEnvelopes(body []byte) ([]map[string]any, bool) {
	var envelopes []map[string]any
	if err := json.Unmarshal(body, &envelopes); err != nil {
		debug.Log("signal", "adapter=%s receive parse error: %v", a.name, err)
		return nil, false
	}
	return envelopes, true
}

// ---------------------------------------------------------------------------
// Message processing
// ---------------------------------------------------------------------------

// processEnvelope converts one signal-cli-rest-api payload into an inbound
// message. Each phase lives in a small helper below: envelope unwrapping,
// sync (multi-device) classification, sender/dataMessage extraction, dedup,
// allow-listing, text rendering and final dispatch.
func (a *signalAdapter) processEnvelope(ctx context.Context, raw map[string]any) {
	rawJSON, _ := json.Marshal(raw)
	debug.Log("signal", "adapter=%s processEnvelope: %s", a.name, string(rawJSON))

	raw = unwrapSignalEnvelope(raw)

	// syncMessage envelopes (sent by this account from another device) are
	// promoted to inbound-looking messages; anything else inside a sync
	// envelope is dropped.
	isSync, isNoteToSelf, isGroupSync := a.resolveSyncEnvelope(raw)
	if isSync && !isNoteToSelf && !isGroupSync {
		return
	}

	sender, senderName := envelopeSender(raw)
	if sender == "" {
		rawJSON, _ := json.Marshal(raw)
		debug.Log("signal", "adapter=%s ignoring envelope with no sender: %s", a.name, string(rawJSON))
		return
	}

	// Self-message filtering (#968): Note to Self and group sync messages
	// legitimately originate from this account (sent from another device).
	// Track the flag so the allowed-users check can exempt them.
	selfOriginated := (isNoteToSelf || isGroupSync) && sender == a.account

	dataMessage := extractSignalDataMessage(raw)
	if dataMessage == nil {
		return
	}

	// Timestamp for dedup + message ID
	ts := jsonInt64(dataMessage, "timestamp")
	if ts == 0 {
		return
	}
	if a.seenEnvelope(ts) {
		return
	}

	// Check for group
	groupInfo, _ := dataMessage["groupInfo"].(map[string]any)
	groupID, _ := groupInfo["groupId"].(string)
	isGroup := groupID != ""

	if !a.senderAllowed(sender, selfOriginated) {
		debug.Log("signal", "adapter=%s user %s not in allowed list", a.name, sender)
		return
	}

	text, ok := a.inboundText(dataMessage, isGroup)
	if !ok {
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
	a.dispatchInbound(ctx, msg, chatID)
}

// unwrapSignalEnvelope normalizes a signal-cli-rest-api payload: the actual
// envelope may be wrapped as {"account":"+...", "envelope":{...}}, in which
// case the account-level fields are merged into the inner envelope.
func unwrapSignalEnvelope(raw map[string]any) map[string]any {
	inner, _ := raw["envelope"].(map[string]any)
	if inner == nil {
		return raw
	}
	if acct, ok := raw["account"].(string); ok {
		inner["account"] = acct
	}
	return inner
}

// resolveSyncEnvelope classifies a syncMessage envelope (a message sent by
// this account from another device). It mutates raw in place, promoting the
// synced sentMessage into an inbound-looking dataMessage with synthesized
// source fields (#968), and reports:
//   - sync: raw carries a syncMessage at all
//   - noteToSelf: genuine Note to Self aimed at this account
//   - groupSync: sync of a group message sent from another device
//
// A sync envelope that is neither (or whose echo timestamp was already seen)
// must be dropped by the caller.
func (a *signalAdapter) resolveSyncEnvelope(raw map[string]any) (sync, noteToSelf, groupSync bool) {
	syncMsg, _ := raw["syncMessage"].(map[string]any)
	if syncMsg == nil {
		return false, false, false
	}
	sync = true
	sentMsg, _ := syncMsg["sentMessage"].(map[string]any)
	if sentMsg == nil {
		return sync, false, false
	}
	dest, _ := sentMsg["destinationNumber"].(string)
	// Check if it's a group message sync
	groupInfo, _ := sentMsg["groupInfo"].(map[string]any)
	if dest != "" && dest == a.account {
		// Check echo suppression
		ts := jsonInt64(sentMsg, "timestamp")
		if ts > 0 && a.isSentTimestamp(ts) {
			a.removeSentTimestamp(ts)
			return sync, false, false
		}
		// Genuine Note to Self
		noteToSelf = true
	} else if groupInfo != nil {
		// Sync of a message sent to a group from another device; echo
		// suppression applies here too.
		ts := jsonInt64(sentMsg, "timestamp")
		if ts > 0 && a.isSentTimestamp(ts) {
			a.removeSentTimestamp(ts)
			return sync, false, false
		}
		// Not our sent message — treat as inbound group message
		groupSync = true
	} else {
		return sync, false, false
	}

	// Set source fields so sender extraction works (#968): the sync
	// envelope carries no sourceNumber/sourceName, so sender extraction
	// yielded "" and every Note to Self message was silently dropped.
	// Mirrors the group sync branch.
	if _, ok := raw["sourceNumber"]; !ok {
		raw["sourceNumber"] = a.account
	}
	if _, ok := raw["sourceName"]; !ok {
		raw["sourceName"] = "Me"
	}
	raw["dataMessage"] = sentMsg
	return sync, noteToSelf, groupSync
}

// envelopeSender extracts the sender number and display name, falling back
// from sourceNumber to the legacy source field.
func envelopeSender(raw map[string]any) (sender, senderName string) {
	sender, _ = raw["sourceNumber"].(string)
	if sender == "" {
		sender, _ = raw["source"].(string)
	}
	senderName, _ = raw["sourceName"].(string)
	return sender, senderName
}

// extractSignalDataMessage returns the message payload of an envelope:
// dataMessage first, with editMessage.dataMessage (edits of previously sent
// messages) as fallback.
func extractSignalDataMessage(raw map[string]any) map[string]any {
	dataMessage, _ := raw["dataMessage"].(map[string]any)
	if dataMessage != nil {
		return dataMessage
	}
	if editMsg, _ := raw["editMessage"].(map[string]any); editMsg != nil {
		dataMessage, _ = editMsg["dataMessage"].(map[string]any)
	}
	return dataMessage
}

// seenEnvelope reports whether the timestamp was already processed and, if
// not, records it. The seen-map is pruned to a 5 minute window once it grows
// past signalDedupMaxSize entries.
func (a *signalAdapter) seenEnvelope(ts int64) bool {
	a.mu.Lock()
	defer a.mu.Unlock()
	if _, seen := a.seen[ts]; seen {
		return true
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
	return false
}

// senderAllowed applies the allowed_users filter. Self-originated sync
// messages bypass the filter, otherwise a restricted allowed_users list
// would drop the account's own Note to Self / group sync messages (#968).
func (a *signalAdapter) senderAllowed(sender string, selfOriginated bool) bool {
	if selfOriginated || len(a.allowedUsers) == 0 {
		return true
	}
	return entryMatches(a.allowedUsers, sender)
}

// inboundText renders the final text of a message: mention placeholders are
// expanded, and for group messages with requireMention, messages without a
// mention are gated (#1018). ok=false means the envelope carries no text
// worth dispatching.
func (a *signalAdapter) inboundText(dataMessage map[string]any, isGroup bool) (text string, ok bool) {
	// Extract text
	text, _ = dataMessage["message"].(string)

	// Render mentions
	if mentions, _ := dataMessage["mentions"].([]any); len(mentions) > 0 && text != "" {
		text = renderSignalMentions(text, mentions)
	}

	// Mention gating for groups (#1018).
	if isGroup && a.requireMention {
		var drop bool
		text, drop = a.gateGroupMention(text, dataMessage)
		if drop {
			debug.Log("signal", "adapter=%s ignoring group message (no mention)", a.name)
			return "", false
		}
	}

	if strings.TrimSpace(text) == "" {
		return "", false
	}
	return text, true
}

// dispatchInbound routes a fully-built inbound message: the pairing handshake
// intercepts messages while pairing is in progress, everything else goes to
// the manager. Pairing errors never flip Healthy (#1238).
func (a *signalAdapter) dispatchInbound(ctx context.Context, msg InboundMessage, chatID string) {
	if a.manager == nil {
		return
	}
	pairingResult, err := a.manager.HandlePairingInbound(msg)
	debug.Log("signal", "adapter=%s pairing: consumed=%v bound=%v err=%v", a.name, pairingResult.Consumed, pairingResult.Bound, err)
	// #1238: pairing errors do NOT flip Healthy. The SSE loop keeps
	// delivering messages regardless, but nothing ever re-publishes
	// connected state (sseLoop retries forever without re-entering
	// connectAndServe), so a single pairing hiccup showed the channel as
	// offline in every status surface until process restart. Align with
	// nostr/whatsapp: log only.
	if err != nil && err != ErrNoSessionBound {
		debug.Log("signal", "adapter=%s pairing error (connection unaffected): %v", a.name, err)
	}
	if pairingResult.Consumed {
		// Auto-add first paired group to allowlist
		_ = a.sendText(ctx, chatID, pairingResult.ReplyText)
		if err := a.manager.NotifyPreviousBindingReplaced(ctx, pairingResult); err != nil {
			debug.Log("signal", "adapter=%s notify previous binding failed: %v", a.name, err)
		}
		return
	}
	a.manager.HandleInbound(ctx, msg)
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
	if chatID == "" {
		return nil
	}

	// Images first, then the remaining text - same ordering as the other
	// media adapters. Extraction failures degrade to a text notice instead of
	// dropping the whole reply.
	raw := defaultOutboundText(event)
	images, remainder := ExtractImagesFromText(raw)
	for _, img := range images {
		if err := a.sendExtractedImage(ctx, chatID, img); err != nil {
			debug.Log("signal", "adapter=%s image to %s failed: %v (continuing with text)", a.name, chatID, err)
			remainder = strings.TrimSpace(remainder + "\n[image failed: " + err.Error() + "]")
		}
	}
	return a.sendText(ctx, chatID, signalMarkdown(remainder))
}

// sendExtractedImage resolves one extracted image and sends it as a Signal
// attachment via /v2/send base64_attachments (signal-cli-rest-api accepts
// "data:<mime>;filename=<name>;base64,<data>" entries).
func (a *signalAdapter) sendExtractedImage(ctx context.Context, chatID string, img ExtractedImage) error {
	data, mime, filename, err := a.resolveImageBytes(ctx, img)
	if err != nil {
		return err
	}
	att := "data:" + mime + ";filename=" + filename + ";base64," + base64.StdEncoding.EncodeToString(data)

	payload := map[string]any{
		"number":             a.account,
		"message":            "",
		"base64_attachments": []string{att},
	}
	applySignalRecipient(payload, chatID)

	respBody, err := a.postSignalSend(ctx, payload)
	if err != nil {
		return err
	}
	trackSignalTimestamp(respBody, a.addSentTimestamp)
	return nil
}

// signalMIMEExt maps an image MIME type to a file extension.
func signalMIMEExt(mime string) string {
	switch {
	case strings.Contains(mime, "jpeg") || strings.Contains(mime, "jpg"):
		return ".jpg"
	case strings.Contains(mime, "gif"):
		return ".gif"
	case strings.Contains(mime, "webp"):
		return ".webp"
	default:
		return ".png"
	}
}

// signalDownloadImage fetches an http(s) image URL with bounds and
// content-type checks; returns data, mime, and a filename from the URL path.
func (a *signalAdapter) signalDownloadImage(ctx context.Context, rawURL string) ([]byte, string, string, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, rawURL, nil)
	if err != nil {
		return nil, "", "", fmt.Errorf("create request: %w", err)
	}
	resp, err := a.conn.Do(req)
	if err != nil {
		return nil, "", "", fmt.Errorf("download image: %w", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return nil, "", "", fmt.Errorf("download image: HTTP %d", resp.StatusCode)
	}
	data, err := imagepkg.ReadLimited(resp.Body, imagepkg.MaxSize)
	if err != nil {
		return nil, "", "", fmt.Errorf("read image response: %w", err)
	}
	mime := resp.Header.Get("Content-Type")
	if !strings.HasPrefix(mime, "image/") {
		mime = imagepkg.DetectMIME(data)
	}
	if !strings.HasPrefix(mime, "image/") {
		return nil, "", "", fmt.Errorf("content is not an image: %s", mime)
	}
	if u, perr := url.Parse(rawURL); perr == nil && u.Path != "" && u.Path != "/" {
		return data, mime, filepath.Base(u.Path), nil
	}
	return data, mime, "image" + signalMIMEExt(mime), nil
}

// resolveImageBytes turns an ExtractedImage into raw bytes plus a filename.
func (a *signalAdapter) resolveImageBytes(ctx context.Context, img ExtractedImage) ([]byte, string, string, error) {
	switch img.Kind {
	case "data_url":
		parts := strings.SplitN(img.Data, ",", 2)
		if len(parts) < 2 {
			return nil, "", "", fmt.Errorf("invalid data URL")
		}
		data, err := base64.StdEncoding.DecodeString(parts[1])
		if err != nil {
			return nil, "", "", fmt.Errorf("decode data URL: %w", err)
		}
		// RFC 2397 data URLs are not required to carry a ";" parameter
		// (e.g. "data:image/png,..." with URL-encoded data). Guard the slice
		// so a missing ";" falls back instead of panicking (#1018).
		mime := "image/png"
		header := parts[0]
		if idx := strings.Index(header, ";"); idx > 0 {
			header = header[:idx]
		}
		if m := strings.TrimPrefix(header, "data:"); strings.HasPrefix(m, "image/") {
			mime = m
		}
		return data, mime, "image" + signalMIMEExt(mime), nil

	case "url":
		if IsLocalFilePath(img.Data) {
			// #1739: bound the local-path read (the #1557 qq fix, all adapters).
			f, err := os.Open(img.Data)
			if err != nil {
				return nil, "", "", fmt.Errorf("read local image: %w", err)
			}
			data, err := imagepkg.ReadLimited(f, imagepkg.MaxSize)
			f.Close()
			if err != nil {
				return nil, "", "", fmt.Errorf("read local image: %w", err)
			}
			if err != nil {
				return nil, "", "", fmt.Errorf("read local image: %w", err)
			}
			decoded, err := imagepkg.Decode(data)
			if err != nil {
				return nil, "", "", fmt.Errorf("decode local image: %w", err)
			}
			return data, decoded.MIME, filepath.Base(img.Data), nil
		}
		return a.signalDownloadImage(ctx, img.Data)

	default:
		return nil, "", "", fmt.Errorf("unknown image kind: %s", img.Kind)
	}
}

// gateGroupMention enforces requireMention for group messages. It consults
// the structured mentions array first (official Signal clients render an
// "@Name" placeholder whose E.164 number only appears in mentions, so a
// text-only scan would silently drop an explicit bot mention #1018), then
// falls back to scanning the text for the account number. Returns the
// mention-stripped text and whether the message must be dropped.
func (a *signalAdapter) gateGroupMention(text string, dataMessage map[string]any) (string, bool) {
	structured := signalMentionsAccount(dataMessage["mentions"], a.account)
	hasMention := structured ||
		strings.Contains(text, a.account) ||
		(len(a.account) > 1 && strings.Contains(text, a.account[1:]))
	if !hasMention {
		return text, true
	}
	text = stripSignalMention(text, a.account)
	// stripSignalMention is number-based; a structured mention leaves a
	// rendered "@Name" placeholder whose leading "@" must go too.
	if structured {
		text = strings.TrimSpace(strings.TrimPrefix(text, "@"))
	}
	return text, false
}

// signalMentionsAccount reports whether the envelope's structured mentions
// array contains the given E.164 account number (#1018).
func signalMentionsAccount(raw any, account string) bool {
	mentions, ok := raw.([]any)
	if !ok || account == "" {
		return false
	}
	for _, m := range mentions {
		mention, ok := m.(map[string]any)
		if !ok {
			continue
		}
		if number, _ := mention["number"].(string); number == account {
			return true
		}
	}
	return false
}

func (a *signalAdapter) sendText(ctx context.Context, chatID, text string) error {
	if text == "" || chatID == "" {
		return nil
	}

	chunks := splitSignalMessage(text, signalMaxMessageLen)
	var lastErr error
	for i, chunk := range chunks {
		// Rate limit: signal-cli-rest-api returns 429 on rapid sends.
		if i > 0 {
			select {
			case <-time.After(signalInterMessageDelay):
			case <-ctx.Done():
				return ctx.Err()
			}
		}
		endpoint := "/v2/send"
		payload := map[string]any{
			"number":  a.account,
			"message": chunk,
		}
		if strings.HasPrefix(chatID, "group:") {
			// signal-cli-rest-api double-base64-encodes group IDs:
			// the groupId from receive is single-encoded, we need to encode it again
			// and prefix with "group."
			rawGroupID := chatID[6:]
			doubleEncoded := base64.StdEncoding.EncodeToString([]byte(rawGroupID))
			payload["recipients"] = []string{"group." + doubleEncoded}
		} else {
			payload["recipients"] = []string{chatID}
		}

		body, err := json.Marshal(payload)
		if err != nil {
			lastErr = fmt.Errorf("Signal send marshal: %w", err)
			continue
		}

		respBody, sendErr := a.postSignalBody(ctx, endpoint, body)
		if sendErr != nil {
			lastErr = sendErr
			debug.Log("signal", "adapter=%s send error to %s: %v", a.name, chatID, sendErr)
			continue
		}
		// Track sent timestamp for echo suppression
		trackSignalTimestamp(respBody, a.addSentTimestamp)
	}
	return lastErr
}

// applySignalRecipient fills the recipient fields of a /v2/send payload for
// the given chatID (direct number or "group:" prefixed group ID).
func applySignalRecipient(payload map[string]any, chatID string) {
	if strings.HasPrefix(chatID, "group:") {
		// signal-cli-rest-api double-base64-encodes group IDs:
		// the groupId from receive is single-encoded, we need to encode it again
		// and prefix with "group."
		rawGroupID := chatID[6:]
		doubleEncoded := base64.StdEncoding.EncodeToString([]byte(rawGroupID))
		payload["recipients"] = []string{"group." + doubleEncoded}
	} else {
		payload["recipients"] = []string{chatID}
	}
}

// trackSignalTimestamp extracts the send timestamp from a response body for
// echo suppression.
func trackSignalTimestamp(respBody []byte, add func(int64)) {
	var result struct {
		Timestamp int64 `json:"timestamp"`
	}
	if json.Unmarshal(respBody, &result) == nil && result.Timestamp > 0 {
		add(result.Timestamp)
	}
}

// postSignalSend marshals the payload and POSTs it to /v2/send.
func (a *signalAdapter) postSignalSend(ctx context.Context, payload map[string]any) ([]byte, error) {
	body, err := json.Marshal(payload)
	if err != nil {
		return nil, fmt.Errorf("Signal send marshal: %w", err)
	}
	return a.postSignalBody(ctx, "/v2/send", body)
}

// postSignalBody POSTs one request to the signal-cli-rest-api with the
// shared 429 rate-limit retry loop. Returns the response body on 2xx.
func (a *signalAdapter) postSignalBody(ctx context.Context, endpoint string, body []byte) ([]byte, error) {
	var lastErr error
	for attempt := 0; attempt <= maxRateLimitRetries; attempt++ {
		req, err := http.NewRequestWithContext(ctx, "POST", a.baseURL+endpoint, bytes.NewReader(body))
		if err != nil {
			return nil, err
		}
		req.Header.Set("Content-Type", "application/json")

		resp, err := a.conn.Do(req)
		if err != nil {
			return nil, fmt.Errorf("Signal send: %w", err)
		}

		// Handle HTTP 429 (Too Many Requests) with Retry-After backoff.
		if resp.StatusCode == http.StatusTooManyRequests {
			resp.Body.Close()
			if attempt < maxRateLimitRetries {
				delay := parseRetryAfter(resp)
				if err := sleepRetry(ctx, delay); err != nil {
					return nil, err
				}
				continue
			}
			return nil, rateLimitExhausted("Signal")
		}

		// Error-body probe: bounded read, truncation acceptable here
		// (only used for diagnostics), but go through ReadLimited for a
		// consistent pattern (#405).
		respBody, _ := imagepkg.ReadLimited(resp.Body, 4096)
		resp.Body.Close()

		if resp.StatusCode != http.StatusCreated && resp.StatusCode != http.StatusOK {
			return nil, fmt.Errorf("Signal send status %d: %s", resp.StatusCode, string(respBody))
		}
		return respBody, nil
	}
	return nil, lastErr
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
	// #1239: signal-cli-rest-api's TypingIndicatorRequest only has a
	// Recipient field — the groupId body field never existed, so group typing
	// silently 400'd (and the unchecked status code hid it). Group recipients
	// use the same "group." + double-encoded form as sendText.
	recipient := chatID
	if strings.HasPrefix(chatID, "group:") {
		rawGroupID := chatID[6:]
		recipient = "group." + base64.StdEncoding.EncodeToString([]byte(rawGroupID))
	}
	payload := map[string]any{
		"number":    a.account,
		"recipient": recipient,
	}
	body, _ := json.Marshal(payload)
	// Propagate ctx so the request cannot outlive the caller (#968).
	if ctx == nil {
		ctx = context.Background()
	}
	req, err := http.NewRequestWithContext(ctx, "PUT", a.baseURL+"/v1/typing-indicator/"+url.PathEscape(a.account), bytes.NewReader(body))
	if err != nil {
		return err
	}
	req.Header.Set("Content-Type", "application/json")
	resp, err := a.conn.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	// #1239: surface non-2xx in debug logs (the previous silent nil return
	// is exactly why the broken group payload survived — the 400 was
	// invisible).
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		debug.Log("signal", "adapter=%s typing-indicator for %s: HTTP %d", a.name, chatID, resp.StatusCode)
	}
	return nil
}

// ---------------------------------------------------------------------------
// Health check
// ---------------------------------------------------------------------------

func (a *signalAdapter) healthCheck() error {
	req, err := http.NewRequest("GET", a.baseURL+"/v1/health", nil)
	if err != nil {
		return err
	}
	resp, err := util.NewInsecureAwareClient(10 * time.Second).Do(req)
	if err != nil {
		return fmt.Errorf("health check: %w", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK && resp.StatusCode != http.StatusNoContent {
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
	return splitMessageRunes(text, maxLen, false, false, true)
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
	contactURI := ""
	if a.account != "" {
		contactURI = "https://signal.me/#p/" + a.account
	}
	a.manager.PublishAdapterState(AdapterState{
		Name:       a.name,
		Platform:   PlatformSignal,
		Healthy:    healthy,
		Status:     status,
		LastError:  lastErr,
		ContactURI: contactURI,
		UpdatedAt:  time.Now(),
	})
}

// CheckSignalDaemon pings the signal-cli REST API at the given baseURL to
// check if the daemon is running. Returns nil if reachable.
func CheckSignalDaemon(baseURL string) error {
	if baseURL == "" {
		baseURL = signalDefaultBaseURL
	}
	if !strings.HasPrefix(baseURL, "http://") && !strings.HasPrefix(baseURL, "https://") {
		baseURL = "http://" + baseURL
	}
	baseURL = strings.TrimRight(baseURL, "/")
	client := util.NewInsecureAwareClient(5 * time.Second)
	resp, err := client.Get(baseURL + "/v1/about")
	if err != nil {
		return fmt.Errorf("signal-cli daemon not reachable at %s: %w", baseURL, err)
	}
	defer resp.Body.Close()
	if resp.StatusCode >= 500 {
		return fmt.Errorf("signal-cli daemon at %s returned status %d", baseURL, resp.StatusCode)
	}
	return nil
}

// FetchSignalQRCode fetches the QR code PNG from signal-cli-rest-api for
// device linking. Returns the raw PNG bytes.
func FetchSignalQRCode(baseURL, deviceName string) ([]byte, error) {
	if baseURL == "" {
		baseURL = signalDefaultBaseURL
	}
	if !strings.HasPrefix(baseURL, "http://") && !strings.HasPrefix(baseURL, "https://") {
		baseURL = "http://" + baseURL
	}
	baseURL = strings.TrimRight(baseURL, "/")
	if deviceName == "" {
		deviceName = "ggcode"
	}
	client := util.NewInsecureAwareClient(30 * time.Second)
	resp, err := client.Get(fmt.Sprintf("%s/v1/qrcodelink?device_name=%s", baseURL, url.QueryEscape(deviceName)))
	if err != nil {
		return nil, fmt.Errorf("failed to fetch QR code: %w", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != 200 {
		body, _ := util.ReadAll(resp.Body, util.ReadLimitGeneral)
		return nil, fmt.Errorf("QR code request failed (status %d): %s", resp.StatusCode, strings.TrimSpace(string(body)))
	}
	return util.ReadAll(resp.Body, util.ReadLimitGeneral)
}

// SignalDaemonInstallCommand returns a shell command string that installs
// signal-cli-rest-api via Docker.
func SignalDaemonInstallCommand() string {
	return "docker run -d --name signal-cli-rest-api -p 8080:8080 -v signal-cli-config:/home/.local/share/signal-cli/ bbernhard/signal-cli-rest-api:latest"
}
