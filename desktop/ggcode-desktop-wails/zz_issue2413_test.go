package main

import (
	"os"
	"strings"
	"testing"
)

func TestIssue2413RefreshRestartSetsInFlightGuard(t *testing.T) {
	b, err := os.ReadFile("app.go")
	if err != nil {
		t.Fatal(err)
	}
	src := string(b)

	i := strings.Index(src, "refresh invite failed, restarting share")
	if i < 0 {
		t.Fatal("refresh-fail restart branch not found")
	}
	thCall := strings.Index(src[i:], "th.StartShare(agentruntime.ShareConfig{")
	if thCall < 0 {
		t.Fatal("th.StartShare call not found after restart branch")
	}
	branch := src[i : i+thCall]
	if !strings.Contains(branch, "a.tunnelStarting = true") {
		t.Fatal("#2413: restart branch must set tunnelStarting before the th.StartShare round-trip")
	}
	if !strings.Contains(branch, "a.tunnelStarting = false") {
		t.Fatal("#2413: restart branch must reset tunnelStarting via defer (same contract as the default branch)")
	}

	setAt := strings.Index(branch, "a.tunnelStarting = true")
	resetAt := strings.Index(branch, "a.tunnelStarting = false")
	if !(setAt < resetAt) {
		t.Fatal("#2413: defer reset must follow the flag set")
	}
}

func TestIssue2413OverwritePathStopsOldSession(t *testing.T) {
	b, err := os.ReadFile("../../internal/agentruntime/tunnel_host.go")
	if err != nil {
		t.Fatal(err)
	}
	src := string(b)

	// The overwrite path was refactored: the teardown logic moved into
	// teardownShareRef (its Stop ordering is asserted below). Anchor on code
	// symbols - the historical comment anchor was removed by the decomment
	// campaign and the inline Stops disappeared with the refactor.
	i := strings.Index(src, "if oldRef := h.activeShare; oldRef != nil {")
	if i < 0 {
		t.Fatal("overwrite block not found")
	}
	j := strings.Index(src[i:], "h.activeShare = &tunnelSessionRef{")
	if j < 0 {
		t.Fatal("overwrite block terminator not found")
	}
	block := src[i : i+j]
	if !strings.Contains(block, "h.teardownShareRef(oldRef, oldMgr)") {
		t.Fatal("#2413: overwrite path must tear down the old session - the relay websocket lives on the session, broker.Stop alone leaves it alive")
	}

	fn := strings.Index(src, "func (h *TunnelHost) teardownShareRef(")
	if fn < 0 {
		t.Fatal("teardownShareRef not found")
	}
	fnEnd := strings.Index(src[fn:], "\n}\n")
	body := src[fn : fn+fnEnd]
	brokerAt := strings.Index(body, "oldRef.broker.Stop()")
	sessAt := strings.Index(body, "oldRef.session.Stop()")
	if !(brokerAt >= 0 && brokerAt < sessAt) {
		t.Fatal("#2413: teardown must stop broker before session, mirroring TunnelHost.StopShare")
	}
}
