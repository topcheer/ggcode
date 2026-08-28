package im

import (
	"context"
	"encoding/hex"
	"errors"
	"fmt"
	"net/url"
	"os"
	"strings"
	"sync"
	"time"

	"github.com/topcheer/ggcode/internal/config"
	"github.com/topcheer/ggcode/internal/debug"
	"github.com/topcheer/ggcode/internal/safego"

	"github.com/nbd-wtf/go-nostr"
	"github.com/nbd-wtf/go-nostr/nip04"
	"github.com/nbd-wtf/go-nostr/nip19"
)

const (
	nostrMaxMessageLen    = 2000
	nostrReconnectBackoff = 5 * time.Second
	nostrMaxBackoff       = 120 * time.Second
	nostrDedupMaxSize     = 5000
	nostrStartupLookback  = 120
	// nostrInterMsgDelay is the delay between consecutive chunk sends.
	// Relays may rate-limit rapid publishes; 300ms is conservative.
	nostrInterMsgDelay = 300 * time.Millisecond

	// nostrWatchdogTimeout is the maximum idle period before forcing a reconnect.
	// Since go-nostr doesn't expose the underlying WebSocket for read deadlines,
	// we use a watchdog timer instead. Nostr events are persistent on relays,
	// so a brief reconnection gap doesn't cause data loss — the Since filter
	// on re-subscribe picks up any missed events.
	nostrWatchdogTimeout = 5 * time.Minute
)

// ---------------------------------------------------------------------------
// Adapter struct
// ---------------------------------------------------------------------------

type nostrAdapter struct {
	name    string
	manager *Manager

	// Keys (hex)
	privKey string
	pubKey  string

	// Relays
	relays []string

	// Proxy
	proxy string

	mu         sync.RWMutex
	relayConns []*nostr.Relay
	connected  int
	closed     bool

	// Dedup
	seen map[string]time.Time
}

func newNostrAdapter(name string, _ config.IMConfig, adapterCfg config.IMAdapterConfig, mgr *Manager) (*nostrAdapter, error) {
	privKey := strings.TrimSpace(stringValue(adapterCfg.Extra, "private_key"))
	if privKey == "" {
		privKey = strings.TrimSpace(os.Getenv("NOSTR_PRIVATE_KEY"))
	}
	if privKey == "" {
		return nil, fmt.Errorf("Nostr private_key is required for adapter %q (set 'private_key' in extra or NOSTR_PRIVATE_KEY env)", name)
	}

	// Decode nsec → hex if needed
	privKey = normalizeNostrKey(privKey)
	if len(privKey) != 64 {
		return nil, fmt.Errorf("Nostr private_key must be 32 bytes hex (64 chars) or nsec format")
	}

	// Verify the key is valid by deriving pubkey
	pubKey, err := nostr.GetPublicKey(privKey)
	if err != nil {
		return nil, fmt.Errorf("invalid Nostr private_key: %w", err)
	}

	relays := parseCommaList(stringValue(adapterCfg.Extra, "relays"), os.Getenv("NOSTR_RELAYS"))
	if len(relays) == 0 {
		relays = []string{
			"wss://relay.damus.io",
			"wss://nos.lol",
			"wss://relay.nostr.band",
		}
	}

	proxy := resolveProxy(stringValue(adapterCfg.Extra, "proxy"), "NOSTR_PROXY")

	return &nostrAdapter{
		name:    name,
		manager: mgr,
		privKey: privKey,
		pubKey:  pubKey,
		relays:  relays,
		proxy:   proxy,
		seen:    make(map[string]time.Time),
	}, nil
}

func (a *nostrAdapter) Name() string { return a.name }

func (a *nostrAdapter) Start(ctx context.Context) {
	debug.Log("nostr", "adapter=%s start pubkey=%s relays=%v", a.name, a.pubKey[:12], a.relays)
	a.publishState(false, "connecting", "")
	safego.Go("im.nostr.run", func() { a.run(ctx) })
}

func (a *nostrAdapter) Close() error {
	a.mu.Lock()
	a.closed = true
	relays := a.relayConns
	a.relayConns = nil
	a.connected = 0
	a.mu.Unlock()
	// Close outside the lock to avoid potential deadlock.
	for _, r := range relays {
		r.Close()
	}
	return nil
}

// ---------------------------------------------------------------------------
// Main run loop
// ---------------------------------------------------------------------------

func (a *nostrAdapter) run(ctx context.Context) {
	for _, relayURL := range a.relays {
		safego.Go("im.nostr.relay."+relayURL, func() { a.relayLoop(ctx, relayURL) })
	}
	<-ctx.Done()
	a.publishState(false, "stopped", "")
}

func (a *nostrAdapter) relayLoop(ctx context.Context, relayURL string) {
	backoff := nostrReconnectBackoff
	for {
		if ctx.Err() != nil {
			return
		}
		a.mu.RLock()
		isClosed := a.closed
		a.mu.RUnlock()
		if isClosed {
			return
		}

		err := a.connectRelay(ctx, relayURL)
		if err != nil {
			debug.Log("nostr", "adapter=%s relay=%s error: %v", a.name, relayURL, err)
			a.publishState(false, "error", fmt.Sprintf("%s: %v", relayURL, err))
		}

		select {
		case <-ctx.Done():
			return
		case <-time.After(jitterDuration(backoff)):
		}
		backoff = nostrBackoffNext(backoff, err)
	}
}

// errNostrServed marks a relay session that ended AFTER a successful connect
// and subscribe (watchdog timeout, subscription closed by the relay). The
// relay was healthy, so the reconnect backoff must reset instead of doubling
// (#964 — same lesson as the mattermost adapter, #388/#389).
var errNostrServed = errors.New("nostr: relay session served before disconnect")

// nostrBackoffNext returns the backoff for the next reconnect attempt after a
// relay session ended with err. Clean/serve-phase exits reset to the short
// initial delay; connect-phase failures double up to the cap (#964).
func nostrBackoffNext(backoff time.Duration, err error) time.Duration {
	if err == nil || errors.Is(err, errNostrServed) {
		return nostrReconnectBackoff
	}
	if backoff < nostrMaxBackoff {
		backoff *= 2
		if backoff > nostrMaxBackoff {
			backoff = nostrMaxBackoff
		}
	}
	return backoff
}

func (a *nostrAdapter) connectRelay(ctx context.Context, relayURL string) error {
	debug.Log("nostr", "adapter=%s connecting to %s proxy=%s", a.name, relayURL, a.proxy)

	// #758: the old HTTPS_PROXY env hack never worked reliably -- net/http
	// caches the env-proxy func in a sync.Once at first use, so the temp
	// value was either ignored (cache primed by earlier HTTP traffic) or
	// permanently captured into the process-wide transport. Route this
	// relay's host through the per-host transport interceptor instead:
	// deterministic for the relay, untouched for all other traffic.
	relayHost := ""
	if a.proxy != "" {
		if u, err := url.Parse(relayURL); err == nil && u.Host != "" {
			relayHost = u.Host
			if err := RegisterHostProxy(relayHost, a.proxy); err != nil {
				return fmt.Errorf("connect %s: %w", relayURL, err)
			}
			defer UnregisterHostProxy(relayHost)
		} else if err != nil {
			return fmt.Errorf("connect %s: invalid relay URL: %w", relayURL, err)
		}
	}

	relay, err := nostr.RelayConnect(ctx, relayURL)
	if err != nil {
		return fmt.Errorf("connect %s: %w", relayURL, err)
	}

	a.mu.Lock()
	a.relayConns = append(a.relayConns, relay)
	a.connected++
	a.mu.Unlock()
	a.publishState(true, "connected", "")
	debug.Log("nostr", "adapter=%s connected to %s", a.name, relayURL)

	defer func() {
		relay.Close()
		a.mu.Lock()
		for i, r := range a.relayConns {
			if r == relay {
				a.relayConns = append(a.relayConns[:i], a.relayConns[i+1:]...)
				break
			}
		}
		a.connected--
		a.mu.Unlock()
	}()

	// Subscribe to DMs (kind 4) with p-tag = our pubkey
	since := nostr.Now() - nostrStartupLookback
	filter := nostr.Filter{
		Kinds: []int{nostr.KindEncryptedDirectMessage},
		Tags:  nostr.TagMap{"p": []string{a.pubKey}},
		Since: &since,
		Limit: 100,
	}

	sub, err := relay.Subscribe(ctx, nostr.Filters{filter})
	if err != nil {
		return fmt.Errorf("subscribe: %w", err)
	}
	debug.Log("nostr", "adapter=%s subscribed to DMs on %s", a.name, relayURL)

	// Event loop with watchdog timer for dead connection detection.
	// Since go-nostr doesn't expose the WebSocket for read deadlines, we use
	// a periodic watchdog. Nostr relays persist events, so reconnection with
	// a Since filter recovers any missed events.
	watchdog := time.NewTimer(nostrWatchdogTimeout)
	defer watchdog.Stop()

	for {
		select {
		case <-ctx.Done():
			return nil
		case evt, ok := <-sub.Events:
			if !ok {
				// Relay closed a previously-healthy subscription — serve-phase
				// exit, tagged so relayLoop resets the backoff (#964).
				return fmt.Errorf("subscription closed: %w", errNostrServed)
			}
			if evt != nil {
				a.handleEvent(ctx, evt)
			}
			// Reset watchdog on any activity
			watchdog.Reset(nostrWatchdogTimeout)
		case <-sub.EndOfStoredEvents:
			debug.Log("nostr", "adapter=%s EOSE from %s", a.name, relayURL)
			watchdog.Reset(nostrWatchdogTimeout)
		case <-watchdog.C:
			debug.Log("nostr", "adapter=%s watchdog timeout on %s, forcing reconnect", a.name, relayURL)
			// Serve-phase exit from a healthy connection — tagged for backoff
			// reset (#964).
			return fmt.Errorf("watchdog: no activity within %s: %w", nostrWatchdogTimeout, errNostrServed)
		}
	}
}

// ---------------------------------------------------------------------------
// Event handling
// ---------------------------------------------------------------------------

func (a *nostrAdapter) handleEvent(ctx context.Context, evt *nostr.Event) {
	if evt.ID == "" || evt.Kind != nostr.KindEncryptedDirectMessage {
		return
	}

	// Dedup
	a.mu.Lock()
	if _, seen := a.seen[evt.ID]; seen {
		a.mu.Unlock()
		return
	}
	a.seen[evt.ID] = time.Now()
	if len(a.seen) > nostrDedupMaxSize {
		cutoff := time.Now().Add(-10 * time.Minute)
		for k, t := range a.seen {
			if t.Before(cutoff) {
				delete(a.seen, k)
			}
		}
	}
	a.mu.Unlock()

	// Ignore our own events
	if evt.PubKey == a.pubKey {
		return
	}

	// Decrypt NIP-04 content
	sharedSecret, err := nip04.ComputeSharedSecret(evt.PubKey, a.privKey)
	if err != nil {
		debug.Log("nostr", "adapter=%s ECDH failed for event %s: %v", a.name, evt.ID[:12], err)
		return
	}
	plaintext, err := nip04.Decrypt(evt.Content, sharedSecret)
	if err != nil {
		debug.Log("nostr", "adapter=%s decrypt failed for event %s: %v", a.name, evt.ID[:12], err)
		return
	}

	if strings.TrimSpace(plaintext) == "" {
		return
	}

	msg := InboundMessage{
		Envelope: Envelope{
			Adapter:    a.name,
			Platform:   PlatformNostr,
			ChannelID:  evt.PubKey,
			SenderID:   evt.PubKey,
			SenderName: evt.PubKey[:12],
			MessageID:  evt.ID,
			ReceivedAt: evt.CreatedAt.Time(),
		},
		Text: strings.TrimSpace(plaintext),
	}

	// Pairing flow
	if a.manager != nil {
		pairingResult, err := a.manager.HandlePairingInbound(msg)
		debug.Log("nostr", "adapter=%s pairing: consumed=%v bound=%v err=%v", a.name, pairingResult.Consumed, pairingResult.Bound, err)
		if pairingResult.Consumed {
			_ = a.sendNostrDM(ctx, evt.PubKey, pairingResult.ReplyText)
			if err := a.manager.NotifyPreviousBindingReplaced(ctx, pairingResult); err != nil {
				debug.Log("nostr", "adapter=%s notify previous: %v", a.name, err)
			}
			return
		}
	}

	if a.manager != nil {
		a.manager.HandleInbound(ctx, msg)
	}
}

// ---------------------------------------------------------------------------
// Outbound
// ---------------------------------------------------------------------------

func (a *nostrAdapter) Send(ctx context.Context, binding ChannelBinding, event OutboundEvent) error {
	target := binding.ChannelID
	if target == "" {
		target = binding.TargetID
	}
	return a.sendNostrDM(ctx, target, stripMarkdown(defaultOutboundText(event)))
}

func (a *nostrAdapter) sendNostrDM(ctx context.Context, recipientPubKey, text string) error {
	if text == "" || recipientPubKey == "" {
		return nil
	}

	// Resolve npub → hex if needed
	recipientPubKey = resolveNostrPubkey(recipientPubKey)

	chunks := splitNostrMessage(text, nostrMaxMessageLen)

	// Compute shared secret once — it only depends on recipientPubKey and our
	// private key, both constant across all chunks. ECDH scalar multiplication
	// is expensive (~0.1ms), so this avoids N redundant computations.
	sharedSecret, err := nip04.ComputeSharedSecret(recipientPubKey, a.privKey)
	if err != nil {
		return fmt.Errorf("ECDH: %w", err)
	}

	var hardErr error
	for i, chunk := range chunks {
		encrypted, err := nip04.Encrypt(chunk, sharedSecret)
		if err != nil {
			// Encryption error is also recipient-level
			hardErr = fmt.Errorf("NIP-04 encrypt: %w", err)
			break
		}

		// Build and sign event
		evt := nostr.Event{
			PubKey:    a.pubKey,
			CreatedAt: nostr.Now(),
			Kind:      nostr.KindEncryptedDirectMessage,
			Tags:      nostr.Tags{{"p", recipientPubKey}},
			Content:   encrypted,
		}
		if err := evt.Sign(a.privKey); err != nil {
			hardErr = fmt.Errorf("sign: %w", err)
			break // signing key error: will fail for every chunk
		}

		// Publish to all connected relays. If every relay is down (conns empty)
		// we must return an error, NOT nil — the caller (sendWithTimeout)
		// treats nil as delivered and skips its retry loop, silently dropping
		// the message (#964).
		a.mu.RLock()
		conns := make([]*nostr.Relay, len(a.relayConns))
		copy(conns, a.relayConns)
		a.mu.RUnlock()
		if len(conns) == 0 {
			hardErr = fmt.Errorf("no relay connections")
			break
		}

		success := 0
		var relayErr error
		for _, relay := range conns {
			if err := relay.Publish(ctx, evt); err != nil {
				debug.Log("nostr", "adapter=%s publish to %s failed: %v", a.name, relay.URL, err)
				relayErr = fmt.Errorf("publish to %s: %w", relay.URL, err)
				continue
			}
			success++
		}
		// #1225: at least one relay accepted this chunk - treat it as
		// delivered. Surfacing a partial failure as an error makes the caller
		// resend the whole message, and re-encryption (random IV) produces a
		// new event ID the recipient's dedupe cannot catch: the user receives
		// duplicate DMs. Only a zero-success chunk (or no connections at
		// all) errors. The error is STICKY: a later delivered chunk must
		// never erase an earlier chunk's total failure - that chunk is
		// undelivered data, and returning nil would silently drop it (the
		// #964 class the original patch regressed for multi-chunk sends).
		hardErr = nostrFoldChunk(hardErr, success, relayErr)
		// Inter-chunk delay to avoid relay rate-limiting (skip after last chunk).
		if i < len(chunks)-1 {
			select {
			case <-time.After(nostrInterMsgDelay):
			case <-ctx.Done():
				return ctx.Err()
			}
		}
	}
	return hardErr
}

// nostrFoldChunk folds one chunk's publish outcome into the send error
// state. A zero-success chunk sets a sticky error (the chunk is undelivered
// data, #964); a partial-success chunk counts as delivered and never clears
// an error recorded by an earlier chunk (#1225: the previous per-chunk
// `lastErr = nil` erasure silently dropped fully-failed chunks in
// multi-chunk sends).
func nostrFoldChunk(hardErr error, success int, relayErr error) error {
	if success == 0 && relayErr != nil {
		return relayErr
	}
	return hardErr
}

func (a *nostrAdapter) TriggerTyping(ctx context.Context, binding ChannelBinding) error {
	return nil
}

// ---------------------------------------------------------------------------
// Key helpers
// ---------------------------------------------------------------------------

func normalizeNostrKey(key string) string {
	key = strings.TrimSpace(key)
	if strings.HasPrefix(key, "nsec1") {
		_, value, err := nip19.Decode(key)
		if err != nil {
			return key
		}
		if sk, ok := value.(string); ok {
			return sk
		}
	}
	return strings.ToLower(key)
}

func resolveNostrPubkey(input string) string {
	input = strings.TrimSpace(input)
	if strings.HasPrefix(input, "npub1") {
		_, value, err := nip19.Decode(input)
		if err != nil {
			return input
		}
		if pk, ok := value.(string); ok {
			return pk
		}
	}
	// Verify it's valid hex
	if _, err := hex.DecodeString(input); err == nil && len(input) == 64 {
		return input
	}
	return input
}

// splitNostrMessage splits text into chunks fitting within maxLen runes.
// Uses balanced breaking: prefers newline boundaries, then hard cut.
func splitNostrMessage(text string, maxLen int) []string {
	return splitMessageRunes(text, maxLen, false, false, true)
}

// ---------------------------------------------------------------------------
// State
// ---------------------------------------------------------------------------

func (a *nostrAdapter) publishState(healthy bool, status, lastErr string) {
	if a.manager == nil {
		return
	}
	a.manager.PublishAdapterState(AdapterState{
		Name:      a.name,
		Platform:  PlatformNostr,
		Healthy:   healthy,
		Status:    status,
		LastError: lastErr,
		UpdatedAt: time.Now(),
	})
}
