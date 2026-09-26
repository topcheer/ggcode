package session

// sa-146 coverage-net round: gap-filling behavioral tests for internal/session.
//
// Theory anchors (2025-2026 durable agent session literature):
//   - "Crash-Safe Incremental Persistence for Real-Time Agent Sessions" (zylos.ai,
//     2026-07): event-sourced append-only log, torn-write tolerance, idempotent
//     replay by sequence numbers.
//   - "AI Agent Crash Recovery with Journals & Replay" (promptise docs): turn a
//     fatal event into a resumable one via journal + checkpoint.
//   - "USL - Universal Session Log" (agent-session-protocol.github.io):
//     local-first, append-only, crash-recoverable agent session store.
//
// The store already implements these patterns (appendRecordLines + flock,
// terminateTornTail #657, RepairIndex, AppendCheckpointToDisk, index rebuild);
// these tests pin the guarantees so regressions surface immediately.
// Test-only round: no production code changes.

import (
	"bytes"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/topcheer/ggcode/internal/metrics"
	"github.com/topcheer/ggcode/internal/provider"
)

func sa146TempDir(t *testing.T) string {
	t.Helper()
	base := t.TempDir()
	dir := filepath.Join(base, "store")
	if err := os.MkdirAll(dir, 0o700); err != nil {
		t.Fatalf("mkdir store: %v", err)
	}
	// Async store goroutines (runMaintenance, backfill*) can still be writing
	// (saveIndex tmp files, index rebuilds) when cleanup starts, making
	// RemoveAll fail with "directory not empty". Retry briefly; give up
	// silently either way - this is cleanup, not an assertion.
	t.Cleanup(func() {
		deadline := time.Now().Add(2 * time.Second)
		for {
			err := os.RemoveAll(dir)
			if err == nil || !strings.Contains(err.Error(), "directory not empty") || !time.Now().Before(deadline) {
				return
			}
			time.Sleep(20 * time.Millisecond)
		}
	})
	return dir
}

func sa146TestStore(t *testing.T) *JSONLStore {
	t.Helper()
	s, err := NewJSONLStore(sa146TempDir(t))
	if err != nil {
		t.Fatalf("NewJSONLStore: %v", err)
	}
	return s
}

// sa146MsgSeq yields unique message IDs. ID-less messages make every
// loadSession schedule backfillIDsAsync, whose tmp-file writes race
// t.TempDir cleanup - IDs keep the async path dormant.
var sa146MsgSeq atomic.Int64

func sa146UserMsg(text string) provider.Message {
	return provider.Message{
		ID:   fmt.Sprintf("sa146-m%d", sa146MsgSeq.Add(1)),
		Role: "user",
		Content: []provider.ContentBlock{
			{Type: "text", Text: text},
		},
	}
}

// --- constructors / accessors ---

func TestSA146NewDefaultStoreAndDir(t *testing.T) {
	s, err := NewDefaultStore()
	if err != nil {
		t.Fatalf("NewDefaultStore: %v", err)
	}
	if s == nil {
		t.Fatal("NewDefaultStore returned nil store")
	}
	wantSuffix := filepath.Join(".ggcode", "sessions")
	if !strings.HasSuffix(s.Dir(), wantSuffix) {
		t.Errorf("Dir() = %q, want suffix %q", s.Dir(), wantSuffix)
	}
	if fi, err := os.Stat(s.Dir()); err != nil || !fi.IsDir() {
		t.Errorf("default dir %s not created: %v", s.Dir(), err)
	}
}

func TestSA146NewJSONLStoreRejectsFileParent(t *testing.T) {
	base := sa146TestStore(t)
	blocker := filepath.Join(base.Dir(), "blocker.txt")
	if err := os.WriteFile(blocker, []byte("x"), 0o600); err != nil {
		t.Fatalf("setup: %v", err)
	}
	if _, err := NewJSONLStore(filepath.Join(blocker, "sub")); err == nil {
		t.Fatal("NewJSONLStore under a regular file should fail")
	}
}

// --- ListForWorkspace: filter, sort, stale-index repair ---

func TestSA146ListForWorkspaceFilterAndSort(t *testing.T) {
	s := sa146TestStore(t)
	now := time.Now()
	wsA := "sa146-ws-alpha"
	wsB := "sa146-ws-beta"

	mk := func(id, ws string, updated time.Time) *Session {
		ses := &Session{ID: id, CreatedAt: updated.Add(-time.Hour), UpdatedAt: updated, Workspace: ws}
		ses.Messages = []provider.Message{sa146UserMsg("hello from " + id)}
		saveFullForTest(t, s, ses)
		return ses
	}
	mk(generateID(), wsA, now.Add(2*time.Hour)) // newest
	mk(generateID(), wsB, now.Add(90*time.Minute))
	mk(generateID(), wsA, now.Add(time.Hour)) // oldest

	got, err := s.ListForWorkspace(wsA)
	if err != nil {
		t.Fatalf("ListForWorkspace: %v", err)
	}
	if len(got) != 2 {
		t.Fatalf("got %d sessions for wsA, want 2", len(got))
	}
	if !got[0].UpdatedAt.After(got[1].UpdatedAt) {
		t.Errorf("not sorted by UpdatedAt desc: %v vs %v", got[0].UpdatedAt, got[1].UpdatedAt)
	}
	for _, ses := range got {
		if ses.Workspace != wsA {
			t.Errorf("leaked workspace %q into wsA result", ses.Workspace)
		}
	}
}

func TestSA146ListForWorkspaceRepairsEmptyIndex(t *testing.T) {
	s := sa146TestStore(t)
	ws := "sa146-ws-repair"
	ses := &Session{ID: generateID(), CreatedAt: time.Now(), UpdatedAt: time.Now(), Workspace: ws}
	ses.Messages = []provider.Message{sa146UserMsg("need index repair")}
	saveFullForTest(t, s, ses)

	// Wipe the index to a valid-but-empty array: the disk file exists but the
	// index no longer knows the workspace. ListForWorkspace must rebuild.
	if err := os.WriteFile(s.indexPath(), []byte("[]"), 0o600); err != nil {
		t.Fatalf("write empty index: %v", err)
	}
	got, err := s.ListForWorkspace(ws)
	if err != nil {
		t.Fatalf("ListForWorkspace after wipe: %v", err)
	}
	if len(got) != 1 || got[0].ID != ses.ID {
		t.Fatalf("repair path lost session: got %v", got)
	}
}

// --- RepairIndex: phantom entries removed, orphan files adopted ---

func TestSA146RepairIndexRemovesPhantomEntries(t *testing.T) {
	s := sa146TestStore(t)
	ses := &Session{ID: generateID(), CreatedAt: time.Now(), UpdatedAt: time.Now()}
	ses.Messages = []provider.Message{sa146UserMsg("phantom check")}
	saveFullForTest(t, s, ses)

	phantom := indexEntry{ID: "phantom-does-not-exist", Title: "ghost"}
	idx := []indexEntry{{ID: ses.ID, Title: "real"}, phantom}
	data, _ := json.Marshal(idx)
	if err := os.WriteFile(s.indexPath(), data, 0o600); err != nil {
		t.Fatalf("write index: %v", err)
	}

	changed, err := s.RepairIndex()
	if err != nil {
		t.Fatalf("RepairIndex: %v", err)
	}
	if !changed {
		t.Error("RepairIndex should report change when dropping a phantom entry")
	}
	after, err := s.loadIndexFromDisk()
	if err != nil {
		t.Fatalf("loadIndexFromDisk: %v", err)
	}
	if len(after) != 1 || after[0].ID != ses.ID {
		t.Errorf("index after repair = %+v, want only real entry %s", after, ses.ID)
	}
}

func TestSA146RepairIndexAdoptsOrphanDiskFiles(t *testing.T) {
	s := sa146TestStore(t)
	ses := &Session{ID: generateID(), CreatedAt: time.Now(), UpdatedAt: time.Now()}
	ses.Messages = []provider.Message{sa146UserMsg("orphan adoption")}
	saveFullForTest(t, s, ses)

	// Drop the index entirely: the JSONL file becomes an orphan on disk.
	if err := os.Remove(s.indexPath()); err != nil {
		t.Fatalf("remove index: %v", err)
	}
	changed, err := s.RepairIndex()
	if err != nil {
		t.Fatalf("RepairIndex: %v", err)
	}
	if !changed {
		t.Error("RepairIndex should report change when adopting an orphan file")
	}
	listed, err := s.List()
	if err != nil {
		t.Fatalf("List: %v", err)
	}
	if len(listed) != 1 || listed[0].ID != ses.ID {
		t.Fatalf("orphan session not visible after repair: %d entries", len(listed))
	}
}

// --- Corrupt index: loadIndex must self-heal (durability anchor) ---

func TestSA146LoadIndexCorruptSelfHeals(t *testing.T) {
	s := sa146TestStore(t)
	ses := &Session{ID: generateID(), CreatedAt: time.Now(), UpdatedAt: time.Now()}
	ses.Messages = []provider.Message{sa146UserMsg("corrupt index healing")}
	saveFullForTest(t, s, ses)

	if err := os.WriteFile(s.indexPath(), []byte("{not json"), 0o600); err != nil {
		t.Fatalf("write corrupt index: %v", err)
	}
	idx, err := s.loadIndex() // canRepair=true path
	if err != nil {
		t.Fatalf("loadIndex: %v", err)
	}
	if len(idx) != 1 || idx[0].ID != ses.ID {
		t.Fatalf("loadIndex after corruption = %+v, want rebuilt entry", idx)
	}

	// loadIndexFromDisk (no-recovery sibling) must return nil,nil on garbage.
	if err := os.WriteFile(s.indexPath(), []byte("{still not json"), 0o600); err != nil {
		t.Fatalf("rewrite corrupt index: %v", err)
	}
	diskIdx, err := s.loadIndexFromDisk()
	if err != nil || diskIdx != nil {
		t.Errorf("loadIndexFromDisk on corrupt file = (%v, %v), want (nil, nil)", diskIdx, err)
	}
}

// --- AppendCheckpointToDisk: checkpoint record round-trip ---

func TestSA146AppendCheckpointToDiskRoundTrip(t *testing.T) {
	s := sa146TestStore(t)
	ses := &Session{ID: generateID(), CreatedAt: time.Now(), UpdatedAt: time.Now()}
	ses.Messages = []provider.Message{
		{ID: "sa146-msg-a1", Role: "user", Content: []provider.ContentBlock{{Type: "text", Text: "question one"}}},
		{ID: "sa146-msg-b2", Role: "assistant", Content: []provider.ContentBlock{{Type: "text", Text: "answer one"}}},
	}
	saveFullForTest(t, s, ses)
	if err := s.AppendCheckpointToDisk(ses, ses.Messages[0].ID, ses.Messages[1].ID, 4321); err != nil {
		t.Fatalf("AppendCheckpointToDisk: %v", err)
	}

	raw, err := os.ReadFile(s.sessionPath(ses.ID))
	if err != nil {
		t.Fatalf("read session file: %v", err)
	}
	if !bytes.Contains(raw, []byte(`"checkpoint"`)) || !bytes.Contains(raw, []byte("4321")) {
		t.Error("checkpoint record missing from session JSONL")
	}

	reloaded, err := s.Load(ses.ID)
	if err != nil {
		t.Fatalf("Load after checkpoint: %v", err)
	}
	if len(reloaded.Messages) != 2 {
		t.Errorf("messages after checkpoint = %d, want 2", len(reloaded.Messages))
	}
	if reloaded.CheckpointTokens != 4321 {
		t.Errorf("CheckpointTokens = %d, want 4321 (applied from last checkpoint record)", reloaded.CheckpointTokens)
	}
	// Index must have been refreshed (checkpoint resets the debounce).
	idx, err := s.loadIndexFromDisk()
	if err != nil || len(idx) == 0 {
		t.Fatalf("index after checkpoint empty: %v %v", idx, err)
	}
}

// --- AppendTunnelEventToDisk: legacy writer persists, Load skips silently ---
// Contract (loadSession): tunnel_event lines are no longer replayed into
// TunnelEvents - the projection store is the sole source of tunnel history.
// Load must tolerate them silently and keep the session loadable.

func TestSA146AppendTunnelEventToDiskRoundTrip(t *testing.T) {
	s := sa146TestStore(t)
	ses := &Session{ID: generateID(), CreatedAt: time.Now(), UpdatedAt: time.Now()}
	ses.Messages = []provider.Message{sa146UserMsg("tunnel events")}
	saveFullForTest(t, s, ses)

	ev1 := TunnelEvent{EventID: "e1", StreamID: "s1", Type: "delta", Data: json.RawMessage(`{"n":1}`)}
	ev2 := TunnelEvent{EventID: "e2", Type: "done"}
	if err := s.AppendTunnelEventToDisk(ses, ev1); err != nil {
		t.Fatalf("AppendTunnelEventToDisk e1: %v", err)
	}
	if err := s.AppendTunnelEventToDisk(ses, ev2); err != nil {
		t.Fatalf("AppendTunnelEventToDisk e2: %v", err)
	}

	// Records are on disk under the legacy tunnel_event type.
	raw, err := os.ReadFile(s.sessionPath(ses.ID))
	if err != nil {
		t.Fatalf("read session file: %v", err)
	}
	if n := bytes.Count(raw, []byte(`"type":"tunnel_event"`)); n != 2 {
		t.Errorf("tunnel_event record count = %d, want 2", n)
	}

	// Load skips them silently: session stays loadable, messages intact,
	// TunnelEvents stays empty (projection store owns tunnel history).
	reloaded, err := s.Load(ses.ID)
	if err != nil {
		t.Fatalf("Load with tunnel_event lines: %v", err)
	}
	if len(reloaded.Messages) != 1 {
		t.Errorf("messages = %d, want 1 (tunnel lines must not disturb loading)", len(reloaded.Messages))
	}
	if len(reloaded.TunnelEvents) != 0 {
		t.Errorf("TunnelEvents = %+v, want empty (JSONL replay is retired)", reloaded.TunnelEvents)
	}
}

// --- Torn tail (#657): crash residue must not swallow the next append ---

func TestSA146TornTailRecovery(t *testing.T) {
	s := sa146TestStore(t)
	ses := &Session{ID: generateID(), CreatedAt: time.Now(), UpdatedAt: time.Now()}
	ses.Messages = []provider.Message{sa146UserMsg("before the crash")}
	saveFullForTest(t, s, ses)

	// Simulate a crash mid-write: partial JSON, no trailing newline.
	f, err := os.OpenFile(s.sessionPath(ses.ID), os.O_APPEND|os.O_WRONLY, 0o600)
	if err != nil {
		t.Fatalf("open for tear: %v", err)
	}
	if _, err := f.WriteString(`{"type":"message","session_id":"` + ses.ID + `","mes`); err != nil {
		t.Fatalf("write torn residue: %v", err)
	}
	f.Close()

	after := sa146UserMsg("appended after the crash")
	if err := s.AppendMessageToDisk(ses, after); err != nil {
		t.Fatalf("AppendMessageToDisk onto torn tail: %v", err)
	}

	reloaded, err := s.Load(ses.ID)
	if err != nil {
		t.Fatalf("Load after torn-tail append: %v", err)
	}
	if len(reloaded.Messages) != 2 {
		t.Fatalf("messages = %d, want 2 (torn residue skipped, new record kept)", len(reloaded.Messages))
	}
	last := reloaded.Messages[len(reloaded.Messages)-1]
	if last.Role != "user" || last.Content[0].Text != "appended after the crash" {
		t.Errorf("last message = %+v, want the post-crash append", last)
	}
}

// --- AppendMessagesBatchToDisk: single-write batch + order preservation ---

func TestSA146BatchAppendPreservesOrder(t *testing.T) {
	s := sa146TestStore(t)
	ses := &Session{ID: generateID(), CreatedAt: time.Now(), UpdatedAt: time.Now()}
	first := sa146UserMsg("m1")
	ses.Messages = []provider.Message{first}
	saveFullForTest(t, s, ses)

	batch := []provider.Message{
		{ID: "sa146-b2", Role: "assistant", Content: []provider.ContentBlock{{Type: "text", Text: "m2"}}},
		sa146UserMsg("m3"),
		{ID: "sa146-b4", Role: "assistant", Content: []provider.ContentBlock{{Type: "text", Text: "m4"}}},
	}
	if err := s.AppendMessagesBatchToDisk(ses, batch); err != nil {
		t.Fatalf("AppendMessagesBatchToDisk: %v", err)
	}
	if err := s.AppendMessagesBatchToDisk(ses, nil); err != nil {
		t.Errorf("empty batch should be a no-op, got %v", err)
	}

	reloaded, err := s.Load(ses.ID)
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if len(reloaded.Messages) != 4 {
		t.Fatalf("messages = %d, want 4", len(reloaded.Messages))
	}
	for i, want := range []string{"m1", "m2", "m3", "m4"} {
		if got := reloaded.Messages[i].Content[0].Text; got != want {
			t.Errorf("message[%d].text = %q, want %q", i, got, want)
		}
	}
}

// --- backfillTimestamps: legacy sessions get monotonic timestamps ---

func sa146WriteLegacyJSONL(t *testing.T, s *JSONLStore, id string, lines []string) {
	t.Helper()
	var buf bytes.Buffer
	for _, l := range lines {
		buf.WriteString(l)
		buf.WriteByte('\n')
	}
	if err := os.WriteFile(s.sessionPath(id), buf.Bytes(), 0o600); err != nil {
		t.Fatalf("write legacy JSONL: %v", err)
	}
}

func TestSA146BackfillTimestampsAssignsMonotonicTimes(t *testing.T) {
	s := sa146TestStore(t)
	id := generateID()
	meta := `{"type":"meta","session_id":"` + id + `"}`
	m1 := `{"type":"message","session_id":"` + id + `","message":{"role":"user","content":[{"type":"text","text":"legacy a"}]}}`
	m2 := `{"type":"message","session_id":"` + id + `","message":{"role":"assistant","content":[{"type":"text","text":"legacy b"}]}}`
	sa146WriteLegacyJSONL(t, s, id, []string{meta, m1, m2})

	if firstMessageHasTimestamp(s.sessionPath(id)) {
		t.Fatal("legacy session should report no timestamp before backfill")
	}
	s.backfillTimestamps(id)

	if !firstMessageHasTimestamp(s.sessionPath(id)) {
		t.Fatal("first message still has no timestamp after backfill")
	}
	data, err := os.ReadFile(s.sessionPath(id))
	if err != nil {
		t.Fatalf("read backfilled file: %v", err)
	}
	var last time.Time
	msgs := 0
	for _, line := range strings.Split(strings.TrimSpace(string(data)), "\n") {
		var rec jsonlRecord
		if json.Unmarshal([]byte(line), &rec) != nil || rec.Type != "message" {
			continue
		}
		msgs++
		if rec.Timestamp.IsZero() {
			t.Error("message record still has zero timestamp after backfill")
		}
		if !last.IsZero() && !rec.Timestamp.After(last) {
			t.Errorf("timestamps not strictly increasing: %v not after %v", rec.Timestamp, last)
		}
		last = rec.Timestamp
	}
	if msgs != 2 {
		t.Errorf("scanned %d message records, want 2", msgs)
	}
}

func TestSA146BackfillTimestampsSkipsTimestampedSessions(t *testing.T) {
	s := sa146TestStore(t)
	id := generateID()
	ts := time.Now().Add(-2 * time.Hour).UTC().Format(time.RFC3339Nano)
	m1 := `{"type":"message","session_id":"` + id + `","timestamp":"` + ts + `","message":{"role":"user","content":[{"type":"text","text":"has ts"}]}}`
	m2 := `{"type":"message","session_id":"` + id + `","message":{"role":"assistant","content":[{"type":"text","text":"no ts but skipped"}]}}`
	sa146WriteLegacyJSONL(t, s, id, []string{m1, m2})

	before, _ := os.ReadFile(s.sessionPath(id))
	s.backfillTimestamps(id) // first message has a timestamp -> whole file skipped
	after, _ := os.ReadFile(s.sessionPath(id))
	if !bytes.Equal(before, after) {
		t.Error("session with a timestamped first message must not be rewritten")
	}
}

// --- isSummaryNoteMessage matrix ---

func TestSA146IsSummaryNoteMessage(t *testing.T) {
	cases := []struct {
		name string
		msg  *provider.Message
		want bool
	}{
		{"nil", nil, false},
		{"user role", &provider.Message{Role: "user", Content: []provider.ContentBlock{{Type: "text", Text: "[Previous conversation summary] x"}}}, false},
		{"system summary", &provider.Message{Role: "system", Content: []provider.ContentBlock{{Type: "text", Text: "[Previous conversation summary] compaction happened"}}}, true},
		{"system no marker", &provider.Message{Role: "system", Content: []provider.ContentBlock{{Type: "text", Text: "ordinary system note"}}}, false},
		{"system non-text only", &provider.Message{Role: "system", Content: []provider.ContentBlock{{Type: "tool_result", Output: "[Previous conversation summary]"}}}, false},
	}
	for _, tc := range cases {
		if got := isSummaryNoteMessage(tc.msg); got != tc.want {
			t.Errorf("%s: isSummaryNoteMessage = %v, want %v", tc.name, got, tc.want)
		}
	}
}

// --- ExportSessionMarkdownWithDisplay: display names + all block kinds ---

func TestSA146ExportSessionMarkdownWithDisplay(t *testing.T) {
	ses := &Session{
		ID: "sa146-export", Title: "Export Me",
		CreatedAt: time.Now(), UpdatedAt: time.Now(),
		Vendor: "zai", Endpoint: "coding", Model: "glm-4.7",
		Messages: []provider.Message{
			{Role: "user", Content: []provider.ContentBlock{{Type: "text", Text: "run the tests"}}},
			{Role: "assistant", Content: []provider.ContentBlock{
				{Type: "tool_use", ToolName: "go_test", Input: []byte(`{"pkg":"./..."}`)},
			}},
			{Role: "user", Content: []provider.ContentBlock{
				{Type: "tool_result", Output: "ok", IsError: true},
				{Type: "mystery_block"},
			}},
			{Role: "reviewer", Content: []provider.ContentBlock{{Type: "text", Text: "custom role"}}},
		},
	}
	md := ExportSessionMarkdownWithDisplay(ses, "Z.AI", "Coding Plan")
	for _, want := range []string{
		"# Export Me", "**Session:** sa146-export",
		"**Vendor:** Z.AI / Coding Plan / glm-4.7",
		"## User", "## Assistant", "## reviewer",
		"**Tool Call:** `go_test`", "```json", `{"pkg":"./..."}`,
		"**Tool Result** (error=true)", "```", "ok",
		"custom role",
	} {
		if !strings.Contains(md, want) {
			t.Errorf("markdown missing %q\ngot:\n%s", want, md)
		}
	}
	// Raw keys are used when no display names are supplied.
	raw := ExportSessionMarkdown(ses)
	if !strings.Contains(raw, "**Vendor:** zai / coding / glm-4.7") {
		t.Errorf("ExportSessionMarkdown should use raw keys, got:\n%s", raw)
	}
}

// --- searchJSONLLine: matching + skip matrix ---

func TestSA146SearchJSONLLine(t *testing.T) {
	line := `{"type":"message","session_id":"sa146-search","message":{"role":"user","content":[{"type":"text","text":"Fix the Parser Bug TODAY"}]}}`
	res, ok := searchJSONLLine(line, "/tmp/sa146-search.jsonl", "Search Title", []string{"parser bug"})
	if !ok {
		t.Fatal("expected case-insensitive match")
	}
	if res.Role != "user" || res.SessionID != "sa146-search" || res.Title != "Search Title" {
		t.Errorf("result metadata = %+v", res)
	}
	if !strings.Contains(res.Snippet, "Parser") {
		t.Errorf("snippet = %q, want it to contain the matched text", res.Snippet)
	}

	skips := []string{
		`{invalid json`,
		`{"type":"meta","session_id":"x"}`,
		`{"type":"message","session_id":"x"}`,
		`{"type":"message","session_id":"x","message":{"role":"assistant","content":[{"type":"tool_use","tool_name":"ls"}]}}`,
	}
	for _, s := range skips {
		if _, ok := searchJSONLLine(s, "/tmp/x.jsonl", "t", []string{"parser"}); ok {
			t.Errorf("line should not match: %s", s)
		}
	}
}

// --- SessionLock accessors + parsePID ---

func TestSA146SessionLockAccessorsAndParsePID(t *testing.T) {
	var nilLock *SessionLock
	if nilLock.HolderPID() != 0 {
		t.Error("nil lock HolderPID should be 0")
	}
	if nilLock.Acquired() {
		t.Error("nil lock should not report acquired")
	}
	l := &SessionLock{storeDir: t.TempDir(), sessionID: "sa146", holderPID: os.Getpid()}
	if l.HolderPID() != os.Getpid() {
		t.Errorf("HolderPID = %d, want %d", l.HolderPID(), os.Getpid())
	}
	if l.Acquired() {
		t.Error("unacquired lock should not report acquired")
	}

	pids := []struct {
		in   []byte
		want int
	}{
		{in: nil, want: 0},
		{in: []byte(""), want: 0},
		{in: []byte("123"), want: 123},
		{in: []byte("notapid"), want: 0},
		{in: []byte("12345678901234567890"), want: 1234567890123456}, // capped at 16 bytes
	}
	for _, tc := range pids {
		if got := parsePID(tc.in); got != tc.want {
			t.Errorf("parsePID(%q) = %d, want %d", tc.in, got, tc.want)
		}
	}
}

// --- endpoint stats: nil receivers, fallbacks, caps ---

func TestSA146EndpointStatsNilReceivers(t *testing.T) {
	var s *Session
	if got := s.UsageForEndpoint("v", "e"); got != (provider.TokenUsage{}) {
		t.Errorf("nil receiver UsageForEndpoint = %v, want zero", got)
	}
	if got := s.MetricsForEndpoint("v", "e"); got != nil {
		t.Errorf("nil receiver MetricsForEndpoint = %v, want nil", got)
	}
}

func TestSA146MetricsForEndpointRebuildAndFallbacks(t *testing.T) {
	// History present but no per-endpoint buckets -> rebuild path.
	s := &Session{Metrics: []metrics.MetricEvent{{Vendor: "v", Endpoint: "e"}}}
	got := s.MetricsForEndpoint("v", "e")
	if len(got) != 1 || got[0].Vendor != "v" {
		t.Fatalf("rebuild path = %+v, want 1 event", got)
	}
	// Empty key with vendor-tagged metadata -> nil (ambiguous legacy bucket).
	if got := s.MetricsForEndpoint("", ""); got != nil {
		t.Errorf("empty key with metadata = %v, want nil", got)
	}
	// Empty key with no buckets and no metadata -> raw copy.
	plain := &Session{Metrics: []metrics.MetricEvent{{}}}
	if got := plain.MetricsForEndpoint("", ""); len(got) != 1 {
		t.Errorf("empty key fallback = %+v, want raw copy of 1 event", got)
	}
	// Non-empty key, no buckets, no metadata, matching session key -> raw copy
	// (zero-element copy of a nil slice is itself nil; only length matters).
	matching := &Session{Vendor: "v", Endpoint: "e"}
	if got := matching.MetricsForEndpoint("v", "e"); len(got) != 0 {
		t.Errorf("session-key match = %+v, want empty copy", got)
	}
}

func TestSA146EndpointUsageFallbacks(t *testing.T) {
	// No history, matching session key -> fall back to session TokenUsage.
	s := &Session{Vendor: "v", Endpoint: "e", TokenUsage: provider.TokenUsage{InputTokens: 7}}
	if got := s.UsageForEndpoint("v", "e"); got.InputTokens != 7 {
		t.Errorf("session-token fallback = %+v, want InputTokens=7", got)
	}
	// ensureEndpointStatsLocked lazily creates both maps.
	lazy := &Session{}
	lazy.AddUsageForEndpoint("v", "e", provider.TokenUsage{InputTokens: 1})
	lazy.AppendMetricForEndpoint("v", "e", metrics.MetricEvent{Vendor: "v", Endpoint: "e"})
	if len(lazy.EndpointUsage) != 1 || len(lazy.EndpointMetrics) != 1 {
		t.Fatalf("lazy init failed: usage=%v metrics=%v", lazy.EndpointUsage, lazy.EndpointMetrics)
	}
	// Cap: appending past maxEndpointMetricsPerKey keeps the most recent 200.
	for i := 0; i < maxEndpointMetricsPerKey+5; i++ {
		lazy.AppendMetricForEndpoint("v", "e", metrics.MetricEvent{Vendor: "v", Endpoint: "e"})
	}
	if got := lazy.MetricsForEndpoint("v", "e"); len(got) != maxEndpointMetricsPerKey {
		t.Errorf("capped metrics = %d, want %d", len(got), maxEndpointMetricsPerKey)
	}
}

// --- saveIndex leaves no temp file behind (atomic rename contract) ---

func TestSA146SaveIndexNoTempResidue(t *testing.T) {
	s := sa146TestStore(t)
	ses := &Session{ID: generateID(), CreatedAt: time.Now(), UpdatedAt: time.Now()}
	ses.Messages = []provider.Message{sa146UserMsg("index residue")}
	saveFullForTest(t, s, ses)

	if _, err := os.Stat(s.indexPath()); err != nil {
		t.Fatalf("index.json missing: %v", err)
	}
	if _, err := os.Stat(s.indexPath() + ".tmp"); !os.IsNotExist(err) {
		t.Error("index.json.tmp should not survive a completed saveIndex")
	}
}

// --- batch 2: pure helpers, cheap branches, durability contracts ---

func TestSA146QuickRecordType(t *testing.T) {
	cases := []struct {
		line string
		want string
	}{
		{`{"type":"message","x":1}`, "message"},
		{`{"session_id":"a"}`, ""},
		{`{"type":"meta"`, "meta"}, // unterminated JSON is irrelevant: naive substring scan
	}
	for _, tc := range cases {
		if got := quickRecordType([]byte(tc.line)); got != tc.want {
			t.Errorf("quickRecordType(%q) = %q, want %q", tc.line, got, tc.want)
		}
	}
}

func TestSA146QuickIsDialogueRole(t *testing.T) {
	cases := []struct {
		line string
		want bool
	}{
		{`{"message":{"role":"user"}}`, true},
		{`{"message":{"role":"assistant"}}`, true},
		{`{"message":{"role":"system"}}`, false},
		{`{"no_role":true}`, false},
	}
	for _, tc := range cases {
		if got := quickIsDialogueRole([]byte(tc.line)); got != tc.want {
			t.Errorf("quickIsDialogueRole(%q) = %v, want %v", tc.line, got, tc.want)
		}
	}
}

func TestSA146QuickExtractTimestampTopLevelOnly(t *testing.T) {
	ts := quickExtractTimestamp([]byte(`{"type":"message","timestamp":"2026-01-02T03:04:05Z","message":{}}`))
	if ts.IsZero() || ts.Year() != 2026 || ts.Hour() != 3 {
		t.Errorf("valid top-level timestamp not extracted: %v", ts)
	}
	// #558 C: nested pseudo-timestamps must be ignored.
	if got := quickExtractTimestamp([]byte(`{"message":{"timestamp":"2099-01-01T00:00:00Z"}}`)); !got.IsZero() {
		t.Errorf("nested timestamp leaked: %v", got)
	}
	if got := quickExtractTimestamp([]byte(`{"timestamp":"2026-01-02T03"}`)); !got.IsZero() {
		t.Errorf("short value should not parse: %v", got)
	}
	if got := quickExtractTimestamp([]byte(`{"timestamp":123}`)); !got.IsZero() {
		t.Errorf("non-string field should not parse: %v", got)
	}
	if got := quickExtractTimestamp([]byte(`{"no_ts":true}`)); !got.IsZero() {
		t.Errorf("absent field should be zero: %v", got)
	}
}

func TestSA146TopLevelStringFieldEscapeAware(t *testing.T) {
	line := []byte(`{"note":"has \"timestamp\":\"fake\" inside","timestamp":"2026-03-04T05:06:07Z"}`)
	s, e := topLevelStringField(line, "timestamp")
	if s < 0 || string(line[s:e]) != "2026-03-04T05:06:07Z" {
		t.Errorf("escape-aware scan failed: [%d,%d) %q", s, e, line[s:max(0, e)])
	}
	if s2, e2 := topLevelStringField(line, "absent"); s2 != -1 || e2 != -1 {
		t.Errorf("absent key = (%d,%d), want (-1,-1)", s2, e2)
	}
}

func TestSA146LastDialogueIndexAndOrphanDetection(t *testing.T) {
	if got := lastDialogueIndex(nil); got != -1 {
		t.Errorf("lastDialogueIndex(nil) = %d, want -1", got)
	}
	msgs := []provider.Message{
		sa146UserMsg("u1"),
		{Role: "assistant", Content: []provider.ContentBlock{{Type: "text", Text: "a1"}}},
		sa146UserMsg("u2"),
	}
	if got := lastDialogueIndex(msgs); got != 2 {
		t.Errorf("lastDialogueIndex = %d, want 2", got)
	}
	toolResult := provider.Message{Role: "user", Content: []provider.ContentBlock{{Type: "tool_result"}}}
	toolUse := provider.Message{Role: "assistant", Content: []provider.ContentBlock{{Type: "tool_use"}}}
	plain := sa146UserMsg("plain")
	if !isOrphanToolMessage(toolResult) || !isOrphanToolMessage(toolUse) || isOrphanToolMessage(plain) {
		t.Error("isOrphanToolMessage matrix failed")
	}
	if !messageHasToolResult(toolResult) || messageHasToolResult(plain) {
		t.Error("messageHasToolResult matrix failed")
	}
	if !messageHasToolUse(toolUse) || messageHasToolUse(plain) {
		t.Error("messageHasToolUse matrix failed")
	}
}

func TestSA146CapContextTail(t *testing.T) {
	if got := capContextTail(make([]provider.Message, 3)); len(got) != 3 {
		t.Fatalf("short slice must pass through unchanged, got %d", len(got))
	}
	plain := make([]provider.Message, MaxContextMessages+5)
	for i := range plain {
		if i%2 == 0 {
			plain[i] = sa146UserMsg(fmt.Sprintf("m%d", i))
		} else {
			plain[i] = provider.Message{Role: "assistant", Content: []provider.ContentBlock{{Type: "text", Text: fmt.Sprintf("a%d", i)}}}
		}
	}
	got := capContextTail(plain)
	if len(got) != MaxContextMessages+1 {
		t.Fatalf("capped length = %d, want %d (+1 note)", len(got), MaxContextMessages+1)
	}
	if !strings.Contains(got[0].Content[0].Text, "5 earlier messages") {
		t.Errorf("truncation note = %q, want omitted=5", got[0].Content[0].Text)
	}
	// Orphan half-pair at the window START edge must be pulled in, not split:
	// with 205 messages the window opens at index 5; an orphan tool_result
	// there shifts the opening back to 4 (omitted 5 -> 4).
	orphaned := make([]provider.Message, len(plain))
	copy(orphaned, plain)
	orphaned[5] = provider.Message{Role: "user", Content: []provider.ContentBlock{{Type: "tool_result", Output: "orphan pair"}}}
	got2 := capContextTail(orphaned)
	if !strings.Contains(got2[0].Content[0].Text, "4 earlier messages") {
		t.Errorf("orphan shift note = %q, want omitted=4", got2[0].Content[0].Text)
	}
}

func TestSA146NormalizeWorkspacePathAndCurrent(t *testing.T) {
	if got := NormalizeWorkspacePath("   "); got != "" {
		t.Errorf("blank path = %q, want empty", got)
	}
	existing := t.TempDir()
	if got := NormalizeWorkspacePath(existing); !filepath.IsAbs(got) {
		t.Errorf("existing path = %q, want absolute clean path", got)
	}
	missing := filepath.Join(existing, "not-created-yet")
	if got := NormalizeWorkspacePath(missing); !filepath.IsAbs(got) || strings.HasSuffix(got, "/") {
		t.Errorf("missing path falls back to abs+clean: %q", got)
	}
	if CurrentWorkspacePath() == "" {
		t.Error("CurrentWorkspacePath should resolve in a working process")
	}
}

func TestSA146NewSessionMessageIDFormat(t *testing.T) {
	a, b := newSessionMessageID(), newSessionMessageID()
	if a == b {
		t.Fatal("message IDs must be unique")
	}
	for _, id := range []string{a, b} {
		if !strings.HasPrefix(id, "msg_") || len(id) != len("msg_")+8+1+4+1+4+1+4+1+12 {
			t.Errorf("unexpected ID format: %q", id)
		}
	}
}

func TestSA146ExportMarkdownLoadError(t *testing.T) {
	s := sa146TestStore(t)
	if _, err := s.ExportMarkdown("no-such-session-id"); err == nil {
		t.Error("ExportMarkdown of a missing session should error")
	}
}

func TestSA146CleanupOlderThanSkipsPinned(t *testing.T) {
	s := sa146TestStore(t)
	old := time.Now().Add(-48 * time.Hour)
	pinned := &Session{ID: generateID(), CreatedAt: old, UpdatedAt: old, Pinned: true}
	free := &Session{ID: generateID(), CreatedAt: old, UpdatedAt: old}
	for _, ses := range []*Session{pinned, free} {
		ses.Messages = []provider.Message{sa146UserMsg("cleanup candidate")}
		saveFullForTest(t, s, ses)
		// Save() stamps UpdatedAt=now; re-assert the old timestamp via a meta
		// record (the latest meta wins on load).
		ses.UpdatedAt = old
		if err := s.AppendMetaToDisk(ses); err != nil {
			t.Fatalf("AppendMetaToDisk backdate %s: %v", ses.ID, err)
		}
	}
	removed, err := s.CleanupOlderThan(time.Now().Add(-24 * time.Hour))
	if err != nil {
		t.Fatalf("CleanupOlderThan: %v", err)
	}
	if removed != 1 {
		t.Fatalf("removed = %d, want 1", removed)
	}
	if _, err := s.Load(pinned.ID); err != nil {
		t.Errorf("pinned session must survive cleanup: %v", err)
	}
	if _, err := s.Load(free.ID); err == nil {
		t.Error("unpinned old session should have been deleted")
	}
}

func TestSA146HasUserInteractionOnDisk(t *testing.T) {
	s := sa146TestStore(t)
	withUser := &Session{ID: generateID(), CreatedAt: time.Now(), UpdatedAt: time.Now()}
	withUser.Messages = []provider.Message{sa146UserMsg("real user turn")}
	saveFullForTest(t, s, withUser)

	if got, err := s.HasUserInteractionOnDisk(withUser.ID); err != nil || !got {
		t.Errorf("user-text session = (%v,%v), want (true,nil)", got, err)
	}
	assistantOnly := &Session{ID: generateID(), CreatedAt: time.Now(), UpdatedAt: time.Now()}
	assistantOnly.Messages = []provider.Message{
		{ID: "sa146-a1", Role: "assistant", Content: []provider.ContentBlock{{Type: "text", Text: "only answer"}}},
	}
	saveFullForTest(t, s, assistantOnly)
	if got, err := s.HasUserInteractionOnDisk(assistantOnly.ID); err != nil || got {
		t.Errorf("assistant-only session = (%v,%v), want (false,nil)", got, err)
	}
	if got, err := s.HasUserInteractionOnDisk(generateID()); err != nil || got {
		t.Errorf("missing file = (%v,%v), want (false,nil)", got, err)
	}
}

func TestSA146EndpointStatsKeyTable(t *testing.T) {
	cases := []struct{ vendor, endpoint, want string }{
		{"", "", ""},
		{"v", "", "v"},
		{"", "e", "e"},
		{" v ", " e ", "v/e"},
	}
	for _, tc := range cases {
		if got := EndpointStatsKey(tc.vendor, tc.endpoint); got != tc.want {
			t.Errorf("EndpointStatsKey(%q,%q) = %q, want %q", tc.vendor, tc.endpoint, got, tc.want)
		}
	}
	var nilSession *Session
	nilSession.AddUsageForEndpoint("v", "e", provider.TokenUsage{}) // must not panic
}

func TestSA146RebuildEndpointStatsCapsAndSkipsEmptyKeys(t *testing.T) {
	s := &Session{}
	for i := 0; i < maxEndpointMetricsPerKey+10; i++ {
		s.UsageHistory = append(s.UsageHistory, UsageEntry{Vendor: "v", Endpoint: "e", Usage: provider.TokenUsage{InputTokens: 1}})
		s.Metrics = append(s.Metrics, metrics.MetricEvent{Vendor: "v", Endpoint: "e"})
	}
	// Untagged legacy entries resolve to an empty key and must be skipped.
	s.UsageHistory = append(s.UsageHistory, UsageEntry{Usage: provider.TokenUsage{InputTokens: 100}})
	s.Metrics = append(s.Metrics, metrics.MetricEvent{})

	s.RebuildEndpointStats()
	if got := s.UsageForEndpoint("v", "e"); got.InputTokens != maxEndpointMetricsPerKey+10 {
		t.Errorf("rebuilt usage = %+v, want InputTokens=%d", got, maxEndpointMetricsPerKey+10)
	}
	if got := s.MetricsForEndpoint("v", "e"); len(got) != maxEndpointMetricsPerKey {
		t.Errorf("rebuilt metrics = %d, want capped %d", len(got), maxEndpointMetricsPerKey)
	}
	if _, has := s.EndpointUsage[""]; has {
		t.Error("empty key must not create a bucket")
	}
}

func TestSA146SetTagsNormalizesAndPersists(t *testing.T) {
	s := sa146TestStore(t)
	ses := &Session{ID: generateID(), CreatedAt: time.Now(), UpdatedAt: time.Now()}
	ses.Messages = []provider.Message{sa146UserMsg("tagged session")}
	saveFullForTest(t, s, ses)

	many := make([]string, 0, maxTagsPerSession+4)
	many = append(many, "  dup ", "DUP", "", strings.Repeat("r", maxTagRunes+5))
	for i := len(many); i < maxTagsPerSession+4; i++ {
		many = append(many, fmt.Sprintf("tag%d", i))
	}
	if err := s.SetTags(ses, many); err != nil {
		t.Fatalf("SetTags: %v", err)
	}
	if len(ses.Tags) != maxTagsPerSession {
		t.Fatalf("tags = %d, want capped %d", len(ses.Tags), maxTagsPerSession)
	}
	if ses.Tags[0] != "dup" {
		t.Errorf("first tag = %q, want trimmed 'dup'", ses.Tags[0])
	}
	if runeCount := len([]rune(ses.Tags[1])); runeCount != maxTagRunes {
		t.Errorf("long tag = %d runes, want truncated to %d", runeCount, maxTagRunes)
	}

	reloaded, err := s.Load(ses.ID)
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if len(reloaded.Tags) != maxTagsPerSession || reloaded.Tags[0] != "dup" {
		t.Errorf("tags round-trip = %v", reloaded.Tags)
	}
}

// --- batch 3: error branches and boundary conditions ---

func TestSA146PruneInvalidIndexKeepsEntriesOnLoadError(t *testing.T) {
	s := sa146TestStore(t)
	ses := &Session{ID: generateID(), CreatedAt: time.Now(), UpdatedAt: time.Now()}
	ses.Messages = []provider.Message{sa146UserMsg("unreadable")}
	saveFullForTest(t, s, ses)

	// #709 hardening: a transient load error must NOT evict the entry —
	// the session stays listed until a later repair pass succeeds.
	if err := os.Chmod(s.sessionPath(ses.ID), 0o000); err != nil {
		t.Fatalf("chmod: %v", err)
	}
	defer func() { _ = os.Chmod(s.sessionPath(ses.ID), 0o600) }()

	idx := []indexEntry{{ID: ses.ID}}
	valid, cleaned := s.pruneInvalidIndexEntries(idx)
	if cleaned {
		t.Error("load error must not report cleaned=true")
	}
	if len(valid) != 1 || valid[0].ID != ses.ID {
		t.Errorf("entry dropped on transient load error: %+v", valid)
	}
}

func TestSA146HasUserInteractionOnDiskPermError(t *testing.T) {
	s := sa146TestStore(t)
	ses := &Session{ID: generateID(), CreatedAt: time.Now(), UpdatedAt: time.Now()}
	ses.Messages = []provider.Message{sa146UserMsg("perm probe")}
	saveFullForTest(t, s, ses)

	if err := os.Chmod(s.sessionPath(ses.ID), 0o000); err != nil {
		t.Fatalf("chmod: %v", err)
	}
	defer func() { _ = os.Chmod(s.sessionPath(ses.ID), 0o600) }()

	// Uncertain state must surface as an error, not a silent "no interaction"
	// (a false negative would let CleanupOlderThan delete a live session).
	if got, err := s.HasUserInteractionOnDisk(ses.ID); err == nil {
		t.Errorf("permission-denied read = (%v, nil), want an error", got)
	}
}

func TestSA146CleanupOlderThanDeleteErrorSurfaces(t *testing.T) {
	s := sa146TestStore(t)
	old := time.Now().Add(-48 * time.Hour)
	free := &Session{ID: generateID(), CreatedAt: old, UpdatedAt: old}
	free.Messages = []provider.Message{sa146UserMsg("undeletable")}
	saveFullForTest(t, s, free)
	free.UpdatedAt = old
	if err := s.AppendMetaToDisk(free); err != nil {
		t.Fatalf("AppendMetaToDisk: %v", err)
	}

	// Read-only store dir: List succeeds, Delete fails, error propagates.
	if err := os.Chmod(s.Dir(), 0o500); err != nil {
		t.Fatalf("chmod dir: %v", err)
	}
	defer func() { _ = os.Chmod(s.Dir(), 0o700) }()

	_, err := s.CleanupOlderThan(time.Now().Add(-24 * time.Hour))
	if err == nil {
		t.Error("Delete failure must surface, not be swallowed")
	}
}

func TestSA146BackfillTimestampsOpenFailure(t *testing.T) {
	s := sa146TestStore(t)
	id := generateID()
	m1 := `{"type":"message","session_id":"` + id + `","message":{"role":"user","content":[{"type":"text","text":"no ts"}]}}`
	sa146WriteLegacyJSONL(t, s, id, []string{m1})

	if err := os.Chmod(s.sessionPath(id), 0o000); err != nil {
		t.Fatalf("chmod: %v", err)
	}
	defer func() { _ = os.Chmod(s.sessionPath(id), 0o600) }()

	// Unreadable file: firstMessageHasTimestamp -> false, backfill bails
	// at open, no panic, no rewrite.
	if firstMessageHasTimestamp(s.sessionPath(id)) {
		t.Error("unreadable file should report false, not true")
	}
	s.backfillTimestamps(id) // must not panic
}

func TestSA146LoadIndexPermError(t *testing.T) {
	s := sa146TestStore(t)
	if err := os.WriteFile(s.indexPath(), []byte("[]"), 0o600); err != nil {
		t.Fatalf("write index: %v", err)
	}
	if err := os.Chmod(s.indexPath(), 0o000); err != nil {
		t.Fatalf("chmod: %v", err)
	}
	defer func() { _ = os.Chmod(s.indexPath(), 0o600) }()

	if _, err := s.loadIndex(); err == nil {
		t.Error("unreadable index (not missing) must return the I/O error")
	}
}

func TestSA146TryAcquireSessionLockBadDir(t *testing.T) {
	if _, err := TryAcquireSessionLock(filepath.Join(t.TempDir(), "nope", "deeper"), "sa146-x"); err == nil {
		t.Error("lock acquisition in a missing directory should fail")
	}
}

func TestSA146MakeSnippetBoundaries(t *testing.T) {
	// Match at the very start: no leading ellipsis.
	res, ok := searchJSONLLine(
		`{"type":"message","session_id":"x","message":{"role":"user","content":[{"type":"text","text":"needle at start"}]}}`,
		"/tmp/x.jsonl", "t", []string{"needle"})
	if !ok {
		t.Fatal("start match not found")
	}
	if strings.HasPrefix(res.Snippet, "...") {
		t.Errorf("match at index 0 must not have a leading ellipsis: %q", res.Snippet)
	}
	// Match at the very end: no trailing ellipsis.
	res2, ok := searchJSONLLine(
		`{"type":"message","session_id":"x","message":{"role":"user","content":[{"type":"text","text":"tail ends with needle"}]}}`,
		"/tmp/x.jsonl", "t", []string{"needle"})
	if !ok {
		t.Fatal("end match not found")
	}
	if strings.HasSuffix(res2.Snippet, "...") {
		t.Errorf("match at end must not have a trailing ellipsis: %q", res2.Snippet)
	}
	// Needle longer than the 200-char snippet budget: half<0 clamps to 0.
	longNeedle := strings.Repeat("x", 210)
	res3, ok := searchJSONLLine(
		`{"type":"message","session_id":"x","message":{"role":"user","content":[{"type":"text","text":"`+longNeedle+` tail"}]}}`,
		"/tmp/x.jsonl", "t", []string{longNeedle})
	if !ok {
		t.Fatal("long-needle match not found")
	}
	if strings.HasPrefix(res3.Snippet, "...") {
		t.Errorf("oversized needle must not produce a leading ellipsis: %q", res3.Snippet)
	}
}

func TestSA146RunMaintenanceCleanStoreEarlyReturn(t *testing.T) {
	s := sa146TestStore(t)
	ses := &Session{ID: generateID(), CreatedAt: time.Now(), UpdatedAt: time.Now()}
	ses.Messages = []provider.Message{sa146UserMsg("maintenance probe")}
	saveFullForTest(t, s, ses)

	done := make(chan struct{})
	go func() {
		s.runMaintenance() // takes s.mu itself; contends with the background
		close(done)
	}()
	select {
	case <-done:
	case <-time.After(10 * time.Second):
		buf := make([]byte, 1<<20)
		n := runtime.Stack(buf, true)
		t.Fatalf("runMaintenance did not finish in 10s; stacks:\n%s", buf[:n])
	}

	if _, err := os.Stat(s.indexPath()); err != nil {
		t.Errorf("index must survive a no-op maintenance pass: %v", err)
	}
	if got, err := s.Load(ses.ID); err != nil || len(got.Messages) != 1 {
		t.Errorf("session lost after maintenance: %v %v", got, err)
	}
}

func TestSA146SaveIndexTempCreateFailure(t *testing.T) {
	s := sa146TestStore(t)
	if err := os.Chmod(s.Dir(), 0o500); err != nil {
		t.Fatalf("chmod dir: %v", err)
	}
	defer func() { _ = os.Chmod(s.Dir(), 0o700) }()

	if err := s.saveIndex([]indexEntry{{ID: "x"}}); err == nil {
		t.Error("saveIndex in a read-only directory should fail at tmp creation")
	}
}

func TestSA146AppendRecordLinesOpenFailure(t *testing.T) {
	s := sa146TestStore(t)
	ses := &Session{ID: generateID(), CreatedAt: time.Now(), UpdatedAt: time.Now()}
	ses.Messages = []provider.Message{sa146UserMsg("locked file probe")}
	saveFullForTest(t, s, ses)

	// Unwritable data file: the flock sidecar is fine, but the O_RDWR open
	// fails and appendRecordLines aborts instead of half-writing (#558 D).
	if err := os.Chmod(s.sessionPath(ses.ID), 0o000); err != nil {
		t.Fatalf("chmod: %v", err)
	}
	defer func() { _ = os.Chmod(s.sessionPath(ses.ID), 0o600) }()

	if err := s.AppendMessageToDisk(ses, sa146UserMsg("must fail")); err == nil {
		t.Error("append onto an unwritable session file should fail")
	}
}

func TestSA146BackfillIDsUnreadableFile(t *testing.T) {
	s := sa146TestStore(t)
	id := generateID()
	m1 := `{"type":"message","session_id":"` + id + `","message":{"role":"user","content":[{"type":"text","text":"ids?"}]}}`
	sa146WriteLegacyJSONL(t, s, id, []string{m1})

	if err := os.Chmod(s.sessionPath(id), 0o000); err != nil {
		t.Fatalf("chmod: %v", err)
	}
	defer func() { _ = os.Chmod(s.sessionPath(id), 0o600) }()

	// Must abort without corrupting anything; no panic.
	s.backfillIDs(id, map[string]provider.Message{"sa146-x": sa146UserMsg("update")})
}

func TestSA146BackfillTimestampsOverlongLine(t *testing.T) {
	s := sa146TestStore(t)
	id := generateID()
	// 10.5MB single line: firstMessageHasTimestamp (2MB cap) and the backfill
	// scanner (10MB cap) both bail with ErrTooLong - the session is left
	// untouched rather than partially rewritten.
	blob := strings.Repeat("a", 10*1024*1024+512)
	line := `{"type":"message","session_id":"` + id + `","message":{"role":"user","content":[{"type":"text","text":"` + blob + `"}]}}`
	sa146WriteLegacyJSONL(t, s, id, []string{line})

	s.backfillTimestamps(id) // must return early, no panic

	data, err := os.ReadFile(s.sessionPath(id))
	if err != nil {
		t.Fatalf("read back: %v", err)
	}
	if !strings.Contains(string(data), blob) {
		t.Error("over-long session file was modified by backfill")
	}
}

func TestSA146UsageForEndpointRebuildMissFallsThrough(t *testing.T) {
	s := &Session{Vendor: "v", Endpoint: "e"}
	s.UsageHistory = []UsageEntry{{Vendor: "v", Endpoint: "e", Usage: provider.TokenUsage{InputTokens: 3}}}
	// Rebuild fills bucket "v/e"; asking for a different endpoint misses and
	// falls through to the zero value (history is non-empty so no fallback).
	if got := s.UsageForEndpoint("other", "endpoint"); got.InputTokens != 0 {
		t.Errorf("foreign endpoint usage = %+v, want zero", got)
	}
}

func TestSA146QuickExtractTimestampInvalidValue(t *testing.T) {
	if got := quickExtractTimestamp([]byte(`{"timestamp":"9999-99-99T99:99:99Z"}`)); !got.IsZero() {
		t.Errorf("invalid RFC3339 should yield zero, got %v", got)
	}
}

func TestSA146TopLevelStringFieldUnterminated(t *testing.T) {
	if s, e := topLevelStringField([]byte(`{"timestamp`), "timestamp"); s != -1 || e != -1 {
		t.Errorf("unterminated key = (%d,%d), want (-1,-1)", s, e)
	}
	if s, e := topLevelStringField([]byte(`{"timestamp":"unterminated`), "timestamp"); s != -1 || e != -1 {
		t.Errorf("unterminated value = (%d,%d), want (-1,-1)", s, e)
	}
}

func TestSA146ListAfterDirRemoval(t *testing.T) {
	s := sa146TestStore(t)
	if err := os.RemoveAll(s.Dir()); err != nil {
		t.Fatalf("remove store dir: %v", err)
	}
	if _, err := s.List(); err == nil {
		t.Error("List against a removed store directory should fail")
	}
}
