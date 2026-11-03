package lanchat

import (
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"testing"
	"time"
)

func newIssue988Hub(t *testing.T, apiKey string) *Hub {
	t.Helper()
	store := NewStore(filepath.Join(t.TempDir(), "store"))
	return NewHub("self-node", "tui", "http://127.0.0.1:1", apiKey, store, WorkspaceMeta{})
}

func TestIssue988OutboundUsesConfiguredAPIKey(t *testing.T) {
	var gotKey string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotKey = r.Header.Get("X-API-Key")
		w.WriteHeader(http.StatusOK)
	}))
	defer srv.Close()

	h := newIssue988Hub(t, "my-strong-secret")
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

func TestIssue988OutboundZeroConfigStillUsesCommunityKey(t *testing.T) {
	var gotKey string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotKey = r.Header.Get("X-API-Key")
		w.WriteHeader(http.StatusOK)
	}))
	defer srv.Close()

	h := newIssue988Hub(t, communityKey)
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
