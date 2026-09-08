package a2a

import (
	"net"
	"testing"
)

// #1889 case 1: hostname entries must re-resolve (rate-limited) when a
// dial-time check misses the startup snapshot - a DHCP lease change moves
// the LAN collector to an IP the snapshot cannot know, and delivery used
// to be rejected 100% again until a server restart.
func TestPushGuardReResolvesOnMiss(t *testing.T) {
	g := newPushGuard([]string{"collector.invalid"})
	// Startup resolution of .invalid fails (logged) - snapshot empty.
	if g.ipAllowed(net.ParseIP("10.99.99.99")) {
		t.Fatal("unrelated IP must not be allowed")
	}
	if len(g.resolveNames) == 0 || len(g.allowHostIPs) != 0 {
		t.Fatalf("expected unresolved name retained, got names=%v ips=%v", g.resolveNames, g.allowHostIPs)
	}
	// Simulate DNS healing: pretend the resolver now yields 10.99.99.99.
	g.mu.Lock()
	g.allowHostIPs = append(g.allowHostIPs, net.ParseIP("10.99.99.99"))
	g.mu.Unlock()
	if !g.ipAllowed(net.ParseIP("10.99.99.99")) {
		t.Fatal("refreshed IP must be allowed")
	}
}

// Bare-IP entries keep working without any DNS involvement.
func TestPushGuardBareIPStable(t *testing.T) {
	g := newPushGuard([]string{"127.0.0.1", "fd00::1"})
	if !g.ipAllowed(net.ParseIP("127.0.0.1")) {
		t.Fatal("bare IPv4 entry must be allowed at dial time")
	}
	if !g.ipAllowed(net.ParseIP("fd00::1")) {
		t.Fatal("bare IPv6 entry must be allowed at dial time")
	}
	// A neighbor IP is NOT allowed - no widening.
	if g.ipAllowed(net.ParseIP("127.0.0.2")) {
		t.Fatal("neighbor IP must stay rejected")
	}
}
