//go:build goolm

package agentruntime

import (
	"os"
	"strings"
	"testing"
)

func TestIssue4503_CloseStopsDetachedBroker(t *testing.T) {
	b, err := os.ReadFile("tunnel_host.go")
	if err != nil {
		t.Skip("source not readable in this environment")
	}
	s := string(b)
	idx := strings.Index(s, "func (h *TunnelHost) Close() {")
	if idx < 0 {
		t.Fatal("Close not found")
	}
	end := strings.Index(s[idx+1:], "\nfunc ")
	if end < 0 {
		end = len(s) - idx - 1
	}
	body := s[idx : idx+1+end]
	if !strings.Contains(body, "ref.broker != nil") || !strings.Contains(body, "ref.broker.Stop()") {
		t.Fatal("#4503 regression: Close no longer stops the detached share broker")
	}
}
