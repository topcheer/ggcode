package lanchat

// Issue #990 companion tests:
//  1. Store.Append must be a pure O_APPEND write so two Store instances
//     sharing one session file (TUI + daemon double-open) cannot lose
//     messages via read-modify-write clobbering.
//  2. Corrupt approval-policies.json must not be silently wiped by
//     SetApprovalPolicy write-back; Save must round-trip through Load.
//  3. SetSessionID must reject session IDs that could escape the
//     sessions/ directory via path separators or parent traversal.

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"
)

// --- Problem 1: O_APPEND append semantics ---

func TestStoreAppendTwoInstancesBothMessagesSurvive(t *testing.T) {
	dir := t.TempDir()
	storeA := NewStore(dir)
	storeB := NewStore(dir)

	msgA := Message{ID: "msg-a", FromNodeID: "node-a", FromNick: "alice", Content: "from instance A", Timestamp: time.Now().UnixMilli()}
	msgB := Message{ID: "msg-b", FromNodeID: "node-b", FromNick: "bob", Content: "from instance B", Timestamp: time.Now().UnixMilli()}

	// Sequential appends from two Store handles over the same file: the
	// in-process approximation of two processes sharing the session dir.
	if err := storeA.Append("sess", msgA); err != nil {
		t.Fatalf("storeA.Append: %v", err)
	}
	if err := storeB.Append("sess", msgB); err != nil {
		t.Fatalf("storeB.Append: %v", err)
	}

	msgs, err := storeB.LoadRecent("sess", 0)
	if err != nil {
		t.Fatalf("LoadRecent: %v", err)
	}
	if len(msgs) != 2 {
		t.Fatalf("got %d messages, want 2 (both instances must survive)", len(msgs))
	}
	if msgs[0].ID != "msg-a" || msgs[1].ID != "msg-b" {
		t.Fatalf("message order/IDs = [%s, %s], want [msg-a, msg-b]", msgs[0].ID, msgs[1].ID)
	}
}

func TestStoreAppendConcurrentInstancesNoLoss(t *testing.T) {
	dir := t.TempDir()
	storeA := NewStore(dir)
	storeB := NewStore(dir)

	const perInstance = 25
	var wg sync.WaitGroup
	for idx, store := range []*Store{storeA, storeB} {
		wg.Add(1)
		go func(s *Store, prefix int) {
			defer wg.Done()
			for i := 0; i < perInstance; i++ {
				msg := Message{
					ID:         fmt.Sprintf("msg-%d-%d", prefix, i),
					FromNodeID: "node-x",
					Content:    fmt.Sprintf("msg %d", i),
					Timestamp:  time.Now().UnixMilli(),
				}
				if err := s.Append("sess", msg); err != nil {
					t.Errorf("Append: %v", err)
					return
				}
			}
		}(store, idx)
	}
	wg.Wait()

	// perInstance*2 (50) <= maxHistoryPerSession (100), so the read-side
	// cap cannot mask any loss.
	msgs, err := storeA.LoadRecent("sess", 0)
	if err != nil {
		t.Fatalf("LoadRecent: %v", err)
	}
	if len(msgs) != perInstance*2 {
		t.Fatalf("got %d messages, want %d: concurrent appenders lost messages", len(msgs), perInstance*2)
	}
}

func TestStoreAppendHealsTornTailLine(t *testing.T) {
	dir := t.TempDir()
	store := NewStore(dir)

	// Simulate a writer that crashed mid-line: fragment without newline.
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "sess.jsonl"), []byte("{torn-fragment"), 0o644); err != nil {
		t.Fatal(err)
	}

	msg := Message{ID: "msg-ok", FromNodeID: "n", FromNick: "alice", Content: "after torn tail", Timestamp: time.Now().UnixMilli()}
	if err := store.Append("sess", msg); err != nil {
		t.Fatalf("Append: %v", err)
	}

	msgs, err := store.LoadRecent("sess", 0)
	if err != nil {
		t.Fatalf("LoadRecent: %v", err)
	}
	if len(msgs) != 1 || msgs[0].ID != "msg-ok" {
		t.Fatalf("got %d messages, want exactly the post-fragment message to survive", len(msgs))
	}
}

// --- Problem 2: approval policy persistence ---

func TestApprovalPoliciesCorruptFileNotWipedBySetApprovalPolicy(t *testing.T) {
	dir := t.TempDir()
	corrupt := []byte("{not valid json")
	if err := os.WriteFile(filepath.Join(dir, "approval-policies.json"), corrupt, 0o644); err != nil {
		t.Fatal(err)
	}

	// Load must surface the corruption, not silently return an empty map.
	if _, err := LoadApprovalPolicies(dir); err == nil {
		t.Fatal("LoadApprovalPolicies should fail on corrupt JSON")
	}

	hub := NewHub("node-A", "tui", "http://localhost:11111", "", NewStore(dir), WorkspaceMeta{})
	if hub.approvalPoliciesLoaded {
		t.Fatal("hub should mark approval policies as not-loaded on corrupt file")
	}

	// User sets a policy during the session. The in-memory map updates
	// (policy applies for this run) but must NOT be written back, or the
	// corrupt-but-recoverable file would be overwritten with an incomplete
	// map and the original policies permanently wiped.
	hub.SetApprovalPolicy("PeerHuman", "always")

	after, err := os.ReadFile(filepath.Join(dir, "approval-policies.json"))
	if err != nil {
		t.Fatal(err)
	}
	if string(after) != string(corrupt) {
		t.Fatalf("corrupt approval-policies.json was overwritten: %q", string(after))
	}
}

func TestApprovalPoliciesSaveLoadRoundTrip(t *testing.T) {
	dir := t.TempDir()
	in := map[string]string{"alice": "always", "bob": "never"}
	if err := SaveApprovalPolicies(dir, in); err != nil {
		t.Fatalf("SaveApprovalPolicies: %v", err)
	}
	if _, err := os.Stat(filepath.Join(dir, "approval-policies.json.tmp")); !os.IsNotExist(err) {
		t.Fatalf("tmp file should not linger after atomic rename")
	}
	out, err := LoadApprovalPolicies(dir)
	if err != nil {
		t.Fatalf("LoadApprovalPolicies: %v", err)
	}
	if len(out) != 2 || out["alice"] != "always" || out["bob"] != "never" {
		t.Fatalf("round-trip mismatch: %v", out)
	}
}

// --- Problem 3: sessionID sanitization ---

func TestSetSessionIDRejectsUnsafeIDs(t *testing.T) {
	base := t.TempDir()
	origStore := NewStore(t.TempDir())
	hub := NewHub("node-A", "tui", "http://localhost:11111", "", origStore, WorkspaceMeta{})

	for _, bad := range []string{"../evil", "a/b", "a\\b", "..", "sub/../../escape"} {
		hub.SetSessionID(base, bad)
		hub.mu.RLock()
		dir := hub.store.dir
		hub.mu.RUnlock()
		if dir != origStore.dir {
			t.Fatalf("sessionID %q should be rejected and keep the old store (store dir changed to %s)", bad, dir)
		}
		if _, err := os.Stat(filepath.Join(base, "sessions")); !os.IsNotExist(err) {
			t.Fatalf("sessionID %q should not create any sessions directory", bad)
		}
	}

	// Valid IDs still work.
	hub.SetSessionID(base, "session-1")
	hub.mu.RLock()
	dir := hub.store.dir
	hub.mu.RUnlock()
	want := filepath.Join(base, "sessions", "session-1")
	if dir != want {
		t.Fatalf("valid sessionID store dir = %q, want %q", dir, want)
	}
	if !strings.HasSuffix(dir, "session-1") {
		t.Fatalf("unexpected store dir %q", dir)
	}
}
