package session

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

	if s, _ := topLevelStringField([]byte(`{"msg":{"text":"timestamp"},"other":"v"}`), "timestamp"); s != -1 {
		t.Fatal("string-value occurrence must not match")
	}

	if s, _ := topLevelStringField([]byte(`{"a":"b"}`), "timestamp"); s != -1 {
		t.Fatal("absent field must return -1")
	}
}

func TestExtractSessionID_WindowsDottedDir(t *testing.T) {
	cases := []struct {
		path string
		want string
	}{

		{`C:\Users\zhan.ju\.ggcode\sessions\20260801-abcdef.jsonl`, `20260801-abcdef`},

		{`20260801-abcdef.jsonl`, `20260801-abcdef`},

		{`C:\sessions\my.session.v2.jsonl`, `my.session.v2`},

		{"/home/u/.ggcode/sessions/20260801-abcdef.jsonl", "20260801-abcdef"},
		{"/home/zhan.ju/sessions/20260801-abcdef.jsonl", "20260801-abcdef"},

		{"/sessions/plainid", "plainid"},
	}
	for _, c := range cases {
		if got := extractSessionID(c.path); got != c.want {
			t.Errorf("extractSessionID(%q) = %q, want %q", c.path, got, c.want)
		}
	}
}

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

func summaryMessageForMigration() provider.Message {
	return provider.Message{
		Role: "system",
		Content: []provider.ContentBlock{{
			Type: "text",
			Text: "[Previous conversation summary]\nEarlier messages were compacted.",
		}},
	}
}

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

	ses, err := store.Load(id)
	if err != nil {
		t.Fatalf("first Load: %v", err)
	}

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

	ses2, err := store.Load(id)
	if err != nil {
		t.Fatalf("second Load: %v", err)
	}
	if len(ses2.Messages) != len(ses.Messages) {
		t.Fatalf("first (migrating) load returned %d messages, fresh load returns %d — stale offset leak (#558 A)", len(ses.Messages), len(ses2.Messages))
	}
}

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

	if !strings.Contains(string(data), `"seed"`) {
		t.Fatal("#558 D: seed record lost during rewrite")
	}
}

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

		t.Error("second exclusive lock acquired while first still held")
	case <-time.After(150 * time.Millisecond):

	}
	unlock1()

	select {
	case <-acquired:
	case <-time.After(2 * time.Second):
		t.Error("second lock never acquired after release — deadlock")
	}
}

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
