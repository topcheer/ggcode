package a2a

import "testing"

// Regression for #1568-A: the Control-hook IP pinning had no allowlist
// exemption - registration exempts allowlisted hosts/CIDRs but delivery
// blocked 100% of private-IP callbacks, the advertised main use case
// (private collectors over 10.x/collector.lan).
func TestPushControlHookExemptsAllowlistedPrivateIP(t *testing.T) {
	hook := pushControlHook(newPushGuard([]string{"10.0.0.0/8", "collector.lan"}))

	// Allowlisted private range: must pass (was blocked as "disallowed IP").
	if err := hook("tcp", "10.1.2.3:443", nil); err != nil {
		t.Fatalf("allowlisted 10/8 must be exempt, got: %v", err)
	}
	// Non-allowlisted loopback/link-local: still pinned.
	if err := hook("tcp", "127.0.0.1:80", nil); err == nil {
		t.Fatal("loopback must stay blocked without an explicit allowlist entry")
	}
	if err := hook("tcp", "169.254.169.254:80", nil); err == nil {
		t.Fatal("link-local metadata IP must stay blocked")
	}
	// Public IP unaffected either way.
	if err := hook("tcp", "93.184.216.34:443", nil); err != nil {
		t.Fatalf("public IP must pass, got: %v", err)
	}
}
