package lanchat

import (
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"testing"
	"time"
)

// newIssue988Hub builds a Hub whose apiKey is set explicitly (custom-key
// topology) or left as the zero-config community key.
func newIssue988Hub(t *testing.T, apiKey string) *Hub {
	t.Helper()
	store := NewStore(filepath.Join(t.TempDir(), "store"))
	return NewHub("self-node", "tui", "http://127.0.0.1:1", apiKey, store, WorkspaceMeta{})
}

// TestIssue988OutboundUsesConfiguredAPIKey pins the #988 fix: every outbound
// send path must carry the CONFIGURED api key (h.APIKey()), not the hardcoded
// community key. Before the fix, two peers that both configured the same
// custom api_key could not talk: the sender's outbound requests carried
// communityKey and the receiver (post-#986) rejected it with 401 — the
// "team shares a custom key" topology silently broke while zero-config kept
// working.
func TestIssue988OutboundUsesConfiguredAPIKey(t *testing.T) {
	var gotKey string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotKey = r.Header.Get("X-API-Key")
		w.WriteHeader(http.StatusOK)
	}))
	defer srv.Close()

	h := newIssue988Hub(t, "my-strong-secret") // custom key configured
	peer := Participant{
		NodeID:   "peer-1",
		Endpoint: srv.URL,
	}
	h.sendPresence(peer)

	deadline := time.Now().Add(2 * time.Second)
	for gotKey == "" && time.Now().Before(deadline) {
		time.Sleep(10 * time.Millisecond)
	}
	if gotKey != "my-strong-secret" {
		t.Fatalf("outbound presence carried X-API-Key %q, want configured key %q (community-key hardcode regressed)", gotKey, "my-strong-secret")
	}
}

// TestIssue988OutboundZeroConfigStillUsesCommunityKey guards the
// complementary side: with no custom key configured, h.APIKey() returns the
// community key, so zero-config interop is unchanged by the fix.
func TestIssue988OutboundZeroConfigStillUsesCommunityKey(t *testing.T) {
	var gotKey string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotKey = r.Header.Get("X-API-Key")
		w.WriteHeader(http.StatusOK)
	}))
	defer srv.Close()

	h := newIssue988Hub(t, communityKey) // zero-config: apiKey == communityKey
	peer := Participant{
		NodeID:   "peer-2",
		Endpoint: srv.URL,
	}
	h.sendPresence(peer)

	deadline := time.Now().Add(2 * time.Second)
	for gotKey == "" && time.Now().Before(deadline) {
		time.Sleep(10 * time.Millisecond)
	}
	if gotKey != communityKey {
		t.Fatalf("zero-config outbound carried X-API-Key %q, want community key %q", gotKey, communityKey)
	}
}
