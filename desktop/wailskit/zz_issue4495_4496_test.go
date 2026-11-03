//go:build goolm

package wailskit

import (
	"os"
	"strings"
	"testing"
)

func TestIssue4495_AttachTunnelBrokerNilSessionGate(t *testing.T) {
	b, err := os.ReadFile("chat.go")
	if err != nil {
		t.Skip("source not readable in this environment")
	}
	s := string(b)
	if strings.Contains(s, "Title:     currentSes.Title,") {
		t.Fatal("#4495 regression: bare currentSes.Title deref is back")
	}

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
