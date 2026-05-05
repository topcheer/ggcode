package im

import (
	"strings"
	"testing"

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

func TestNewNostrAdapter_ValidConfig(t *testing.T) {
	adapterCfg := config.IMAdapterConfig{
		Enabled:  true,
		Platform: "nostr",
		Extra: map[string]interface{}{
			"private_key": strings.Repeat("a", 64),
			"relays":      "wss://relay.example.com,wss://relay2.example.com",
		},
	}
	a, err := newNostrAdapter("test", config.IMConfig{}, adapterCfg, nil)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(a.relays) != 2 {
		t.Fatalf("relays len = %d, want 2", len(a.relays))
	}
	if a.relays[0] != "wss://relay.example.com" {
		t.Errorf("relays[0] = %q", a.relays[0])
	}
}

func TestNewNostrAdapter_DefaultRelays(t *testing.T) {
	adapterCfg := config.IMAdapterConfig{
		Enabled:  true,
		Platform: "nostr",
		Extra: map[string]interface{}{
			"private_key": strings.Repeat("b", 64),
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

func TestNormalizeNostrKey(t *testing.T) {
	tests := []struct {
		in, want string
	}{
		{"ABC123", "abc123"},
		{" abc123 ", "abc123"},
		{"nsec1test", "nsec1test"},
	}
	for _, tt := range tests {
		got := normalizeNostrKey(tt.in)
		if got != tt.want {
			t.Errorf("normalizeNostrKey(%q) = %q, want %q", tt.in, got, tt.want)
		}
	}
}

func TestComputeNostrEventID(t *testing.T) {
	event := map[string]any{
		"pubkey":     strings.Repeat("a", 64),
		"created_at": int64(1234567890),
		"kind":       4,
		"content":    "test",
	}
	id := computeNostrEventID(event)
	if len(id) != 64 {
		t.Errorf("event ID len = %d, want 64", len(id))
	}
}
