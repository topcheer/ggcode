package im

import (
	"context"
	"encoding/hex"
	"fmt"
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
	defer a.mu.Unlock()
	a.closed = true
	for _, r := range a.relayConns {
		r.Close()
	}
	a.relayConns = nil
	a.connected = 0
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

		if err := a.connectRelay(ctx, relayURL); err != nil {
			debug.Log("nostr", "adapter=%s relay=%s error: %v", a.name, relayURL, err)
			a.publishState(false, "error", fmt.Sprintf("%s: %v", relayURL, err))
		}

		select {
		case <-ctx.Done():
			return
		case <-time.After(backoff):
		}
		if backoff < nostrMaxBackoff {
			backoff *= 2
			if backoff > nostrMaxBackoff {
				backoff = nostrMaxBackoff
			}
		}
	}
}

func (a *nostrAdapter) connectRelay(ctx context.Context, relayURL string) error {
	debug.Log("nostr", "adapter=%s connecting to %s proxy=%s", a.name, relayURL, a.proxy)

	// If proxy is set, inject HTTPS_PROXY into the environment.
	// go-nostr/coder/websocket uses http.DefaultTransport which reads HTTPS_PROXY.
	var cleanup func()
	if a.proxy != "" {
		cleanup = setEnvTemp("HTTPS_PROXY", a.proxy)
	}
	defer func() {
		if cleanup != nil {
			cleanup()
		}
	}()

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

	// Event loop
	for {
		select {
		case <-ctx.Done():
			return nil
		case evt, ok := <-sub.Events:
			if !ok {
				return fmt.Errorf("subscription closed")
			}
			if evt != nil {
				a.handleEvent(ctx, evt)
			}
		case <-sub.EndOfStoredEvents:
			debug.Log("nostr", "adapter=%s EOSE from %s", a.name, relayURL)
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
			_ = a.sendNostrDM(evt.PubKey, pairingResult.ReplyText)
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
	return a.sendNostrDM(target, defaultOutboundText(event))
}

func (a *nostrAdapter) sendNostrDM(recipientPubKey, text string) error {
	if text == "" || recipientPubKey == "" {
		return nil
	}

	// Resolve npub → hex if needed
	recipientPubKey = resolveNostrPubkey(recipientPubKey)

	chunks := splitSignalMessage(text, nostrMaxMessageLen)
	var lastErr error
	for _, chunk := range chunks {
		// Compute shared secret for NIP-04 encryption
		sharedSecret, err := nip04.ComputeSharedSecret(recipientPubKey, a.privKey)
		if err != nil {
			lastErr = fmt.Errorf("ECDH: %w", err)
			continue
		}
		encrypted, err := nip04.Encrypt(chunk, sharedSecret)
		if err != nil {
			lastErr = fmt.Errorf("NIP-04 encrypt: %w", err)
			continue
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
			lastErr = fmt.Errorf("sign: %w", err)
			continue
		}

		// Publish to all connected relays
		a.mu.RLock()
		conns := make([]*nostr.Relay, len(a.relayConns))
		copy(conns, a.relayConns)
		a.mu.RUnlock()

		for _, relay := range conns {
			if err := relay.Publish(context.Background(), evt); err != nil {
				debug.Log("nostr", "adapter=%s publish to %s failed: %v", a.name, relay.URL, err)
			}
		}
	}
	return lastErr
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
