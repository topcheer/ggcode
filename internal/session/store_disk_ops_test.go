package session

import (
	"bufio"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/topcheer/ggcode/internal/provider"
)

// sa-137: disk-ops coverage round. Targets previously-uncovered public API:
// NewDefaultStore, Dir, RepairIndex, ListForWorkspace, AppendTunnelEventToDisk,
// AppendCheckpointToDisk, SessionLock.HolderPID. All tests use real file IO on
// t.TempDir() — no internal mocking — per the 12-Factor-Agents event-sourcing
// model (append-only JSONL is the source of truth; derived state is rebuilt).

// writeUserSession persists a session with a single user message so
// HasUserInteractionOnDisk reports true (RepairIndex adopts it into the index).
// The meta record is appended after the in-memory Messages are populated, so
// Workspace/Title survive loadSession reconstruction (same order as the
// production save path: meta carries the index-facing metadata).
func writeUserSession(t *testing.T, store *JSONLStore, ses *Session, text string) {
	t.Helper()
	msg := provider.Message{
		ID:      "msg_" + ses.ID + "_u1",
		Role:    "user",
		Content: []provider.ContentBlock{{Type: "text", Text: text}},
	}
	ses.Messages = []provider.Message{msg}
	if err := store.Save(ses); err != nil {
		t.Fatalf("Save: %v", err)
	}
	if err := store.AppendMetaToDisk(ses); err != nil {
		t.Fatalf("AppendMetaToDisk: %v", err)
	}
	if err := store.AppendMessagesBatchToDisk(ses, ses.Messages); err != nil {
		t.Fatalf("AppendMessagesBatchToDisk: %v", err)
	}
}

func TestNewDefaultStoreUsesHomeEnv(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)

	store, err := NewDefaultStore()
	if err != nil {
		t.Fatalf("NewDefaultStore: %v", err)
	}
	wantDir := filepath.Join(home, ".ggcode", "sessions")
	if store.Dir() != wantDir {
		t.Fatalf("Dir() = %q, want %q", store.Dir(), wantDir)
	}
	fi, err := os.Stat(wantDir)
	if err != nil {
		t.Fatalf("store dir not created: %v", err)
	}
	if !fi.IsDir() {
		t.Fatalf("%s is not a directory", wantDir)
	}
}

func TestDirReturnsStoreRoot(t *testing.T) {
	dir := t.TempDir()
	store, err := NewJSONLStore(dir)
	if err != nil {
		t.Fatal(err)
	}
	if got := store.Dir(); got != dir {
		t.Fatalf("Dir() = %q, want %q", got, dir)
	}
}

func TestRepairIndexAdoptsOrphanWithInteraction(t *testing.T) {
	dir := t.TempDir()
	store, err := NewJSONLStore(dir)
	if err != nil {
		t.Fatal(err)
	}

	// Orphan session file on disk with a user message, but no index entry:
	// Save() touches the file without touching the index, so removing the
	// index afterwards leaves a genuine orphan.
	ses := NewSession("zai", "cn-coding-openai", "glm-5-turbo")
	ses.Workspace = dir
	ses.Title = "orphan-with-interaction"
	writeUserSession(t, store, ses, "hello repair")
	if err := os.Remove(filepath.Join(dir, "index.json")); err != nil && !os.IsNotExist(err) {
		t.Fatalf("removing index: %v", err)
	}

	changed, err := store.RepairIndex()
	if err != nil {
		t.Fatalf("RepairIndex: %v", err)
	}
	if !changed {
		t.Fatal("RepairIndex reported no change for an orphan with user interaction")
	}

	idx, err := store.loadIndex()
	if err != nil {
		t.Fatal(err)
	}
	found := false
	for _, e := range idx {
		if e.ID == ses.ID {
			found = true
		}
	}
	if !found {
		t.Fatalf("orphan session %s not adopted into index (%d entries)", ses.ID, len(idx))
	}
}

func TestRepairIndexRemovesOrphanWithoutInteraction(t *testing.T) {
	dir := t.TempDir()
	store, err := NewJSONLStore(dir)
	if err != nil {
		t.Fatal(err)
	}

	// Touch-only session file: no user message ever appended. No List() warmup
	// here - List() itself repairs an empty index and would sweep the orphan
	// before we could observe RepairIndex doing it.
	ses := NewSession("zai", "cn-coding-openai", "glm-5-turbo")
	if err := store.Save(ses); err != nil {
		t.Fatal(err)
	}

	changed, err := store.RepairIndex()
	if err != nil {
		t.Fatalf("RepairIndex: %v", err)
	}
	if !changed {
		t.Fatal("expected change when an interaction-less orphan is swept")
	}
	if _, err := os.Stat(store.sessionPath(ses.ID)); !os.IsNotExist(err) {
		t.Fatalf("orphan file should have been removed, stat err = %v", err)
	}
}

func TestRepairIndexRemovesStaleEntries(t *testing.T) {
	dir := t.TempDir()
	store, err := NewJSONLStore(dir)
	if err != nil {
		t.Fatal(err)
	}

	ses := NewSession("zai", "cn-coding-openai", "glm-5-turbo")
	ses.Workspace = dir
	ses.Title = "stale-entry"
	writeUserSession(t, store, ses, "index me")
	idxPath := filepath.Join(dir, "index.json")
	if _, err := os.Stat(idxPath); err != nil {
		t.Fatalf("index should exist after AppendMetaToDisk: %v", err)
	}

	// Delete the backing file: the index entry is now stale. No List() call
	// here - its async maintenance (scheduleMaintenanceLocked) would prune
	// the stale entry first and leave RepairIndex nothing to reconcile.
	if err := os.Remove(store.sessionPath(ses.ID)); err != nil {
		t.Fatal(err)
	}
	changed, err := store.RepairIndex()
	if err != nil {
		t.Fatalf("RepairIndex: %v", err)
	}
	if !changed {
		t.Fatal("expected index change after deleting a session file")
	}
	idx, err := store.loadIndex()
	if err != nil {
		t.Fatal(err)
	}
	for _, e := range idx {
		if e.ID == ses.ID {
			t.Fatalf("stale entry for %s still present (%d entries)", ses.ID, len(idx))
		}
	}
}

func TestListForWorkspaceFiltersAndSorts(t *testing.T) {
	dir := t.TempDir()
	store, err := NewJSONLStore(dir)
	if err != nil {
		t.Fatal(err)
	}

	wsA := filepath.Join(dir, "proj-a")
	wsB := filepath.Join(dir, "proj-b")
	for _, d := range []string{wsA, wsB} {
		if err := os.MkdirAll(d, 0o755); err != nil {
			t.Fatal(err)
		}
	}

	oldA := NewSession("zai", "cn-coding-openai", "glm-5-turbo")
	oldA.Workspace = wsA
	oldA.Title = "a-old"
	writeUserSession(t, store, oldA, "a old")
	time.Sleep(20 * time.Millisecond)

	newA := NewSession("zai", "cn-coding-openai", "glm-5-turbo")
	newA.Workspace = wsA
	newA.Title = "a-new"
	writeUserSession(t, store, newA, "a new")
	time.Sleep(20 * time.Millisecond)

	inB := NewSession("zai", "cn-coding-openai", "glm-5-turbo")
	inB.Workspace = wsB
	inB.Title = "b-only"
	writeUserSession(t, store, inB, "b only")

	got, err := store.ListForWorkspace(wsA)
	if err != nil {
		t.Fatalf("ListForWorkspace: %v", err)
	}
	if len(got) != 2 {
		t.Fatalf("got %d sessions for wsA, want 2", len(got))
	}
	if got[0].ID != newA.ID || got[1].ID != oldA.ID {
		t.Fatalf("sort order wrong: [%s %s], want [%s %s]", got[0].ID, got[1].ID, newA.ID, oldA.ID)
	}
	if got[0].Title != "a-new" || got[1].Title != "a-old" {
		t.Fatalf("titles wrong: %q, %q", got[0].Title, got[1].Title)
	}

	gotB, err := store.ListForWorkspace(wsB)
	if err != nil {
		t.Fatalf("ListForWorkspace(wsB): %v", err)
	}
	if len(gotB) != 1 || gotB[0].ID != inB.ID {
		t.Fatalf("wsB listing wrong: %d sessions", len(gotB))
	}
}

func TestListForWorkspaceRepairsEmptyIndex(t *testing.T) {
	dir := t.TempDir()
	store, err := NewJSONLStore(dir)
	if err != nil {
		t.Fatal(err)
	}

	ses := NewSession("zai", "cn-coding-openai", "glm-5-turbo")
	ses.Workspace = dir
	ses.Title = "needs-repair"
	writeUserSession(t, store, ses, "resurrect me")
	// Simulate index corruption/loss: files exist on disk, index gone.
	if err := os.Remove(filepath.Join(dir, "index.json")); err != nil {
		t.Fatal(err)
	}

	got, err := store.ListForWorkspace(dir)
	if err != nil {
		t.Fatalf("ListForWorkspace: %v", err)
	}
	if len(got) != 1 || got[0].ID != ses.ID {
		t.Fatalf("expected repaired listing to return the session, got %d", len(got))
	}
}

func TestAppendTunnelEventToDiskWritesValidJSONL(t *testing.T) {
	dir := t.TempDir()
	store, err := NewJSONLStore(dir)
	if err != nil {
		t.Fatal(err)
	}

	ses := NewSession("zai", "cn-coding-openai", "glm-5-turbo")
	ses.Workspace = dir
	writeUserSession(t, store, ses, "tunnel test")
	idxPath := filepath.Join(dir, "index.json")
	idxBefore, err := os.ReadFile(idxPath)
	if err != nil {
		t.Fatalf("index should exist after first message append: %v", err)
	}

	events := []TunnelEvent{
		{EventID: "ev-1", StreamID: "s1", Type: "delta", Data: json.RawMessage(`{"n":1}`)},
		{EventID: "ev-2", Type: "done"},
	}
	for _, ev := range events {
		if err := store.AppendTunnelEventToDisk(ses, ev); err != nil {
			t.Fatalf("AppendTunnelEventToDisk(%s): %v", ev.EventID, err)
		}
	}

	// Tunnel events must not touch the index (cheap-append guarantee).
	idxAfter, err := os.ReadFile(idxPath)
	if err != nil {
		t.Fatal(err)
	}
	if string(idxBefore) != string(idxAfter) {
		t.Fatal("AppendTunnelEventToDisk must not modify the session index")
	}

	// Parse the raw JSONL: every line must be valid JSON and the tunnel
	// records must round-trip their fields exactly (append-only log is the
	// source of truth — load-time replay is delegated to the projection store).
	f, err := os.Open(store.sessionPath(ses.ID))
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()
	type teRec struct {
		Type        string `json:"type"`
		SessionID   string `json:"session_id"`
		TunnelEvent *struct {
			EventID  string          `json:"event_id"`
			StreamID string          `json:"stream_id,omitempty"`
			Type     string          `json:"type"`
			Data     json.RawMessage `json:"data,omitempty"`
		} `json:"tunnel_event,omitempty"`
	}
	var got []teRec
	sc := bufio.NewScanner(f)
	for sc.Scan() {
		line := strings.TrimSpace(sc.Text())
		if line == "" {
			continue
		}
		var r teRec
		if err := json.Unmarshal([]byte(line), &r); err != nil {
			t.Fatalf("corrupt JSONL line %q: %v", line, err)
		}
		if r.Type == "tunnel_event" {
			got = append(got, r)
		}
	}
	if err := sc.Err(); err != nil {
		t.Fatal(err)
	}
	if len(got) != 2 {
		t.Fatalf("got %d tunnel_event records, want 2", len(got))
	}
	if got[0].SessionID != ses.ID || got[0].TunnelEvent.EventID != "ev-1" ||
		got[0].TunnelEvent.StreamID != "s1" || got[0].TunnelEvent.Type != "delta" {
		t.Fatalf("ev-1 round-trip mismatch: %+v", got[0].TunnelEvent)
	}
	if string(got[0].TunnelEvent.Data) != `{"n":1}` {
		t.Fatalf("ev-1 data mismatch: %s", got[0].TunnelEvent.Data)
	}
	if got[1].TunnelEvent.EventID != "ev-2" || got[1].TunnelEvent.StreamID != "" || got[1].TunnelEvent.Type != "done" {
		t.Fatalf("ev-2 round-trip mismatch: %+v", got[1].TunnelEvent)
	}
}

func TestConcurrentAppendTunnelEventsJSONLIntegrity(t *testing.T) {
	dir := t.TempDir()
	store, err := NewJSONLStore(dir)
	if err != nil {
		t.Fatal(err)
	}

	ses := NewSession("zai", "cn-coding-openai", "glm-5-turbo")
	ses.Workspace = dir
	writeUserSession(t, store, ses, "concurrent")

	const workers = 8
	const perWorker = 12
	var wg sync.WaitGroup
	errCh := make(chan error, workers)
	for w := 0; w < workers; w++ {
		wg.Add(1)
		go func(w int) {
			defer wg.Done()
			for i := 0; i < perWorker; i++ {
				ev := TunnelEvent{
					EventID: ses.ID + "-w" + itoa(w) + "-i" + itoa(i),
					Type:    "delta",
				}
				if err := store.AppendTunnelEventToDisk(ses, ev); err != nil {
					errCh <- err
					return
				}
			}
		}(w)
	}
	wg.Wait()
	close(errCh)
	for err := range errCh {
		t.Fatalf("concurrent append failed: %v", err)
	}

	// Every line in the file must be valid JSON (no interleaved/torn writes)
	// and all worker×iteration EventIDs must be present exactly once.
	f, err := os.Open(store.sessionPath(ses.ID))
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()
	seen := make(map[string]int)
	sc := bufio.NewScanner(f)
	for sc.Scan() {
		line := strings.TrimSpace(sc.Text())
		if line == "" {
			continue
		}
		var r struct {
			Type        string `json:"type"`
			TunnelEvent *struct {
				EventID string `json:"event_id"`
			} `json:"tunnel_event,omitempty"`
		}
		if err := json.Unmarshal([]byte(line), &r); err != nil {
			t.Fatalf("torn/corrupt line under concurrency: %q: %v", line, err)
		}
		if r.Type == "tunnel_event" {
			seen[r.TunnelEvent.EventID]++
		}
	}
	if err := sc.Err(); err != nil {
		t.Fatal(err)
	}
	if len(seen) != workers*perWorker {
		t.Fatalf("got %d unique event IDs, want %d", len(seen), workers*perWorker)
	}
	for w := 0; w < workers; w++ {
		for i := 0; i < perWorker; i++ {
			id := ses.ID + "-w" + itoa(w) + "-i" + itoa(i)
			if seen[id] != 1 {
				t.Fatalf("event %s count = %d, want 1", id, seen[id])
			}
		}
	}
}

func itoa(n int) string {
	if n == 0 {
		return "0"
	}
	var b [20]byte
	pos := len(b)
	for n > 0 {
		pos--
		b[pos] = byte('0' + n%10)
		n /= 10
	}
	return string(b[pos:])
}

func TestAppendCheckpointToDiskRoundTrip(t *testing.T) {
	dir := t.TempDir()
	store, err := NewJSONLStore(dir)
	if err != nil {
		t.Fatal(err)
	}

	ses := NewSession("zai", "cn-coding-openai", "glm-5-turbo")
	ses.Workspace = dir
	m1 := provider.Message{ID: "msg_cp_m1", Role: "user", Content: []provider.ContentBlock{{Type: "text", Text: "first"}}}
	m2 := provider.Message{ID: "msg_cp_m2", Role: "assistant", Content: []provider.ContentBlock{{Type: "text", Text: "summary anchor"}}}
	m3 := provider.Message{ID: "msg_cp_m3", Role: "user", Content: []provider.ContentBlock{{Type: "text", Text: "last before cp"}}}
	if err := store.Save(ses); err != nil {
		t.Fatal(err)
	}
	if err := store.AppendMessagesBatchToDisk(ses, []provider.Message{m1, m2, m3}); err != nil {
		t.Fatal(err)
	}

	updatedAtBefore := ses.UpdatedAt
	if err := store.AppendCheckpointToDisk(ses, m2.ID, m3.ID, 4321); err != nil {
		t.Fatalf("AppendCheckpointToDisk: %v", err)
	}
	// ToDisk variant must not mutate the caller's Session object.
	if !ses.UpdatedAt.Equal(updatedAtBefore) {
		t.Fatal("AppendCheckpointToDisk must not modify the Session object")
	}

	// Checkpoint always updates the index — it must exist and list the session.
	if _, err := os.Stat(filepath.Join(dir, "index.json")); err != nil {
		t.Fatalf("index missing after checkpoint: %v", err)
	}

	// Round-trip: derived state rebuilt from the append-only log.
	loaded, err := store.Load(ses.ID)
	if err != nil {
		t.Fatal(err)
	}
	if loaded.CheckpointTokens != 4321 {
		t.Fatalf("CheckpointTokens = %d, want 4321", loaded.CheckpointTokens)
	}
	if loaded.CheckpointMessageCount != 1 {
		t.Fatalf("CheckpointMessageCount = %d, want 1 (summary msg only)", loaded.CheckpointMessageCount)
	}
	if len(loaded.ContextMessages) != 1 || loaded.ContextMessages[0].ID != m2.ID {
		t.Fatalf("ContextMessages = %+v, want [%s]", loaded.ContextMessages, m2.ID)
	}

	// A message appended after the checkpoint belongs to the post-CP window.
	m4 := provider.Message{ID: "msg_cp_m4", Role: "user", Content: []provider.ContentBlock{{Type: "text", Text: "after cp"}}}
	if err := store.AppendMessagesBatchToDisk(loaded, []provider.Message{m4}); err != nil {
		t.Fatal(err)
	}
	loaded2, err := store.Load(ses.ID)
	if err != nil {
		t.Fatal(err)
	}
	if len(loaded2.ContextMessages) != 2 || loaded2.ContextMessages[1].ID != m4.ID {
		t.Fatalf("post-CP message not restored: %+v", loaded2.ContextMessages)
	}
	if loaded2.CheckpointMessageCount != 2 {
		t.Fatalf("CheckpointMessageCount = %d, want 2", loaded2.CheckpointMessageCount)
	}
	// Full history is never truncated by compaction bookkeeping.
	if len(loaded2.Messages) != 4 {
		t.Fatalf("Messages = %d, want 4 (full log preserved)", len(loaded2.Messages))
	}
}

func TestSessionLockHolderPID(t *testing.T) {
	// Nil receiver: all accessors must be nil-safe.
	var nilLock *SessionLock
	if got := nilLock.HolderPID(); got != 0 {
		t.Fatalf("nil HolderPID = %d, want 0", got)
	}
	if nilLock.Acquired() {
		t.Fatal("nil lock must not report acquired")
	}
	if got := nilLock.SessionID(); got != "" {
		t.Fatalf("nil SessionID = %q, want empty", got)
	}

	dir := t.TempDir()
	id := "lock-pid-target"

	// First acquire: we hold it, so holder PID is 0 (unknown/not-other).
	first, err := TryAcquireSessionLock(dir, id)
	if err != nil {
		t.Fatalf("first acquire: %v", err)
	}
	if !first.Acquired() {
		t.Fatal("first acquire must succeed on a fresh dir")
	}
	if got := first.HolderPID(); got != 0 {
		t.Fatalf("self-held HolderPID = %d, want 0", got)
	}
	if got := first.SessionID(); got != id {
		t.Fatalf("SessionID = %q, want %q", got, id)
	}

	// Second acquire on the same lock file must fail (flock is exclusive
	// across open file descriptions, even within one process) and must
	// surface the holder's PID, which the first holder wrote to the file.
	second, err := TryAcquireSessionLock(dir, id)
	if err != nil {
		t.Fatalf("second acquire: %v", err)
	}
	if second.Acquired() {
		t.Fatal("second acquire must not succeed while the first is held")
	}
	if got := second.HolderPID(); got != os.Getpid() {
		t.Fatalf("HolderPID = %d, want %d (our PID read from the lock file)", got, os.Getpid())
	}
	if got := second.SessionID(); got != id {
		t.Fatalf("failed-lock SessionID = %q, want %q", got, id)
	}

	// Release frees the slot; a fresh acquire must succeed again.
	first.Release()
	third, err := TryAcquireSessionLock(dir, id)
	if err != nil {
		t.Fatalf("acquire after release: %v", err)
	}
	if !third.Acquired() {
		t.Fatal("acquire after release must succeed")
	}
	third.Release()
}
