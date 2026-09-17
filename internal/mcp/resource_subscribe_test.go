package mcp

import (
	"context"
	"strings"
	"testing"
)

// TestResourceSubscribeCapabilityGate verifies that SubscribeResource and
// UnsubscribeResource refuse to send anything when the negotiated server
// capabilities do not advertise resources.subscribe (MCP 2025-06-18).
func TestResourceSubscribeCapabilityGate(t *testing.T) {
	c := NewClient("cap-test", "true", nil)

	c.setNegotiatedState("2025-06-18", ServerCaps{})
	if c.HasResourceSubscribe() {
		t.Fatal("HasResourceSubscribe = true with empty resources capability")
	}

	c.setNegotiatedState("2025-06-18", ServerCaps{Resources: &ResourcesCapability{ListChanged: true}})
	if c.HasResourceSubscribe() {
		t.Fatal("HasResourceSubscribe = true with subscribe=false")
	}

	err := c.SubscribeResource(context.Background(), "file:///x")
	if err == nil || !strings.Contains(err.Error(), "does not advertise resources.subscribe") {
		t.Fatalf("SubscribeResource on uncapable server: got %v, want capability gate error", err)
	}
	err = c.UnsubscribeResource(context.Background(), "file:///x")
	if err == nil || !strings.Contains(err.Error(), "does not advertise resources.subscribe") {
		t.Fatalf("UnsubscribeResource on uncapable server: got %v, want capability gate error", err)
	}

	c.setNegotiatedState("2025-06-18", ServerCaps{Resources: &ResourcesCapability{Subscribe: true}})
	if !c.HasResourceSubscribe() {
		t.Fatal("HasResourceSubscribe = false with subscribe=true capability")
	}
}
