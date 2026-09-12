package tui

// #2167 regression: the masking pattern lacked "key" - api_key/
// private_key/encrypt_key (the nostr identity key is a REQUIRED adapter
// credential) all rendered in cleartext in the IM edit panel despite
// the #2158 fix's claim.

import (
	"strings"
	"testing"

	"github.com/topcheer/ggcode/internal/config"
)

func TestLooksLikeSecretFieldKeySuffixes(t *testing.T) {
	cases := []string{
		"api_key", "API_KEY", "private_key", "encrypt_key",
		"client_key", "access_key", "secret_key", "bot_token",
	}
	for _, k := range cases {
		if !looksLikeSecretField(k) {
			t.Errorf("looksLikeSecretField(%q) = false, want true", k)
		}
	}
	// Config-side copy must stay in lockstep (three consumer surfaces).
	for _, k := range cases {
		if !config.LooksLikeSecretField(k) {
			t.Errorf("config.LooksLikeSecretField(%q) = false, want true", k)
		}
	}
}

func TestIMEditSelectMasksNostrPrivateKey(t *testing.T) {
	m := newTestModel()
	m.config = &config.Config{}
	m.config.IM.Adapters = map[string]config.IMAdapterConfig{}
	m.config.IM.Adapters["nostr-2158"] = config.IMAdapterConfig{
		Extra: map[string]interface{}{
			"private_key": "nsec1zzzzzzzzzzzzzz",
		},
	}
	s := m.enterIMEditSelect("nostr-2158")
	v, ok := s.fieldValues["private_key"]
	if !ok {
		t.Fatal("private_key missing from edit state")
	}
	if strings.Contains(v, "nsec1") {
		t.Fatalf("nostr identity private key rendered in cleartext: %q", v)
	}
}
