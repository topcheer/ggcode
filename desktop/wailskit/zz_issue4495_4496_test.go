//go:build goolm

package wailskit

import (
	"os"
	"strings"
	"testing"
)

// #4495 pin: the AttachTunnelBroker session-info push must not dereference
// the raw currentSes snapshot - it is nil until the first session exists
// (cold start + phone connecting to the share tunnel panicked on .Title
// while holding b.mu).
func TestIssue4495_AttachTunnelBrokerNilSessionGate(t *testing.T) {
	b, err := os.ReadFile("chat.go")
	if err != nil {
		t.Skip("source not readable in this environment")
	}
	s := string(b)
	if strings.Contains(s, "Title:     currentSes.Title,") {
		t.Fatal("#4495 regression: bare currentSes.Title deref is back")
	}
	// The gate must sit inside the AttachTunnelBroker function.
	idx := strings.Index(s, "func (b *ChatBridge) AttachTunnelBroker(")
	end := strings.Index(s[idx+1:], "\nfunc ")
	if end < 0 {
		end = len(s) - idx - 1
	}
	fn := s[idx : idx+1+end]
	if !strings.Contains(fn, "if currentSes != nil {") {
		t.Fatal("AttachTunnelBroker must nil-gate currentSes before reading Title")
	}
}

// #4496 pin: AddMCPServer's existing lookup must use the EFFECTIVE server
// set (yaml ∪ Claude-migrated), not the yaml-only cfg.MCPServers - a
// migrated-only http server edited with no form type used to default to
// stdio and get evicted by validation.
func TestIssue4496_AddMCPServerLookupUsesEffectiveSet(t *testing.T) {
	b, err := os.ReadFile("mcp.go")
	if err != nil {
		t.Skip("source not readable in this environment")
	}
	s := string(b)
	idx := strings.Index(s, "func AddMCPServer(")
	end := strings.Index(s[idx+1:], "\nfunc ")
	if end < 0 {
		end = len(s) - idx - 1
	}
	fn := s[idx : idx+1+end]
	if !strings.Contains(fn, "effectiveSessionServers(chatSnap, cfg)") {
		t.Fatal("#4496 regression: existing lookup no longer uses the merged set")
	}
	if strings.Contains(fn, "range cfg.MCPServers {\n\t\tif cfg.MCPServers[i].Name == name") {
		t.Fatal("#4496 regression: yaml-only lookup is back")
	}
}
