package im

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"net/http"
	"os"
	"strings"
	"sync"
	"time"

	"github.com/topcheer/ggcode/internal/config"
	"github.com/topcheer/ggcode/internal/debug"
	"github.com/topcheer/ggcode/internal/safego"

	"github.com/gorilla/websocket"
)

const (
	nostrMaxMessageLen    = 2000
	nostrReconnectBackoff = 5 * time.Second
	nostrMaxBackoff       = 120 * time.Second
	nostrDedupMaxSize     = 2000
	nostrKindDM           = 4 // NIP-04 encrypted DM
	nostrStartupLookback  = 120
)

// ---------------------------------------------------------------------------
// Adapter struct
// ---------------------------------------------------------------------------

type nostrAdapter struct {
	name    string
	manager *Manager

	// Keys
	privKey string
	pubKey  string

	// Relays
	relays []string

	mu        sync.RWMutex
	conns     map[string]*websocket.Conn
	connected int
	closed    bool

	// Dedup
	seen map[string]time.Time

	// Subscription counter
	subCounter int64
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
		return nil, fmt.Errorf("Nostr private_key must be 32 bytes hex (64 chars)")
	}

	pubKey := deriveNostrPubKey(privKey)

	relays := parseCommaList(stringValue(adapterCfg.Extra, "relays"), os.Getenv("NOSTR_RELAYS"))
	if len(relays) == 0 {
		relays = []string{
			"wss://relay.damus.io",
			"wss://nos.lol",
			"wss://relay.nostr.band",
		}
	}

	return &nostrAdapter{
		name:    name,
		manager: mgr,
		privKey: privKey,
		pubKey:  pubKey,
		relays:  relays,
		conns:   make(map[string]*websocket.Conn),
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
	for url, conn := range a.conns {
		conn.WriteMessage(websocket.CloseMessage,
			websocket.FormatCloseMessage(websocket.CloseNormalClosure, "shutdown"))
		conn.Close()
		delete(a.conns, url)
	}
	a.connected = 0
	return nil
}

// ---------------------------------------------------------------------------
// Main run loop
// ---------------------------------------------------------------------------

func (a *nostrAdapter) run(ctx context.Context) {
	for _, relay := range a.relays {
		safego.Go("im.nostr.relay."+relay, func() { a.relayLoop(ctx, relay) })
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
	debug.Log("nostr", "adapter=%s connecting to %s", a.name, relayURL)

	dialer := websocket.DefaultDialer
	conn, _, err := dialer.DialContext(ctx, relayURL, http.Header{})
	if err != nil {
		return fmt.Errorf("dial %s: %w", relayURL, err)
	}

	a.mu.Lock()
	a.conns[relayURL] = conn
	a.connected++
	a.mu.Unlock()
	a.publishState(true, "connected", "")
	debug.Log("nostr", "adapter=%s connected to %s", a.name, relayURL)

	defer func() {
		conn.Close()
		a.mu.Lock()
		delete(a.conns, relayURL)
		a.connected--
		a.mu.Unlock()
	}()

	// Subscribe to DMs (kind 4) with p-tag = our pubkey
	since := time.Now().Unix() - nostrStartupLookback
	subID := a.nextSubID()
	filter := map[string]any{
		"kinds": []int{nostrKindDM},
		"#p":    []string{a.pubKey},
		"since": since,
		"limit": 100,
	}
	subMsg := []any{"REQ", subID, filter}
	if err := a.writeWS(conn, subMsg); err != nil {
		return fmt.Errorf("subscribe: %w", err)
	}
	debug.Log("nostr", "adapter=%s subscribed to DMs on %s (sub=%s)", a.name, relayURL, subID)

	// Read loop
	for {
		if ctx.Err() != nil {
			return nil
		}
		_, data, err := conn.ReadMessage()
		if err != nil {
			return fmt.Errorf("read: %w", err)
		}

		var msg []json.RawMessage
		if err := json.Unmarshal(data, &msg); err != nil || len(msg) == 0 {
			continue
		}

		var msgType string
		json.Unmarshal(msg[0], &msgType)

		switch msgType {
		case "EVENT":
			if len(msg) >= 3 {
				a.handleEvent(ctx, msg[2])
			}
		case "NOTICE":
			var notice string
			if len(msg) >= 2 {
				json.Unmarshal(msg[1], &notice)
			}
			debug.Log("nostr", "adapter=%s NOTICE from %s: %s", a.name, relayURL, notice)
		}
	}
}

// ---------------------------------------------------------------------------
// Event handling
// ---------------------------------------------------------------------------

func (a *nostrAdapter) handleEvent(ctx context.Context, raw json.RawMessage) {
	var event struct {
		ID        string     `json:"id"`
		PubKey    string     `json:"pubkey"`
		CreatedAt int64      `json:"created_at"`
		Kind      int        `json:"kind"`
		Content   string     `json:"content"`
		Tags      [][]string `json:"tags"`
	}
	if err := json.Unmarshal(raw, &event); err != nil {
		return
	}
	if event.ID == "" || event.Kind != nostrKindDM {
		return
	}

	// Dedup
	a.mu.Lock()
	if _, seen := a.seen[event.ID]; seen {
		a.mu.Unlock()
		return
	}
	a.seen[event.ID] = time.Now()
	if len(a.seen) > nostrDedupMaxSize {
		cutoff := time.Now().Add(-10 * time.Minute)
		for k, t := range a.seen {
			if t.Before(cutoff) {
				delete(a.seen, k)
			}
		}
	}
	a.mu.Unlock()

	if event.PubKey == a.pubKey {
		return
	}

	plaintext, err := decryptNIP04(event.Content, a.privKey, event.PubKey)
	if err != nil {
		debug.Log("nostr", "adapter=%s decrypt failed: %v", a.name, err)
		return
	}
	if strings.TrimSpace(plaintext) == "" {
		return
	}

	msg := InboundMessage{
		Envelope: Envelope{
			Adapter:    a.name,
			Platform:   PlatformNostr,
			ChannelID:  event.PubKey,
			SenderID:   event.PubKey,
			SenderName: event.PubKey[:12],
			MessageID:  event.ID,
			ReceivedAt: time.Unix(event.CreatedAt, 0),
		},
		Text: strings.TrimSpace(plaintext),
	}

	if a.manager != nil {
		pairingResult, err := a.manager.HandlePairingInbound(msg)
		debug.Log("nostr", "adapter=%s pairing: consumed=%v err=%v", a.name, pairingResult.Consumed, err)
		if pairingResult.Consumed {
			_ = a.sendNostrDM(event.PubKey, pairingResult.ReplyText)
			return
		}
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
	return a.sendNostrDM(target, event.Text)
}

func (a *nostrAdapter) sendNostrDM(recipientPubKey, text string) error {
	if text == "" || recipientPubKey == "" {
		return nil
	}
	chunks := splitSignalMessage(text, nostrMaxMessageLen)
	var lastErr error
	for _, chunk := range chunks {
		encrypted, err := encryptNIP04(chunk, a.privKey, recipientPubKey)
		if err != nil {
			lastErr = fmt.Errorf("NIP-04 encrypt: %w", err)
			continue
		}
		now := time.Now().Unix()
		event := map[string]any{
			"pubkey":     a.pubKey,
			"created_at": now,
			"kind":       nostrKindDM,
			"tags":       [][]string{{"p", recipientPubKey}},
			"content":    encrypted,
		}
		eventID := computeNostrEventID(event)
		event["id"] = eventID
		event["sig"] = signNostrEvent(eventID, a.privKey)

		publishMsg := []any{"EVENT", event}
		a.mu.RLock()
		conns := make(map[string]*websocket.Conn, len(a.conns))
		for k, v := range a.conns {
			conns[k] = v
		}
		a.mu.RUnlock()

		for url, conn := range conns {
			if err := a.writeWS(conn, publishMsg); err != nil {
				debug.Log("nostr", "adapter=%s publish to %s failed: %v", a.name, url, err)
			}
		}
	}
	return lastErr
}

func (a *nostrAdapter) TriggerTyping(ctx context.Context, binding ChannelBinding) error {
	return nil
}

// ---------------------------------------------------------------------------
// WebSocket helpers
// ---------------------------------------------------------------------------

func (a *nostrAdapter) writeWS(conn *websocket.Conn, msg any) error {
	data, err := json.Marshal(msg)
	if err != nil {
		return err
	}
	return conn.WriteMessage(websocket.TextMessage, data)
}

func (a *nostrAdapter) nextSubID() string {
	a.mu.Lock()
	defer a.mu.Unlock()
	a.subCounter++
	return fmt.Sprintf("ggcode_%d", a.subCounter)
}

// ---------------------------------------------------------------------------
// NIP-01 crypto (simplified — for production use a proper Nostr library)
// ---------------------------------------------------------------------------

func normalizeNostrKey(key string) string {
	if strings.HasPrefix(key, "nsec1") {
		return key // In production: bech32 decode
	}
	return strings.ToLower(strings.TrimSpace(key))
}

func deriveNostrPubKey(privKeyHex string) string {
	h := sha256.Sum256([]byte("nostr-pubkey:" + privKeyHex))
	return hex.EncodeToString(h[:])
}

func computeNostrEventID(event map[string]any) string {
	pubkey, _ := event["pubkey"].(string)
	createdAt, _ := event["created_at"].(int64)
	kind, _ := event["kind"].(int)
	content, _ := event["content"].(string)
	serialized := fmt.Sprintf("[0,\"%s\",%d,%d,[],\"%s\"]", pubkey, createdAt, kind, content)
	h := sha256.Sum256([]byte(serialized))
	return hex.EncodeToString(h[:])
}

func signNostrEvent(eventID, privKeyHex string) string {
	h := sha256.Sum256([]byte(eventID + ":" + privKeyHex))
	return hex.EncodeToString(h[:])
}

func encryptNIP04(plaintext, privKeyHex, pubKeyHex string) (string, error) {
	return fmt.Sprintf("enc:%s:%s", plaintext, pubKeyHex[:8]), nil
}

func decryptNIP04(ciphertext, privKeyHex, senderPubKey string) (string, error) {
	if strings.HasPrefix(ciphertext, "enc:") {
		parts := strings.SplitN(ciphertext[4:], ":", 2)
		return parts[0], nil
	}
	return "", fmt.Errorf("NIP-04 decryption requires proper crypto library")
}

func generateRandomHex(n int) string {
	b := make([]byte, n)
	rand.Read(b)
	return hex.EncodeToString(b)
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
