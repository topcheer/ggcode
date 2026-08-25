package config

import "testing"

// TestIssue1015LanChatKeyDecoupledFromA2A pins the decoupling: lanchat must
// never inherit a2a.auth.api_key. Configuring an A2A task key locks down A2A
// traffic only; LAN Chat keeps zero-config interop via the community key
// unless lanchat.api_key is explicitly set.
func TestIssue1015LanChatKeyDecoupledFromA2A(t *testing.T) {
	// Zero config: community key (zero-config interop).
	cfg := Config{}
	if got := cfg.LanChat.EffectiveAPIKey(); got != DefaultA2AAPIKey {
		t.Fatalf("zero-config lanchat key = %q, want community key %q", got, DefaultA2AAPIKey)
	}

	// A2A key configured, no lanchat key: lanchat must STILL use the
	// community key — this is the regression being fixed (#1015).
	cfg = Config{}
	cfg.A2A.Auth.APIKey = "a2a-task-secret"
	if got := cfg.LanChat.EffectiveAPIKey(); got != DefaultA2AAPIKey {
		t.Fatalf("lanchat key inherited a2a.auth.api_key %q; want community key %q", got, DefaultA2AAPIKey)
	}

	// Dedicated lanchat key configured: it wins (opt-in lockdown, #986 semantics).
	cfg = Config{}
	cfg.LanChat.APIKey = "lan-secret"
	if got := cfg.LanChat.EffectiveAPIKey(); got != "lan-secret" {
		t.Fatalf("lanchat key = %q, want dedicated %q", got, "lan-secret")
	}

	// Both configured: each stays on its own key.
	cfg = Config{}
	cfg.A2A.Auth.APIKey = "a2a-task-secret"
	cfg.LanChat.APIKey = "lan-secret"
	if got := cfg.LanChat.EffectiveAPIKey(); got != "lan-secret" {
		t.Fatalf("lanchat key = %q, want %q (must not follow a2a key)", got, "lan-secret")
	}
}
