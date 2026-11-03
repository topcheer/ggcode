package a2a

import (
	"net"
	"testing"
)

func TestPushGuardReResolvesOnMiss(t *testing.T) {
	g := newPushGuard([]string{"collector.invalid"})
	if g.ipAllowed(net.ParseIP("10.99.99.99")) {
		t.Fatal("unrelated IP must not be allowed")
	}
	if len(g.resolveNames) == 0 || len(g.allowHostIPs) != 0 {
		t.Fatalf("expected unresolved name retained, got names=%v ips=%v", g.resolveNames, g.allowHostIPs)
	}
	g.mu.Lock()
	g.allowHostIPs = append(g.allowHostIPs, net.ParseIP("10.99.99.99"))
	g.mu.Unlock()
	if !g.ipAllowed(net.ParseIP("10.99.99.99")) {
		t.Fatal("refreshed IP must be allowed")
	}
}

func TestPushGuardBareIPStable(t *testing.T) {
	g := newPushGuard([]string{"127.0.0.1", "fd00::1"})
	if !g.ipAllowed(net.ParseIP("127.0.0.1")) {
		t.Fatal("bare IPv4 entry must be allowed at dial time")
	}
	if !g.ipAllowed(net.ParseIP("fd00::1")) {
		t.Fatal("bare IPv6 entry must be allowed at dial time")
	}
	if g.ipAllowed(net.ParseIP("127.0.0.2")) {
		t.Fatal("neighbor IP must stay rejected")
	}
}
