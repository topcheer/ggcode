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
	nostrInterMsgDelay    = 300 * time.Millisecond

	nostrWatchdogTimeout = 5 * time.Minute
)

type nostrAdapter struct {
	name    string
	manager *Manager

	privKey string
	pubKey  string

	relays []string

	proxy string

	mu         sync.RWMutex
	relayConns []*nostr.Relay
	connected  int
	closed     bool

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

	privKey = normalizeNostrKey(privKey)
	if len(privKey) != 64 {
		return nil, fmt.Errorf("Nostr private_key must be 32 bytes hex (64 chars) or nsec format")
	}

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
	debug.Log("nostr", "adapter=%s start pubkey=%s relays=%v", a.name, nostrShortID(a.pubKey), a.relays)
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

	for _, r := range relays {
		r.Close()
	}
	return nil
}

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

var errNostrServed = errors.New("nostr: relay session served before disconnect")

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

	a.mu.Lock()
	closed := a.closed
	a.mu.Unlock()
	if closed {
		return fmt.Errorf("connect %s: adapter closed", relayURL)
	}

	relay, err := nostr.RelayConnect(ctx, relayURL)
	if err != nil {
		return fmt.Errorf("connect %s: %w", relayURL, err)
	}

	a.mu.Lock()

	if a.closed {
		a.mu.Unlock()
		relay.Close()
		return fmt.Errorf("connect %s: adapter closed during dial", relayURL)
	}
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

	watchdog := time.NewTimer(nostrWatchdogTimeout)
	defer watchdog.Stop()

	for {
		select {
		case <-ctx.Done():
			return nil
		case evt, ok := <-sub.Events:
			if !ok {

				return fmt.Errorf("subscription closed: %w", errNostrServed)
			}
			if evt != nil {

				evt := evt
				safego.Go(fmt.Sprintf("nostr-inbound-%s", a.name), func() {
					a.handleEvent(ctx, evt)
				})
			}

			watchdog.Reset(nostrWatchdogTimeout)
		case <-sub.EndOfStoredEvents:
			debug.Log("nostr", "adapter=%s EOSE from %s", a.name, relayURL)
			watchdog.Reset(nostrWatchdogTimeout)
		case <-watchdog.C:
			debug.Log("nostr", "adapter=%s watchdog timeout on %s, forcing reconnect", a.name, relayURL)

			return fmt.Errorf("watchdog: no activity within %s: %w", nostrWatchdogTimeout, errNostrServed)
		}
	}
}

func nostrShortID(s string) string {
	if len(s) > 12 {
		return s[:12]
	}
	return s
}

func (a *nostrAdapter) handleEvent(ctx context.Context, evt *nostr.Event) {
	if evt.ID == "" || evt.Kind != nostr.KindEncryptedDirectMessage {
		return
	}

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

	if evt.PubKey == a.pubKey {
		return
	}

	sharedSecret, err := nip04.ComputeSharedSecret(evt.PubKey, a.privKey)
	if err != nil {
		debug.Log("nostr", "adapter=%s ECDH failed for event %s: %v", a.name, nostrShortID(evt.ID), err)
		return
	}
	plaintext, err := nip04.Decrypt(evt.Content, sharedSecret)
	if err != nil {
		debug.Log("nostr", "adapter=%s decrypt failed for event %s: %v", a.name, nostrShortID(evt.ID), err)
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
			SenderName: nostrShortID(evt.PubKey),
			MessageID:  evt.ID,
			ReceivedAt: evt.CreatedAt.Time(),
		},
		Text: strings.TrimSpace(plaintext),
	}

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

	recipientPubKey = resolveNostrPubkey(recipientPubKey)

	chunks := splitNostrMessage(text, nostrMaxMessageLen)

	sharedSecret, err := nip04.ComputeSharedSecret(recipientPubKey, a.privKey)
	if err != nil {
		return fmt.Errorf("ECDH: %w", err)
	}

	var hardErr error
	for i, chunk := range chunks {
		encrypted, err := nip04.Encrypt(chunk, sharedSecret)
		if err != nil {

			hardErr = fmt.Errorf("NIP-04 encrypt: %w", err)
			break
		}

		evt := nostr.Event{
			PubKey:    a.pubKey,
			CreatedAt: nostr.Now(),
			Kind:      nostr.KindEncryptedDirectMessage,
			Tags:      nostr.Tags{{"p", recipientPubKey}},
			Content:   encrypted,
		}
		if err := evt.Sign(a.privKey); err != nil {
			hardErr = fmt.Errorf("sign: %w", err)
			break
		}

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

		hardErr = nostrFoldChunk(hardErr, success, relayErr)

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

func nostrFoldChunk(hardErr error, success int, relayErr error) error {
	if success == 0 && relayErr != nil {
		return relayErr
	}
	return hardErr
}

func (a *nostrAdapter) TriggerTyping(ctx context.Context, binding ChannelBinding) error {
	return nil
}

func NormalizeNostrPrivateKey(key string) string {
	return normalizeNostrKey(key)
}

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

	if _, err := hex.DecodeString(input); err == nil && len(input) == 64 {
		return input
	}
	return input
}

func splitNostrMessage(text string, maxLen int) []string {
	return splitMessageRunes(text, maxLen, false, false, true)
}

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

func ValidateNostrPrivateKey(key string) error {
	k := strings.TrimSpace(key)
	if k == "" {
		return fmt.Errorf("private_key is empty")
	}
	k = normalizeNostrKey(k)
	if len(k) != 64 {
		return fmt.Errorf("private_key must be 32 bytes hex (64 chars) or nsec format")
	}
	if _, err := nostr.GetPublicKey(k); err != nil {
		return fmt.Errorf("invalid private_key: %w", err)
	}
	return nil
}
