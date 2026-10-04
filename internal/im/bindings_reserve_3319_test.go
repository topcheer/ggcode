package im

// #3319: two ggcode instances (TUI + daemon) each hold divergent in-memory
// PassiveReplyCount views of the same JSON binding store. Reserving seqs
// must serialize under the cross-process file lock so both instances never
// emit the same (msg_id, msg_seq) - QQ's server-side dedup key.

import (
	"path/filepath"
	"sync"
	"testing"
)

func TestReservePassiveSeqsCrossInstanceNoCollision(t *testing.T) {
	path := filepath.Join(t.TempDir(), "im-bindings.json")
	// Two store handles simulate two processes on the same file.
	storeA, err := NewJSONFileBindingStore(path)
	if err != nil {
		t.Fatal(err)
	}
	storeB, err := NewJSONFileBindingStore(path)
	if err != nil {
		t.Fatal(err)
	}
	if err := storeA.Save(ChannelBinding{
		Workspace:            "ws",
		Adapter:              "qq",
		LastInboundMessageID: "msg-1",
	}); err != nil {
		t.Fatal(err)
	}

	// Both instances concurrently reserve; every returned start seq must be
	// unique across the union of both instances' reservations.
	const perInstance = 8
	var mu sync.Mutex
	seen := map[int]bool{}
	var wg sync.WaitGroup
	for _, s := range []*JSONFileBindingStore{storeA, storeB} {
		wg.Add(1)
		go func(s *JSONFileBindingStore) {
			defer wg.Done()
			for range perInstance {
				start, err := s.ReservePassiveSeqs("ws", "msg-1", 1)
				if err != nil {
					t.Errorf("reserve: %v", err)
					return
				}
				mu.Lock()
				if seen[start] {
					t.Errorf("seq %d reserved twice across instances", start)
				}
				seen[start] = true
				mu.Unlock()
			}
		}(s)
	}
	wg.Wait()
	if len(seen) != perInstance*2 {
		t.Fatalf("expected %d unique seqs, got %d", perInstance*2, len(seen))
	}
}

func TestReservePassiveSeqsBatchStartAndMismatch(t *testing.T) {
	store, err := NewJSONFileBindingStore(filepath.Join(t.TempDir(), "im-bindings.json"))
	if err != nil {
		t.Fatal(err)
	}
	if err := store.Save(ChannelBinding{
		Workspace:            "ws",
		Adapter:              "qq",
		LastInboundMessageID: "msg-1",
	}); err != nil {
		t.Fatal(err)
	}
	start, err := store.ReservePassiveSeqs("ws", "msg-1", 3)
	if err != nil {
		t.Fatal(err)
	}
	if start != 1 {
		t.Fatalf("first batch start = %d, want 1", start)
	}
	start2, err := store.ReservePassiveSeqs("ws", "msg-1", 2)
	if err != nil {
		t.Fatal(err)
	}
	if start2 != 4 {
		t.Fatalf("second batch start = %d, want 4 (5-2+1)", start2)
	}
	// A different inbound message ID must not reserve against this binding.
	if _, err := store.ReservePassiveSeqs("ws", "msg-other", 1); err != ErrNoChannelBound {
		t.Fatalf("mismatched messageID err = %v, want ErrNoChannelBound", err)
	}
	// Invalid n is rejected without touching the counter.
	if _, err := store.ReservePassiveSeqs("ws", "msg-1", 0); err == nil {
		t.Fatal("n=0 must be rejected")
	}
	list, err := store.List()
	if err != nil || len(list) != 1 {
		t.Fatalf("list: %v %d", err, len(list))
	}
	if list[0].PassiveReplyCount != 5 {
		t.Fatalf("count = %d, want 5", list[0].PassiveReplyCount)
	}
}

func TestQQChunkCount(t *testing.T) {
	if got := qqChunkCount(false, "   "); got != 0 {
		t.Fatalf("blank text chunk count = %d, want 0", got)
	}
	if got := qqChunkCount(false, "hello"); got != 1 {
		t.Fatalf("short text chunk count = %d, want 1", got)
	}
}
