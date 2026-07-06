//go:build integration_service

package im

import (
	"context"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/nbd-wtf/go-nostr"
	"github.com/nbd-wtf/go-nostr/nip04"
	"github.com/nbd-wtf/go-nostr/nip19"
	"github.com/topcheer/ggcode/internal/config"
)

// testNostrSink captures outbound messages sent through the manager.
type testNostrSink struct {
	name    string
	mu      sync.Mutex
	sent    []OutboundEvent
	binding ChannelBinding
}

func (s *testNostrSink) Name() string { return s.name }
func (s *testNostrSink) Send(ctx context.Context, binding ChannelBinding, event OutboundEvent) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.sent = append(s.sent, event)
	s.binding = binding
	return nil
}

// testNostrBridge captures inbound messages delivered to the bridge.
type testNostrBridge struct {
	mu      sync.Mutex
	inbound []InboundMessage
}

func (b *testNostrBridge) SubmitInboundMessage(ctx context.Context, msg InboundMessage) error {
	b.mu.Lock()
	defer b.mu.Unlock()
	b.inbound = append(b.inbound, msg)
	return nil
}

// setupNostrTest creates a bot adapter + sender identity + manager with session bound.
func setupNostrTest(t *testing.T, adapterName string) (*nostrAdapter, string, string, *Manager, *testNostrBridge) {
	t.Helper()

	botSK := nostr.GeneratePrivateKey()
	_, _ = nostr.GetPublicKey(botSK) // botPK derived by adapter
	senderSK := nostr.GeneratePrivateKey()
	senderPK, _ := nostr.GetPublicKey(senderSK)

	mgr := NewManager()
	bridge := &testNostrBridge{}
	mgr.SetBridge(bridge)
	mgr.BindSession(SessionBinding{
		Workspace: "/tmp/test-nostr-workspace",
		BoundAt:   time.Now(),
	})

	sink := &testNostrSink{name: adapterName}
	mgr.RegisterSink(sink)

	adapter, err := newNostrAdapter(adapterName, config.IMConfig{}, config.IMAdapterConfig{
		Enabled:  true,
		Platform: "nostr",
		Extra:    map[string]interface{}{"private_key": botSK},
	}, mgr)
	if err != nil {
		t.Fatalf("create adapter: %v", err)
	}

	return adapter, senderSK, senderPK, mgr, bridge
}

// encryptAndSign creates a signed NIP-04 DM event from sender to bot.
func encryptAndSign(t *testing.T, senderSK, botPK, text string) *nostr.Event {
	t.Helper()
	shared, err := nip04.ComputeSharedSecret(botPK, senderSK)
	if err != nil {
		t.Fatalf("ECDH: %v", err)
	}
	encrypted, err := nip04.Encrypt(text, shared)
	if err != nil {
		t.Fatalf("encrypt: %v", err)
	}
	senderPK, _ := nostr.GetPublicKey(senderSK)
	evt := &nostr.Event{
		PubKey:    senderPK,
		CreatedAt: nostr.Now(),
		Kind:      nostr.KindEncryptedDirectMessage,
		Tags:      nostr.Tags{{"p", botPK}},
		Content:   encrypted,
	}
	if err := evt.Sign(senderSK); err != nil {
		t.Fatalf("sign: %v", err)
	}
	return evt
}

// ---------------------------------------------------------------------------
// E2E Test: Pairing flow — first message generates pairing code
// ---------------------------------------------------------------------------

func TestNostrE2E_PairingFirstMessage(t *testing.T) {
	adapter, senderSK, senderPK, mgr, _ := setupNostrTest(t, "e2e-pair")

	// Send first message — should trigger pairing
	evt := encryptAndSign(t, senderSK, adapter.pubKey, "hello bot")
	adapter.handleEvent(context.Background(), evt)

	// Verify pendingPairing was set (pairing code generated)
	mgr.mu.Lock()
	pending := mgr.pendingPairing
	mgr.mu.Unlock()

	if pending == nil {
		t.Fatal("expected pendingPairing to be set after first message")
	}
	if pending.Code == "" {
		t.Error("pairing code should not be empty")
	}
	if pending.Adapter != "e2e-pair" {
		t.Errorf("pending adapter = %q, want %q", pending.Adapter, "e2e-pair")
	}
	if pending.ChannelID != senderPK {
		t.Errorf("pending channelID = %q, want %q", pending.ChannelID, senderPK)
	}
	t.Logf("Pairing code generated: %s for sender %s", pending.Code, senderPK[:12])
}

// ---------------------------------------------------------------------------
// E2E Test: Pairing flow — second message with correct code completes binding
// ---------------------------------------------------------------------------

func TestNostrE2E_PairingCompleteBinding(t *testing.T) {
	adapter, senderSK, senderPK, mgr, _ := setupNostrTest(t, "e2e-bind")

	// First message triggers pairing
	evt1 := encryptAndSign(t, senderSK, adapter.pubKey, "hello")
	adapter.handleEvent(context.Background(), evt1)

	mgr.mu.Lock()
	code := mgr.pendingPairing.Code
	mgr.mu.Unlock()

	if code == "" {
		t.Fatal("no pairing code")
	}

	// Second message with correct pairing code
	evt2 := encryptAndSign(t, senderSK, adapter.pubKey, code)
	adapter.handleEvent(context.Background(), evt2)

	// Verify binding was created
	bindings, _ := mgr.ListBindings()
	var found *ChannelBinding
	for i := range bindings {
		if bindings[i].ChannelID == senderPK {
			found = &bindings[i]
			break
		}
	}
	if found == nil {
		t.Fatal("expected binding after correct pairing code")
	}
	if found.Platform != PlatformNostr {
		t.Errorf("binding platform = %q, want %q", found.Platform, PlatformNostr)
	}
	t.Logf("Binding created: channel=%s… adapter=%s", found.ChannelID[:12], found.Adapter)
}

// ---------------------------------------------------------------------------
// E2E Test: Wrong pairing code does not bind
// ---------------------------------------------------------------------------

func TestNostrE2E_WrongPairingCode(t *testing.T) {
	adapter, senderSK, _, mgr, _ := setupNostrTest(t, "e2e-wrong")

	// First message triggers pairing
	evt1 := encryptAndSign(t, senderSK, adapter.pubKey, "hello")
	adapter.handleEvent(context.Background(), evt1)

	// Wrong code
	evt2 := encryptAndSign(t, senderSK, adapter.pubKey, "0000")
	adapter.handleEvent(context.Background(), evt2)

	// Should NOT be bound
	bindings, _ := mgr.ListBindings()
	for _, b := range bindings {
		if b.Adapter == "e2e-wrong" {
			t.Error("should not be bound with wrong code")
		}
	}

	// Pending should still exist
	mgr.mu.Lock()
	pending := mgr.pendingPairing
	mgr.mu.Unlock()
	if pending == nil {
		t.Error("pending pairing should still exist after wrong code")
	}
}

// ---------------------------------------------------------------------------
// E2E Test: Bound message goes to HandleInbound → bridge
// ---------------------------------------------------------------------------

func TestNostrE2E_BoundMessageDeliveredToBridge(t *testing.T) {
	adapter, senderSK, senderPK, mgr, bridge := setupNostrTest(t, "e2e-inbound")

	// Manually bind the channel
	binding := ChannelBinding{
		ChannelID: senderPK,
		Platform:  PlatformNostr,
		Adapter:   "e2e-inbound",
	}
	mgr.currentBindings["e2e-inbound"] = &binding

	testMessage := "hello from nostr, this is a bound message"
	evt := encryptAndSign(t, senderSK, adapter.pubKey, testMessage)
	adapter.handleEvent(context.Background(), evt)

	// Verify bridge received the message
	bridge.mu.Lock()
	defer bridge.mu.Unlock()
	if len(bridge.inbound) != 1 {
		t.Fatalf("bridge inbound: got %d, want 1", len(bridge.inbound))
	}
	msg := bridge.inbound[0]
	if msg.Text != testMessage {
		t.Errorf("text = %q, want %q", msg.Text, testMessage)
	}
	if msg.Envelope.Platform != PlatformNostr {
		t.Errorf("platform = %q, want %q", msg.Envelope.Platform, PlatformNostr)
	}
	if msg.Envelope.ChannelID != senderPK {
		t.Errorf("channelID = %q, want %q", msg.Envelope.ChannelID, senderPK)
	}
	if msg.Envelope.SenderID != senderPK {
		t.Errorf("senderID = %q, want %q", msg.Envelope.SenderID, senderPK)
	}
	t.Logf("Inbound delivered: text=%q sender=%s", msg.Text, msg.Envelope.SenderID[:12])
}

// ---------------------------------------------------------------------------
// E2E Test: Self-message ignored
// ---------------------------------------------------------------------------

func TestNostrE2E_SelfMessageIgnored(t *testing.T) {
	adapter, _, _, mgr, bridge := setupNostrTest(t, "e2e-self")

	// Pre-bind with bot's own pubkey
	mgr.currentBindings["e2e-self"] = &ChannelBinding{
		ChannelID: adapter.pubKey,
		Platform:  PlatformNostr,
		Adapter:   "e2e-self",
	}

	// Bot sends event to itself
	shared, _ := nip04.ComputeSharedSecret(adapter.pubKey, adapter.privKey)
	encrypted, _ := nip04.Encrypt("self message", shared)
	evt := &nostr.Event{
		PubKey:    adapter.pubKey,
		CreatedAt: nostr.Now(),
		Kind:      nostr.KindEncryptedDirectMessage,
		Tags:      nostr.Tags{{"p", adapter.pubKey}},
		Content:   encrypted,
	}
	evt.Sign(adapter.privKey)

	adapter.handleEvent(context.Background(), evt)

	bridge.mu.Lock()
	defer bridge.mu.Unlock()
	if len(bridge.inbound) != 0 {
		t.Errorf("self-message should be ignored, got %d inbound", len(bridge.inbound))
	}
}

// ---------------------------------------------------------------------------
// E2E Test: Dedup — same event processed only once
// ---------------------------------------------------------------------------

func TestNostrE2E_DedupSameEvent(t *testing.T) {
	adapter, senderSK, senderPK, mgr, bridge := setupNostrTest(t, "e2e-dedup")

	mgr.currentBindings["e2e-dedup"] = &ChannelBinding{
		ChannelID: senderPK, Platform: PlatformNostr, Adapter: "e2e-dedup",
	}

	evt := encryptAndSign(t, senderSK, adapter.pubKey, "dedup test")

	// Process same event twice
	adapter.handleEvent(context.Background(), evt)
	adapter.handleEvent(context.Background(), evt)

	bridge.mu.Lock()
	defer bridge.mu.Unlock()
	if len(bridge.inbound) != 1 {
		t.Errorf("dedup: got %d inbound, want 1", len(bridge.inbound))
	}
}

// ---------------------------------------------------------------------------
// E2E Test: Empty message ignored
// ---------------------------------------------------------------------------

func TestNostrE2E_EmptyMessageIgnored(t *testing.T) {
	adapter, senderSK, senderPK, mgr, bridge := setupNostrTest(t, "e2e-empty")

	mgr.currentBindings["e2e-empty"] = &ChannelBinding{
		ChannelID: senderPK, Platform: PlatformNostr, Adapter: "e2e-empty",
	}

	evt := encryptAndSign(t, senderSK, adapter.pubKey, "   ")
	adapter.handleEvent(context.Background(), evt)

	bridge.mu.Lock()
	defer bridge.mu.Unlock()
	if len(bridge.inbound) != 0 {
		t.Errorf("empty message should be ignored, got %d inbound", len(bridge.inbound))
	}
}

// ---------------------------------------------------------------------------
// E2E Test: Encrypted for wrong recipient fails to decrypt
// ---------------------------------------------------------------------------

func TestNostrE2E_WrongRecipientIgnored(t *testing.T) {
	adapter, senderSK, senderPK, mgr, bridge := setupNostrTest(t, "e2e-wrongrec")

	mgr.currentBindings["e2e-wrongrec"] = &ChannelBinding{
		ChannelID: senderPK, Platform: PlatformNostr, Adapter: "e2e-wrongrec",
	}

	// Encrypt for a different pubkey, but p-tag points to bot
	otherPK := strings.Repeat("c", 64)
	shared, _ := nip04.ComputeSharedSecret(otherPK, senderSK)
	encrypted, _ := nip04.Encrypt("wrong recipient", shared)
	senderPK, _ = nostr.GetPublicKey(senderSK)
	evt := &nostr.Event{
		PubKey:    senderPK,
		CreatedAt: nostr.Now(),
		Kind:      nostr.KindEncryptedDirectMessage,
		Tags:      nostr.Tags{{"p", adapter.pubKey}},
		Content:   encrypted,
	}
	evt.Sign(senderSK)

	adapter.handleEvent(context.Background(), evt)

	bridge.mu.Lock()
	defer bridge.mu.Unlock()
	if len(bridge.inbound) != 0 {
		t.Errorf("wrong recipient should be ignored, got %d inbound", len(bridge.inbound))
	}
}

// ---------------------------------------------------------------------------
// E2E Test: NIP-19 key interoperability
// ---------------------------------------------------------------------------

func TestNostrE2E_NIP19Keys(t *testing.T) {
	sk := nostr.GeneratePrivateKey()
	pk, _ := nostr.GetPublicKey(sk)
	nsec, _ := nip19.EncodePrivateKey(sk)
	npub, _ := nip19.EncodePublicKey(pk)

	// Adapter accepts nsec
	a, err := newNostrAdapter("e2e-nip19", config.IMConfig{}, config.IMAdapterConfig{
		Enabled:  true,
		Platform: "nostr",
		Extra:    map[string]interface{}{"private_key": nsec},
	}, nil)
	if err != nil {
		t.Fatalf("nsec adapter: %v", err)
	}
	if a.pubKey != pk {
		t.Errorf("pubKey mismatch: got %q, want %q", a.pubKey, pk)
	}

	// resolveNostrPubkey handles npub
	if got := resolveNostrPubkey(npub); got != pk {
		t.Errorf("resolveNostrPubkey(npub) = %q, want %q", got, pk)
	}

	// resolveNostrPubkey passes through hex
	if got := resolveNostrPubkey(pk); got != pk {
		t.Errorf("resolveNostrPubkey(hex) = %q, want %q", got, pk)
	}
}

// ---------------------------------------------------------------------------
// E2E Test: Outbound encrypt → decrypt round-trip
// ---------------------------------------------------------------------------

func TestNostrE2E_OutboundRoundTrip(t *testing.T) {
	botSK := nostr.GeneratePrivateKey()
	botPK, _ := nostr.GetPublicKey(botSK)
	senderSK := nostr.GeneratePrivateKey()
	senderPK, _ := nostr.GetPublicKey(senderSK)

	outboundText := "outbound reply from bot 🤖"

	// Bot encrypts to sender
	botShared, _ := nip04.ComputeSharedSecret(senderPK, botSK)
	encrypted, _ := nip04.Encrypt(outboundText, botShared)

	evt := nostr.Event{
		PubKey:    botPK,
		CreatedAt: nostr.Now(),
		Kind:      nostr.KindEncryptedDirectMessage,
		Tags:      nostr.Tags{{"p", senderPK}},
		Content:   encrypted,
	}
	if err := evt.Sign(botSK); err != nil {
		t.Fatalf("sign: %v", err)
	}

	// Verify signature
	ok, err := evt.CheckSignature()
	if err != nil {
		t.Fatalf("check sig: %v", err)
	}
	if !ok {
		t.Error("signature should be valid")
	}

	// Sender decrypts
	senderShared, _ := nip04.ComputeSharedSecret(evt.PubKey, senderSK)
	decrypted, err := nip04.Decrypt(evt.Content, senderShared)
	if err != nil {
		t.Fatalf("decrypt: %v", err)
	}
	if decrypted != outboundText {
		t.Errorf("round-trip: got %q, want %q", decrypted, outboundText)
	}
	t.Logf("Outbound round-trip OK: %q", decrypted)
}

// ---------------------------------------------------------------------------
// E2E Test: Unicode messages
// ---------------------------------------------------------------------------

func TestNostrE2E_UnicodeMessages(t *testing.T) {
	adapter, senderSK, senderPK, mgr, bridge := setupNostrTest(t, "e2e-unicode")

	mgr.currentBindings["e2e-unicode"] = &ChannelBinding{
		ChannelID: senderPK, Platform: PlatformNostr, Adapter: "e2e-unicode",
	}

	tests := []struct {
		name string
		text string
	}{
		{"chinese", "你好世界，这是一个测试"},
		{"emoji", "Hello 🌍 🚀 🎉"},
		{"japanese", "こんにちは世界"},
		{"mixed", "Hello 世界 🌍 test 123"},
		{"newlines", "line1\nline2\nline3"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			bridge.mu.Lock()
			bridge.inbound = bridge.inbound[:0]
			bridge.mu.Unlock()

			evt := encryptAndSign(t, senderSK, adapter.pubKey, tt.text)
			adapter.handleEvent(context.Background(), evt)

			bridge.mu.Lock()
			defer bridge.mu.Unlock()
			if len(bridge.inbound) != 1 {
				t.Fatalf("inbound: got %d, want 1", len(bridge.inbound))
			}
			if bridge.inbound[0].Text != tt.text {
				t.Errorf("text round-trip: got %q, want %q", bridge.inbound[0].Text, tt.text)
			}
		})
	}
}

// ---------------------------------------------------------------------------
// E2E Test: Long message
// ---------------------------------------------------------------------------

func TestNostrE2E_LongMessage(t *testing.T) {
	adapter, senderSK, senderPK, mgr, bridge := setupNostrTest(t, "e2e-long")

	mgr.currentBindings["e2e-long"] = &ChannelBinding{
		ChannelID: senderPK, Platform: PlatformNostr, Adapter: "e2e-long",
	}

	// Note: handleEvent does strings.TrimSpace on plaintext, so trailing space is trimmed
	longText := strings.TrimSpace(strings.Repeat("Hello 世界 ", 500))
	evt := encryptAndSign(t, senderSK, adapter.pubKey, longText)
	adapter.handleEvent(context.Background(), evt)

	bridge.mu.Lock()
	defer bridge.mu.Unlock()
	if len(bridge.inbound) != 1 {
		t.Fatalf("inbound: got %d, want 1", len(bridge.inbound))
	}
	if bridge.inbound[0].Text != longText {
		t.Errorf("long text: got len=%d, want len=%d", len(bridge.inbound[0].Text), len(longText))
	}
	t.Logf("Long message OK: %d chars", len(bridge.inbound[0].Text))
}

// ---------------------------------------------------------------------------
// E2E Test: TriggerTyping is a no-op
// ---------------------------------------------------------------------------

func TestNostrE2E_TriggerTypingNoop(t *testing.T) {
	adapter, _, _, _, _ := setupNostrTest(t, "e2e-typing")
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	if err := adapter.TriggerTyping(ctx, ChannelBinding{ChannelID: strings.Repeat("a", 64)}); err != nil {
		t.Errorf("TriggerTyping should be noop, got: %v", err)
	}
}

// ---------------------------------------------------------------------------
// E2E Test: sendNostrDM with empty target/text is a no-op
// ---------------------------------------------------------------------------

func TestNostrE2E_SendNostrDMEdgeCases(t *testing.T) {
	adapter, _, _, _, _ := setupNostrTest(t, "e2e-send-edge")
	if err := adapter.sendNostrDM(context.Background(), "", "hello"); err != nil {
		t.Errorf("empty target should be nil, got: %v", err)
	}
	if err := adapter.sendNostrDM(context.Background(), strings.Repeat("a", 64), ""); err != nil {
		t.Errorf("empty text should be nil, got: %v", err)
	}
}

// ---------------------------------------------------------------------------
// E2E Test: Multiple senders each get their own pairing flow
// ---------------------------------------------------------------------------

func TestNostrE2E_MultipleSenders(t *testing.T) {
	adapter, _, _, mgr, _ := setupNostrTest(t, "e2e-multi")

	// Generate 3 different senders
	type sender struct {
		sk, pk string
	}
	senders := make([]sender, 3)
	for i := range senders {
		senders[i].sk = nostr.GeneratePrivateKey()
		senders[i].pk, _ = nostr.GetPublicKey(senders[i].sk)
	}

	// Each sender sends first message
	for i, s := range senders {
		evt := encryptAndSign(t, s.sk, adapter.pubKey, "message from sender "+strings.Repeat("!", i+1))
		adapter.handleEvent(context.Background(), evt)
	}

	// Only one pending pairing at a time (last sender wins)
	mgr.mu.Lock()
	pending := mgr.pendingPairing
	mgr.mu.Unlock()
	if pending == nil {
		t.Fatal("expected pending pairing")
	}
	t.Logf("Pending pairing for sender %s (last one wins)", pending.ChannelID[:12])
}

// ---------------------------------------------------------------------------
// E2E Test: Close and reconnect state
// ---------------------------------------------------------------------------

func TestNostrE2E_CloseClearsConnections(t *testing.T) {
	adapter, _, _, _, _ := setupNostrTest(t, "e2e-close")

	// Verify initial state
	if adapter.connected != 0 {
		t.Errorf("initial connected = %d, want 0", adapter.connected)
	}

	// Close should not panic
	if err := adapter.Close(); err != nil {
		t.Errorf("close error: %v", err)
	}

	adapter.mu.RLock()
	closed := adapter.closed
	adapter.mu.RUnlock()
	if !closed {
		t.Error("adapter should be closed after Close()")
	}
}
