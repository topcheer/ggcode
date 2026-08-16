package session

// #558 feature tests. One test per bug fixed in this batch:
//   A — stale byte-offset cutoff after migrateMessageIDs rewrite
//   C — quickExtractTimestamp picking up pseudo-timestamps inside message content
//   D — cross-process lost-update window between read->tmp->rename rewrites and O_APPEND
//   F — sessionToIndexEntry MsgCount regression under time-windowed loading
//   G — extractSessionID on Windows paths (dotted directory names)

import (
	"bufio"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/topcheer/ggcode/internal/provider"
)

// --- Bug C (#558): top-level timestamp extraction ---

// TestQuickExtractTimestamp_IgnoresPseudoTimestampInContent verifies that a
// tool_use Input containing a fake "timestamp" field serialized BEFORE the
// record's real top-level Timestamp no longer hijacks the extraction (#558 C).
// Before the fix, bytes.Index matched the first (nested) occurrence and
// findMessageCutoff's chronological monotonicity assumption broke.
func TestQuickExtractTimestamp_IgnoresPseudoTimestampInContent(t *testing.T) {
	real := time.Date(2026, 7, 1, 12, 0, 0, 0, time.UTC)
	rec := jsonlRecord{
		Type: "message",
		Message: &provider.Message{
			ID:   "m1",
			Role: "assistant",
			Content: []provider.ContentBlock{{
				Type:     "tool_use",
				ToolID:   "tu1",
				ToolName: "run_command",
				Input:    json.RawMessage(`{"command":"echo {\"timestamp\":\"2099-01-01T00:00:00Z\"}"}`),
			}},
		},
		Timestamp: real,
	}
	line, err := json.Marshal(rec)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(line), "2099-01-01") {
		t.Fatalf("test setup: pseudo-timestamp not serialized into line: %s", line)
	}

	got := quickExtractTimestamp(line)
	if got.IsZero() {
		t.Fatal("quickExtractTimestamp returned zero time (top-level timestamp missed)")
	}
	if !got.Equal(real) {
		t.Fatalf("quickExtractTimestamp = %v, want top-level %v (got hijacked by content pseudo-timestamp?)", got, real)
	}
}

// TestQuickExtractTimestamp_TopLevelFallback verifies the extractor still
// finds the real timestamp when it is the only occurrence (no content noise).
func TestQuickExtractTimestamp_TopLevelFallback(t *testing.T) {
	rec := jsonlRecord{
		Type: "message",
		Message: &provider.Message{
			Role:    "user",
			Content: []provider.ContentBlock{{Type: "text", Text: "hello"}},
		},
		Timestamp: time.Date(2025, 3, 4, 5, 6, 7, 0, time.UTC),
	}
	line, err := json.Marshal(rec)
	if err != nil {
		t.Fatal(err)
	}
	got := quickExtractTimestamp(line)
	if got.IsZero() || got.Year() != 2025 {
		t.Fatalf("quickExtractTimestamp = %v, want 2025-03-04 timestamp", got)
	}
}

// TestQuickExtractTimestamp_TextContentPseudo verifies a pseudo-timestamp in
// plain text content (not just tool_use input) is also skipped.
func TestQuickExtractTimestamp_TextContentPseudo(t *testing.T) {
	real := time.Date(2026, 8, 1, 9, 0, 0, 0, time.UTC)
	rec := jsonlRecord{
		Type: "message",
		Message: &provider.Message{
			Role: "user",
			Content: []provider.ContentBlock{{
				Type: "text",
				Text: `log line {"timestamp":"2099-12-31T23:59:59Z"} end`,
			}},
		},
		Timestamp: real,
	}
	line, err := json.Marshal(rec)
	if err != nil {
		t.Fatal(err)
	}
	got := quickExtractTimestamp(line)
	if got.IsZero() || !got.Equal(real) {
		t.Fatalf("quickExtractTimestamp = %v, want %v", got, real)
	}
}

// TestTopLevelStringField_Depth checks the structure-aware scanner directly:
// the depth-1 field is found, nested occurrences are skipped, and escaped
// quotes inside string values do not corrupt the scan.
func TestTopLevelStringField_Depth(t *testing.T) {
	line := []byte(`{"type":"message","message":{"content":[{"text":"\"timestamp\":\"2099\""}]},"timestamp":"2026-07-01T12:00:00Z"}`)
	start, end := topLevelStringField(line, "timestamp")
	if start < 0 {
		t.Fatal("no top-level match found")
	}
	if got := string(line[start:end]); got != "2026-07-01T12:00:00Z" {
		t.Fatalf("matched wrong occurrence: %q", got)
	}
	if s, e := topLevelStringField([]byte(`{"a":[{"timestamp":"x"}]}`), "timestamp"); s != -1 || e != -1 {
		t.Fatalf("nested match should be skipped, got (%d,%d)", s, e)
	}
	// Unescaped-value noise: key name appearing only inside a nested string value.
	if s, _ := topLevelStringField([]byte(`{"msg":{"text":"timestamp"},"other":"v"}`), "timestamp"); s != -1 {
		t.Fatal("string-value occurrence must not match")
	}
	// Absent field.
	if s, _ := topLevelStringField([]byte(`{"a":"b"}`), "timestamp"); s != -1 {
		t.Fatal("absent field must return -1")
	}
}

// --- Bug G (#558): extractSessionID on Windows-style paths ---

// TestExtractSessionID_WindowsDottedDir verifies the Windows path where a dot
// inside a directory (username zhan.ju) previously corrupted the ID: the old
// code found no "/" and then LastIndex(".") hit the username dot, returning
// garbage like "C:\Users\zhan".
func TestExtractSessionID_WindowsDottedDir(t *testing.T) {
	cases := []struct {
		path string
		want string
	}{
		// Windows: dotted username directory — the reported bug.
		{`C:\Users\zhan.ju\.ggcode\sessions\20260801-abcdef.jsonl`, `20260801-abcdef`},
		// Windows: no directory at all.
		{`20260801-abcdef.jsonl`, `20260801-abcdef`},
		// Windows: multiple dots in the filename — only the LAST extension stripped.
		{`C:\sessions\my.session.v2.jsonl`, `my.session.v2`},
		// POSIX unchanged.
		{"/home/u/.ggcode/sessions/20260801-abcdef.jsonl", "20260801-abcdef"},
		{"/home/zhan.ju/sessions/20260801-abcdef.jsonl", "20260801-abcdef"},
		// No extension.
		{"/sessions/plainid", "plainid"},
	}
	for _, c := range cases {
		if got := extractSessionID(c.path); got != c.want {
			t.Errorf("extractSessionID(%q) = %q, want %q", c.path, got, c.want)
		}
	}
}

// --- Bug A (#558): cutoff recompute after migration rewrite ---

// writeJSONLLine appends one jsonlRecord to path.
func writeJSONLLine(t *testing.T, path string, rec jsonlRecord) {
	t.Helper()
	data, err := json.Marshal(rec)
	if err != nil {
		t.Fatal(err)
	}
	f, err := os.OpenFile(path, os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0600)
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()
	if _, err := f.Write(append(data, '\n')); err != nil {
		t.Fatal(err)
	}
}

// summaryMessageForMigration builds the legacy-format checkpoint summary
// message that migrateMessageIDs looks for inside checkpoint_messages.
func summaryMessageForMigration() provider.Message {
	return provider.Message{
		Role: "system",
		Content: []provider.ContentBlock{{
			Type: "text",
			Text: "[Previous conversation summary]\nEarlier messages were compacted.",
		}},
	}
}

// TestLoadSession_RecomputesCutoffAfterMigration is the #558 A signature test:
// a legacy session (old-format checkpoint with a 200-message snapshot, 550+
// stale messages outside the 24h window) is loaded for the first time. The
// migration rewrites the file (snapshot dropped, summary inserted). Before the
// fix, loadSession kept filtering with the pre-migration byte offset, leaking
// stale messages into the rendering window (probe: 63 vs 51). After the fix
// the cutoff is recomputed and the window matches a fresh full-load's window.
func TestLoadSession_RecomputesCutoffAfterMigration(t *testing.T) {
	dir, err := os.MkdirTemp("", "ggcode_558a_*")
	if err != nil {
		t.Fatal(err)
	}
	defer os.RemoveAll(dir)

	store, err := NewJSONLStore(dir)
	if err != nil {
		t.Fatal(err)
	}
	id := "mig-cutoff-558"
	path := filepath.Join(dir, id+".jsonl")

	writeJSONLLine(t, path, jsonlRecord{Type: "meta", SessionID: id, Title: "legacy", Workspace: dir})

	// 550 stale messages (> recentMessageThreshold=500), all 48h old —
	// outside the 24h RecentMessageWindow, no IDs (legacy format).
	stale := time.Now().Add(-48 * time.Hour)
	for i := 0; i < 550; i++ {
		writeJSONLLine(t, path, jsonlRecord{
			Type: "message",
			Message: &provider.Message{
				Role:    "user",
				Content: []provider.ContentBlock{{Type: "text", Text: fmt.Sprintf("stale %d", i)}},
			},
			Timestamp: stale.Add(time.Duration(i) * time.Millisecond),
		})
	}

	// Old-format checkpoint whose snapshot's last message is the 550th stale
	// message. The snapshot also contains the summary message.
	snapshot := make([]provider.Message, 0, 2)
	snapshot = append(snapshot, summaryMessageForMigration())
	snapshot = append(snapshot, provider.Message{
		Role:    "user",
		Content: []provider.ContentBlock{{Type: "text", Text: "stale 549"}},
	})
	writeJSONLLine(t, path, jsonlRecord{
		Type:               "checkpoint",
		SessionID:          id,
		CheckpointMessages: snapshot,
	})

	// 30 recent messages, inside the window — these are the ones that should
	// be rendered.
	now := time.Now()
	for i := 0; i < 30; i++ {
		writeJSONLLine(t, path, jsonlRecord{
			Type: "message",
			Message: &provider.Message{
				ID:      fmt.Sprintf("recent-%d", i),
				Role:    "user",
				Content: []provider.ContentBlock{{Type: "text", Text: fmt.Sprintf("recent %d", i)}},
			},
			Timestamp: now.Add(time.Duration(i) * time.Second),
		})
	}

	// First (migrating) load — this is the probe scenario.
	ses, err := store.Load(id)
	if err != nil {
		t.Fatalf("first Load: %v", err)
	}

	// The file must have actually been migrated (otherwise the test proves
	// nothing). New-format checkpoint + inserted summary message.
	data, _ := os.ReadFile(path)
	if !strings.Contains(string(data), `"checkpoint_summary_msg_id"`) {
		t.Fatal("test setup: migrateMessageIDs did not rewrite the file — scenario invalid")
	}

	staleCount := 0
	for _, m := range ses.Messages {
		for _, b := range m.Content {
			if strings.HasPrefix(b.Text, "stale ") {
				staleCount++
			}
		}
	}
	if staleCount > 0 {
		t.Fatalf("#558 A: %d stale (48h-old) messages leaked into the rendering window after migration; expected 0", staleCount)
	}

	// Consistency: the migrating load's window must equal a fresh (post-
	// migration, no-rewrite) load's window.
	ses2, err := store.Load(id)
	if err != nil {
		t.Fatalf("second Load: %v", err)
	}
	if len(ses2.Messages) != len(ses.Messages) {
		t.Fatalf("first (migrating) load returned %d messages, fresh load returns %d — stale offset leak (#558 A)", len(ses.Messages), len(ses2.Messages))
	}
}

// --- Bug D (#558): cross-process lost update between rename rewrite and append ---

// TestRewriteVsAppend_NoLostUpdateAcrossProcesses simulates the race from
// issue #558 D: a "rewriter" process does the read -> tmp -> rename cycle of
// migrateMessageIDs while an "appender" hammers the same file with O_APPEND
// writes (the appendRecordLines path, which now takes the same session flock).
// Before the fix, appends landing between the rewriter's read and rename were
// silently discarded. The deterministic interleaving is forced by wrapping the
// rewriter's critical section with the same lock the fix uses.
func TestRewriteVsAppend_NoLostUpdateAcrossProcesses(t *testing.T) {
	dir, err := os.MkdirTemp("", "ggcode_558d_*")
	if err != nil {
		t.Fatal(err)
	}
	defer os.RemoveAll(dir)

	path := filepath.Join(dir, "race.jsonl")
	seed := []string{`{"type":"meta","session_id":"race"}`, `{"type":"message","message":{"role":"user","content":[{"type":"text","text":"seed"}]}}`}
	f, _ := os.Create(path)
	for _, l := range seed {
		f.WriteString(l + "\n")
	}
	f.Close()

	const appends = 40
	var wg sync.WaitGroup
	start := make(chan struct{})

	// Appender goroutine: mimics another process doing O_APPEND writes
	// through appendRecordLines (which holds the session flock).
	wg.Add(1)
	go func() {
		defer wg.Done()
		<-start
		for i := 0; i < appends; i++ {
			rec := jsonlRecord{
				Type: "message",
				Message: &provider.Message{
					ID:      fmt.Sprintf("app-%d", i),
					Role:    "assistant",
					Content: []provider.ContentBlock{{Type: "text", Text: fmt.Sprintf("append %d", i)}},
				},
				Timestamp: time.Now(),
			}
			if err := appendRecordLines(path, []jsonlRecord{rec}); err != nil {
				t.Errorf("append %d: %v", i, err)
				return
			}
		}
	}()

	// Rewriter goroutine: mimics migrateMessageIDs' read -> tmp -> rename,
	// holding the session flock across the whole cycle (as the fix does).
	wg.Add(1)
	go func() {
		defer wg.Done()
		<-start
		unlock, lockErr := lockSessionFile(path)
		if lockErr != nil {
			t.Errorf("rewriter lock: %v", lockErr)
			return
		}
		var lines []string
		sf, err := os.Open(path)
		if err == nil {
			sc := bufio.NewScanner(sf)
			for sc.Scan() {
				if strings.TrimSpace(sc.Text()) != "" {
					lines = append(lines, sc.Text())
				}
			}
			sf.Close()
		}
		tmp := path + ".rw.tmp"
		df, err := os.Create(tmp)
		if err != nil {
			unlock()
			t.Errorf("rewriter create tmp: %v", err)
			return
		}
		for _, l := range lines {
			df.WriteString(l + "\n")
		}
		df.Sync()
		df.Close()
		os.Rename(tmp, path)
		unlock()
	}()

	close(start)
	wg.Wait()

	// Every appended message must survive the concurrent rewrite.
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	for i := 0; i < appends; i++ {
		want := fmt.Sprintf(`"app-%d"`, i)
		if !strings.Contains(string(data), want) {
			t.Fatalf("#558 D: appended message %q lost during concurrent rename rewrite", want)
		}
	}
	// And the seed line must still be there (rewrite preserved prior content).
	if !strings.Contains(string(data), `"seed"`) {
		t.Fatal("#558 D: seed record lost during rewrite")
	}
}

// TestLockSessionFile_SerializesExclusive verifies two exclusive session lock
// acquisitions cannot be held simultaneously (basic mutual exclusion of the
// flock sidecar reused from lockIndexFile).
func TestLockSessionFile_SerializesExclusive(t *testing.T) {
	dir, err := os.MkdirTemp("", "ggcode_558lock_*")
	if err != nil {
		t.Fatal(err)
	}
	defer os.RemoveAll(dir)
	path := filepath.Join(dir, "s.jsonl")

	unlock1, err := lockSessionFile(path)
	if err != nil {
		t.Fatalf("first lock: %v", err)
	}

	acquired := make(chan struct{})
	go func() {
		unlock2, err2 := lockSessionFile(path)
		if err2 != nil {
			t.Errorf("second lock: %v", err2)
			return
		}
		unlock2()
		close(acquired)
	}()

	select {
	case <-acquired:
		// flock without LOCK_NB is blocking — if the second holder got in
		// immediately while we still hold the lock, mutual exclusion failed.
		t.Error("second exclusive lock acquired while first still held")
	case <-time.After(150 * time.Millisecond):
		// Expected: blocked.
	}
	unlock1()

	select {
	case <-acquired:
	case <-time.After(2 * time.Second):
		t.Error("second lock never acquired after release — deadlock")
	}
}

// --- Bug F (#558): index MsgCount regression under windowed loading ---

// TestUpdateIndex_MsgCountDoesNotRegress verifies that updating an index entry
// from a time-windowed session (len(ses.Messages)=1) cannot shrink the stored
// MsgCount that a previous full-count update recorded (580 on disk). Before
// the fix, sessionToIndexEntry used the truncated length verbatim (#558 F).
func TestUpdateIndex_MsgCountDoesNotRegress(t *testing.T) {
	dir, err := os.MkdirTemp("", "ggcode_558f_*")
	if err != nil {
		t.Fatal(err)
	}
	defer os.RemoveAll(dir)

	store, err := NewJSONLStore(dir)
	if err != nil {
		t.Fatal(err)
	}
	id := "msgcount-558"

	// A full ses as the prior index writer would have seen it.
	full := &Session{ID: id, Title: "t", Workspace: dir, UpdatedAt: time.Now()}
	for i := 0; i < 580; i++ {
		full.Messages = append(full.Messages, provider.Message{
			ID:      fmt.Sprintf("m%d", i),
			Role:    "user",
			Content: []provider.ContentBlock{{Type: "text", Text: fmt.Sprintf("m%d", i)}},
		})
	}
	store.mu.Lock()
	if err := store.updateIndex(full); err != nil {
		store.mu.Unlock()
		t.Fatalf("updateIndex (full): %v", err)
	}
	store.mu.Unlock()

	// Windowed view of the SAME session: only the last message loaded.
	windowed := &Session{ID: id, Title: "t", Workspace: dir, UpdatedAt: time.Now()}
	windowed.Messages = full.Messages[len(full.Messages)-1:]
	store.mu.Lock()
	err = store.updateIndex(windowed)
	store.mu.Unlock()
	if err != nil {
		t.Fatalf("updateIndex (windowed): %v", err)
	}

	idx, err := store.loadIndex()
	if err != nil {
		t.Fatal(err)
	}
	for _, e := range idx {
		if e.ID != id {
			continue
		}
		if e.MsgCount < 580 {
			t.Fatalf("#558 F: index MsgCount regressed to %d after windowed update; want >= 580 (disk truth)", e.MsgCount)
		}
		return
	}
	t.Fatalf("session %s missing from index", id)
}
