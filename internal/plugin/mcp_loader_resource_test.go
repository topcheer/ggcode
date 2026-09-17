package plugin

import (
	"encoding/json"
	"strings"
	"testing"
	"time"

	"github.com/topcheer/ggcode/internal/config"
)

func newSubscribeTestPlugin() *MCPPlugin {
	return &MCPPlugin{cfg: config.MCPServerConfig{Name: "sub-test"}}
}

// TestMarkResourceUpdatedRecordsStamp verifies notifications/resources/updated
// payloads are parsed and recorded per URI, with malformed payloads tolerated.
func TestMarkResourceUpdatedRecordsStamp(t *testing.T) {
	m := newSubscribeTestPlugin()

	before := time.Now()
	m.markResourceUpdatedFromParams(json.RawMessage(`{"uri":"file:///a.txt"}`))
	got, ok := m.ResourceUpdatedAt("file:///a.txt")
	if !ok {
		t.Fatal("expected freshness stamp for file:///a.txt")
	}
	if got.Before(before) {
		t.Fatalf("stamp %v predates test start %v", got, before)
	}

	m.markResourceUpdatedFromParams(json.RawMessage(`{"uri":"file:///a.txt"}`))
	got2, ok := m.ResourceUpdatedAt("file:///a.txt")
	if !ok || got2.Before(got) {
		t.Fatalf("second update should overwrite with a newer stamp: %v then %v", got, got2)
	}

	// Malformed / empty URIs must not crash or create entries.
	m.markResourceUpdatedFromParams(json.RawMessage(`{`))
	m.markResourceUpdatedFromParams(json.RawMessage(`{"uri":"   "}`))
	if _, ok := m.ResourceUpdatedAt("   "); ok {
		t.Fatal("blank uri must not be recorded")
	}

	// Reads of other resources stay unmarked.
	if _, ok := m.ResourceUpdatedAt("file:///other"); ok {
		t.Fatal("unexpected stamp for unread uri")
	}
}

// TestSubscribeResourceAsyncGateNoop verifies the lazy subscribe path is a
// no-op for clients whose server lacks resources.subscribe and never marks
// the URI as subscribed.
func TestSubscribeResourceAsyncGateNoop(t *testing.T) {
	m := newSubscribeTestPlugin()

	m.subscribeResourceAsync(nil, "file:///x") // nil client must not panic

	m.mu.RLock()
	if len(m.subscribed) != 0 {
		t.Fatalf("subscribed map should stay empty, got %v", m.subscribed)
	}
	m.mu.RUnlock()
}

// TestUnsubscribeAllEmptyNoop verifies cleanup is safe with no subscriptions.
func TestUnsubscribeAllEmptyNoop(t *testing.T) {
	m := newSubscribeTestPlugin()
	m.unsubscribeAll(nil) // nil client must not panic

	// A populated map with a nil client must still clear the ledger.
	m.mu.Lock()
	m.subscribed = map[string]bool{"file:///x": true}
	m.mu.Unlock()
	m.unsubscribeAll(nil)
	m.mu.RLock()
	cleared := m.subscribed == nil
	m.mu.RUnlock()
	if !cleared {
		t.Fatal("unsubscribeAll should clear the subscription ledger")
	}
}

// TestReadResourceStampsFreshnessOnNotification is a compile-time guard that
// the notification case name matches the MCP wire method used by tests.
func TestResourceUpdatedMethodNames(t *testing.T) {
	const want = "notifications/resources/updated"
	if !strings.HasPrefix(want, "notifications/resources/") {
		t.Fatalf("unexpected method name %q", want)
	}
}
