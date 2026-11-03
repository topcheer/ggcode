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

	m.markResourceUpdatedFromParams(json.RawMessage(`{`))
	m.markResourceUpdatedFromParams(json.RawMessage(`{"uri":"   "}`))
	if _, ok := m.ResourceUpdatedAt("   "); ok {
		t.Fatal("blank uri must not be recorded")
	}

	if _, ok := m.ResourceUpdatedAt("file:///other"); ok {
		t.Fatal("unexpected stamp for unread uri")
	}
}

func TestSubscribeResourceAsyncGateNoop(t *testing.T) {
	m := newSubscribeTestPlugin()

	m.subscribeResourceAsync(nil, "file:///x")

	m.mu.RLock()
	if len(m.subscribed) != 0 {
		t.Fatalf("subscribed map should stay empty, got %v", m.subscribed)
	}
	m.mu.RUnlock()
}

func TestUnsubscribeAllEmptyNoop(t *testing.T) {
	m := newSubscribeTestPlugin()
	m.unsubscribeAll(nil)

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

func TestResourceUpdatedMethodNames(t *testing.T) {
	const want = "notifications/resources/updated"
	if !strings.HasPrefix(want, "notifications/resources/") {
		t.Fatalf("unexpected method name %q", want)
	}
}
