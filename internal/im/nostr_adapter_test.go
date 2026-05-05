package im

import (
	"strings"
	"testing"

	"github.com/nbd-wtf/go-nostr"
	"github.com/nbd-wtf/go-nostr/nip04"
	"github.com/nbd-wtf/go-nostr/nip19"
	"github.com/topcheer/ggcode/internal/config"
)

func TestNewNostrAdapter_MissingPrivateKey(t *testing.T) {
	adapterCfg := config.IMAdapterConfig{
		Enabled:  true,
		Platform: "nostr",
		Extra:    map[string]interface{}{},
	}
	_, err := newNostrAdapter("test", config.IMConfig{}, adapterCfg, nil)
	if err == nil {
		t.Fatal("expected error for missing private_key")
	}
	if !strings.Contains(err.Error(), "private_key") {
		t.Errorf("error should mention private_key: %v", err)
	}
}

func TestNewNostrAdapter_InvalidKeyLen(t *testing.T) {
	adapterCfg := config.IMAdapterConfig{
		Enabled:  true,
		Platform: "nostr",
		Extra: map[string]interface{}{
			"private_key": "tooshort",
		},
	}
	_, err := newNostrAdapter("test", config.IMConfig{}, adapterCfg, nil)
	if err == nil {
		t.Fatal("expected error for short key")
	}
}

func TestNewNostrAdapter_ValidHexKey(t *testing.T) {
	sk := nostr.GeneratePrivateKey()
	adapterCfg := config.IMAdapterConfig{
		Enabled:  true,
		Platform: "nostr",
		Extra: map[string]interface{}{
			"private_key": sk,
			"relays":      "wss://relay.example.com,wss://relay2.example.com",
		},
	}
	a, err := newNostrAdapter("test", config.IMConfig{}, adapterCfg, nil)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	pk, _ := nostr.GetPublicKey(sk)
	if a.pubKey != pk {
		t.Errorf("pubKey = %q, want %q", a.pubKey, pk)
	}
	if len(a.relays) != 2 {
		t.Fatalf("relays len = %d, want 2", len(a.relays))
	}
	if a.relays[0] != "wss://relay.example.com" {
		t.Errorf("relays[0] = %q", a.relays[0])
	}
}

func TestNewNostrAdapter_DefaultRelays(t *testing.T) {
	sk := nostr.GeneratePrivateKey()
	adapterCfg := config.IMAdapterConfig{
		Enabled:  true,
		Platform: "nostr",
		Extra: map[string]interface{}{
			"private_key": sk,
		},
	}
	a, err := newNostrAdapter("test", config.IMConfig{}, adapterCfg, nil)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(a.relays) != 3 {
		t.Fatalf("default relays len = %d, want 3", len(a.relays))
	}
}

func TestNewNostrAdapter_nsecKey(t *testing.T) {
	sk := nostr.GeneratePrivateKey()
	nsec, err := nip19.EncodePrivateKey(sk)
	if err != nil {
		t.Fatalf("encode nsec: %v", err)
	}

	adapterCfg := config.IMAdapterConfig{
		Enabled:  true,
		Platform: "nostr",
		Extra: map[string]interface{}{
			"private_key": nsec,
		},
	}
	a, err := newNostrAdapter("test", config.IMConfig{}, adapterCfg, nil)
	if err != nil {
		t.Fatalf("unexpected error with nsec: %v", err)
	}
	pk, _ := nostr.GetPublicKey(sk)
	if a.pubKey != pk {
		t.Errorf("pubKey mismatch: got %q, want %q", a.pubKey, pk)
	}
}

func TestNormalizeNostrKey(t *testing.T) {
	sk := nostr.GeneratePrivateKey()
	nsec, _ := nip19.EncodePrivateKey(sk)

	// nsec should decode back to hex
	got := normalizeNostrKey(nsec)
	if got != sk {
		t.Errorf("normalizeNostrKey(nsec) = %q, want %q", got, sk)
	}

	// Hex should lowercase
	got = normalizeNostrKey("ABC123")
	if got != "abc123" {
		t.Errorf("normalizeNostrKey(ABC123) = %q, want abc123", got)
	}
}

func TestNIP04EncryptDecryptRoundTrip(t *testing.T) {
	sk1 := nostr.GeneratePrivateKey()
	pk1, _ := nostr.GetPublicKey(sk1)

	sk2 := nostr.GeneratePrivateKey()
	pk2, _ := nostr.GetPublicKey(sk2)

	message := "Hello, Nostr world! 这是一个测试 🌍"

	// Encrypt from sk1 → pk2
	shared1, err := nip04.ComputeSharedSecret(pk2, sk1)
	if err != nil {
		t.Fatalf("shared secret 1: %v", err)
	}
	encrypted, err := nip04.Encrypt(message, shared1)
	if err != nil {
		t.Fatalf("encrypt: %v", err)
	}

	// Decrypt from sk2 ← pk1
	shared2, err := nip04.ComputeSharedSecret(pk1, sk2)
	if err != nil {
		t.Fatalf("shared secret 2: %v", err)
	}
	decrypted, err := nip04.Decrypt(encrypted, shared2)
	if err != nil {
		t.Fatalf("decrypt: %v", err)
	}

	if decrypted != message {
		t.Errorf("round-trip failed: got %q, want %q", decrypted, message)
	}
}

func TestNostrEventSignAndVerify(t *testing.T) {
	sk := nostr.GeneratePrivateKey()
	pk, _ := nostr.GetPublicKey(sk)

	evt := nostr.Event{
		PubKey:    pk,
		CreatedAt: nostr.Now(),
		Kind:      nostr.KindEncryptedDirectMessage,
		Tags:      nostr.Tags{{"p", strings.Repeat("b", 64)}},
		Content:   "test content",
	}
	if err := evt.Sign(sk); err != nil {
		t.Fatalf("sign: %v", err)
	}

	if len(evt.ID) != 64 {
		t.Errorf("event ID len = %d, want 64", len(evt.ID))
	}
	if len(evt.Sig) != 128 {
		t.Errorf("signature len = %d, want 128", len(evt.Sig))
	}

	ok, err := evt.CheckSignature()
	if err != nil {
		t.Fatalf("check sig: %v", err)
	}
	if !ok {
		t.Error("signature should be valid")
	}
}

func TestResolveNostrPubkey(t *testing.T) {
	// Hex pubkey should pass through
	hex := strings.Repeat("a", 64)
	if got := resolveNostrPubkey(hex); got != hex {
		t.Errorf("hex pass-through: got %q", got)
	}

	// npub should decode
	sk := nostr.GeneratePrivateKey()
	pk, _ := nostr.GetPublicKey(sk)
	npub, _ := nip19.EncodePublicKey(pk)
	if got := resolveNostrPubkey(npub); got != pk {
		t.Errorf("npub decode: got %q, want %q", got, pk)
	}
}
