package wailskit

import (
	"strings"
	"testing"
)

// #4151: approval/ask_user request IDs must be unique even without a tunnel
// broker. The old handlers hardcoded "" in the no-broker case, so two
// concurrent approvals shared one map key in InteractionBroker: the first
// registration was overwritten (its waiter never woken, timing out after the
// 15-minute bound) or the first resolution silenced both.
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

// TestNextInteractionRequestIDConcurrent races the generator from many
// goroutines: parallel tool calls can fire approval handlers simultaneously,
// and every issued ID must still be unique.
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
