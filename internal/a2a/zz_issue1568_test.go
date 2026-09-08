package a2a

import (
	"net"
	"testing"
)

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

// TestPushGuardBareIPEntryDelivery pins #1751 case 1: a bare-IP allowlist
// entry must survive the dial-time check - the Control hook only ever sees
// the resolved IP, and ipAllowed used to consult CIDRs alone, rejecting
// 100% of what registration exempted.
func TestPushGuardBareIPEntryDelivery(t *testing.T) {
	g := newPushGuard([]string{"10.1.2.3"})
	if !g.ipAllowed(net.ParseIP("10.1.2.3")) {
		t.Fatal("bare-IP entry must match at dial time")
	}
	if g.ipAllowed(net.ParseIP("10.1.2.4")) {
		t.Fatal("a different IP must not pass a bare-IP entry")
	}
}

// TestPushGuardHostnameEntryResolves: a hostname entry resolves at guard
// construction; its IPs match at dial time. Unresolvable hostnames
// (offline test envs are NOT one - use a reserved TLD) just contribute
// nothing, matching the old CIDR-parse-failure behavior.
func TestPushGuardHostnameEntryResolves(t *testing.T) {
	// localhost always resolves without network access.
	g := newPushGuard([]string{"localhost"})
	loopback := net.ParseIP("127.0.0.1")
	if !g.ipAllowed(loopback) {
		t.Fatal("resolved localhost entry must match its loopback IP at dial time")
	}
	// Reserved invalid TLD: resolution fails, entry contributes no IP -
	// but must not crash or poison the set.
	g2 := newPushGuard([]string{"nonexistent-invalid-tld-xyz.invalid"})
	if g2.ipAllowed(net.ParseIP("127.0.0.1")) {
		t.Fatal("unresolvable entry must not allow unrelated IPs")
	}
}
