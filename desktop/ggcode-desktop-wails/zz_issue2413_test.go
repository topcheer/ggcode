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

	i := strings.Index(src, "#1787 case 1")
	if i < 0 {
		t.Fatal("overwrite block not found")
	}
	j := strings.Index(src[i:], "h.activeShare = &tunnelSessionRef{")
	if j < 0 {
		t.Fatal("overwrite block terminator not found")
	}
	block := src[i : i+j]
	if !strings.Contains(block, "oldRef.session.Stop()") {
		t.Fatal("#2413: overwrite path must stop the old session - the relay websocket lives on the session, broker.Stop alone leaves it alive")
	}

	brokerAt := strings.Index(block, "oldRef.broker.Stop()")
	sessAt := strings.Index(block, "oldRef.session.Stop()")
	if !(brokerAt >= 0 && brokerAt < sessAt) {
		t.Fatal("#2413: teardown must stop broker before session, mirroring TunnelHost.StopShare")
	}
}
