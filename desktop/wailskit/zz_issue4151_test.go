package wailskit

import (
	"strings"
	"testing"
)

func TestNextInteractionRequestIDUniqueNoBroker(t *testing.T) {
	b, err := NewChatBridge()
	if err != nil {
		t.Skipf("bridge init failed in test env: %v", err)
	}

	if broker := b.currentTunnelBroker(); broker != nil {
		t.Skip("tunnel broker present in test env; local-ID path not exercised")
	}

	seen := make(map[string]bool)
	for i := 0; i < 5; i++ {
		id := b.nextInteractionRequestID()
		if id == "" {
			t.Fatalf("request ID must never be empty (empty ID = #4151 collision class)")
		}
		if seen[id] {
			t.Fatalf("request IDs must be unique, duplicate %q", id)
		}
		seen[id] = true
		if !strings.HasPrefix(id, "req-") {
			t.Fatalf("local IDs must use the req- prefix (distinct from broker msg-N namespace), got %q", id)
		}
	}
}

func TestNextInteractionRequestIDConcurrent(t *testing.T) {
	b, err := NewChatBridge()
	if err != nil {
		t.Skipf("bridge init failed in test env: %v", err)
	}
	if broker := b.currentTunnelBroker(); broker != nil {
		t.Skip("tunnel broker present in test env; local-ID path not exercised")
	}

	const n = 32
	ids := make(chan string, n)
	for i := 0; i < n; i++ {
		go func() { ids <- b.nextInteractionRequestID() }()
	}
	seen := make(map[string]bool, n)
	for i := 0; i < n; i++ {
		id := <-ids
		if seen[id] {
			t.Fatalf("concurrent request IDs must be unique, duplicate %q", id)
		}
		seen[id] = true
	}
	if len(seen) != n {
		t.Fatalf("expected %d unique IDs, got %d", n, len(seen))
	}
}
