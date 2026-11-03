package config

import "testing"

// TestIssue1015LanChatKeyDecoupledFromA2A pins the decoupling: lanchat must
// never inherit a2a.auth.api_key. Configuring an A2A task key locks down A2A
// traffic only; LAN Chat keeps zero-config interop via the community key
// unless lanchat.api_key is explicitly set.
func TestIssue1015LanChatKeyDecoupledFromA2A(t *testing.T) {

	cfg := Config{}
	if got := cfg.LanChat.EffectiveAPIKey(); got != DefaultA2AAPIKey {
		t.Fatalf("zero-config lanchat key = %q, want community key %q", got, DefaultA2AAPIKey)
	}

	cfg = Config{}
	cfg.A2A.Auth.APIKey = "a2a-task-secret"
	if got := cfg.LanChat.EffectiveAPIKey(); got != DefaultA2AAPIKey {
		t.Fatalf("lanchat key inherited a2a.auth.api_key %q; want community key %q", got, DefaultA2AAPIKey)
	}

	cfg = Config{}
	cfg.LanChat.APIKey = "lan-secret"
	if got := cfg.LanChat.EffectiveAPIKey(); got != "lan-secret" {
		t.Fatalf("lanchat key = %q, want dedicated %q", got, "lan-secret")
	}

	cfg = Config{}
	cfg.A2A.Auth.APIKey = "a2a-task-secret"
	cfg.LanChat.APIKey = "lan-secret"
	if got := cfg.LanChat.EffectiveAPIKey(); got != "lan-secret" {
		t.Fatalf("lanchat key = %q, want %q (must not follow a2a key)", got, "lan-secret")
	}
}
