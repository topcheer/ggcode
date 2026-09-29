package main

import (
	"path/filepath"
	"sync/atomic"
	"testing"
	"time"
)

// #2892: persist failures must not be silently swallowed - after bounded
// retries the failure is counted (and the originating peer closed on live
// connections). Closed store simulates disk failure (disk full / EIO).
func TestPersistEventDurableRetriesThenCountsFailure(t *testing.T) {
	s, err := openRelayStore(filepath.Join(t.TempDir(), "relay.db"), time.Hour)
	if err != nil {
		t.Fatal(err)
	}
	if err := s.Close(); err != nil {
		t.Fatal(err)
	}
	h := newHub(s)
	r := newRoom("tok-persist-fail")
	p := newPeer(h, r, "server", nil) // nil conn: closeWithReason no-ops safely

	h.persistEventDurable(p, "relay.persist-event", r.token,
		relayMessage{SessionID: "s1", EventID: "e1", AuthorityEpoch: 1}, []byte("{}"))

	deadline := time.Now().Add(5 * time.Second)
	for atomic.LoadUint64(&h.stats.persistErrors) < relayPersistMaxAttempts {
		if time.Now().After(deadline) {
			t.Fatalf("persistErrors = %d, want %d (retries exhausted)",
				atomic.LoadUint64(&h.stats.persistErrors), relayPersistMaxAttempts)
		}
		time.Sleep(10 * time.Millisecond)
	}
	if got := atomic.LoadUint64(&h.stats.persistedEvents); got != 0 {
		t.Fatalf("persistedEvents = %d, want 0 with a failing store", got)
	}
}

// #2892 happy path: one success record, zero error records.
func TestPersistEventDurableSuccess(t *testing.T) {
	s, err := openRelayStore(filepath.Join(t.TempDir(), "relay.db"), time.Hour)
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	h := newHub(s)
	r := newRoom("tok-persist-ok")
	p := newPeer(h, r, "server", nil)

	h.persistEventDurable(p, "relay.persist-event", r.token,
		relayMessage{SessionID: "s1", EventID: "e1", AuthorityEpoch: 1}, []byte("{}"))

	deadline := time.Now().Add(5 * time.Second)
	for atomic.LoadUint64(&h.stats.persistedEvents) < 1 {
		if time.Now().After(deadline) {
			t.Fatal("persistedEvents = 0, want 1")
		}
		time.Sleep(10 * time.Millisecond)
	}
	if got := atomic.LoadUint64(&h.stats.persistErrors); got != 0 {
		t.Fatalf("persistErrors = %d, want 0", got)
	}
}
