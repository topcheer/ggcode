package session

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/topcheer/ggcode/internal/provider"
)

// TestAppendRecordLines_TerminatesTornTail (#657): a crash mid-write leaves
// a final line without '\n'. The next append must first terminate that
// residue so the new record lands on its own line instead of fusing into a
// malformed line that load silently drops (losing BOTH records).
func TestAppendRecordLines_TerminatesTornTail(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "s.jsonl")

	good := `{"type":"message","message":{"role":"user","content":[{"type":"text","text":"ok"}]}}`
	torn := `{"type":"mess` // crash residue: no trailing newline
	if err := os.WriteFile(path, []byte(good+"\n"+torn), 0o644); err != nil {
		t.Fatal(err)
	}

	rec := jsonlRecord{
		Type: "message",
		Message: &provider.Message{
			Role:    "assistant",
			Content: []provider.ContentBlock{{Type: "text", Text: "next record"}},
		},
	}
	if err := appendRecordLines(path, []jsonlRecord{rec}); err != nil {
		t.Fatal(err)
	}

	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	lines := strings.Split(strings.TrimRight(string(data), "\n"), "\n")
	if len(lines) != 3 {
		t.Fatalf("expected 3 lines (good, torn residue, new record), got %d: %q", len(lines), data)
	}
	if lines[1] != torn {
		t.Fatalf("torn residue must stay on its own line, got %q", lines[1])
	}
	var check jsonlRecord
	if err := json.Unmarshal([]byte(lines[2]), &check); err != nil {
		t.Fatalf("appended record must be its own valid JSON line, got %q: %v", lines[2], err)
	}
	if check.Message == nil || check.Message.Role != "assistant" {
		t.Fatalf("appended record content mismatch: %+v", check)
	}
}

// TestAppendThenLoad_TornTailDoesNotEatNextRecord (#657 end-to-end):
// simulate a crash-torn append, then append a real record through the store.
// The load must surface both the pre-crash and the post-crash record — only
// the torn half-line is lost (and now logged), never the record after it.
func TestAppendThenLoad_TornTailDoesNotEatNextRecord(t *testing.T) {
	dir := t.TempDir()
	store, err := NewJSONLStore(dir)
	if err != nil {
		t.Fatal(err)
	}

	ses := NewSession("zai", "cn-coding-openai", "glm-5-turbo")
	ses.Title = "Crash Session"
	ses.Workspace = "/tmp/ws-657"
	ses.Messages = []provider.Message{
		{Role: "user", Content: []provider.ContentBlock{{Type: "text", Text: "before crash"}}},
	}
	if err := store.Save(ses); err != nil {
		t.Fatal(err)
	}
	if err := store.AppendMetaToDisk(ses); err != nil {
		t.Fatal(err)
	}
	if err := store.AppendMessagesBatchToDisk(ses, ses.Messages); err != nil {
		t.Fatal(err)
	}

	// Simulate crash mid-append: raw torn record bytes, no newline.
	path := filepath.Join(dir, ses.ID+".jsonl")
	f, err := os.OpenFile(path, os.O_WRONLY|os.O_APPEND, 0600)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := f.WriteString(`{"type":"usage","usage_entry":{"to`); err != nil {
		t.Fatal(err)
	}
	f.Close()

	// Next launch appends a full record through the normal path.
	ses.Messages = []provider.Message{
		{Role: "assistant", Content: []provider.ContentBlock{{Type: "text", Text: "after crash"}}},
	}
	if err := store.AppendMessagesBatchToDisk(ses, ses.Messages); err != nil {
		t.Fatal(err)
	}

	loaded, err := store.Load(ses.ID)
	if err != nil {
		t.Fatalf("Load failed: %v", err)
	}
	var sawBefore, sawAfter bool
	for _, m := range loaded.Messages {
		for _, b := range m.Content {
			if strings.Contains(b.Text, "before crash") {
				sawBefore = true
			}
			if strings.Contains(b.Text, "after crash") {
				sawAfter = true
			}
		}
	}
	if !sawBefore || !sawAfter {
		t.Fatalf("torn tail must not eat the next record: before=%v after=%v", sawBefore, sawAfter)
	}
}

// TestTerminateTornTail covers the primitive: clean tails and empty files
// stay untouched; torn tails gain exactly one newline.
func TestTerminateTornTail(t *testing.T) {
	dir := t.TempDir()

	// Clean tail: unchanged.
	p1 := filepath.Join(dir, "clean.jsonl")
	if err := os.WriteFile(p1, []byte("a\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	f1, err := os.OpenFile(p1, os.O_CREATE|os.O_RDWR|os.O_APPEND, 0600)
	if err != nil {
		t.Fatal(err)
	}
	if err := terminateTornTail(f1); err != nil {
		t.Fatal(err)
	}
	f1.Close()
	d1, _ := os.ReadFile(p1)
	if string(d1) != "a\n" {
		t.Fatalf("clean tail must be untouched, got %q", d1)
	}

	// Torn tail: exactly one newline appended.
	p2 := filepath.Join(dir, "torn.jsonl")
	if err := os.WriteFile(p2, []byte("a"), 0o644); err != nil {
		t.Fatal(err)
	}
	f2, err := os.OpenFile(p2, os.O_CREATE|os.O_RDWR|os.O_APPEND, 0600)
	if err != nil {
		t.Fatal(err)
	}
	if err := terminateTornTail(f2); err != nil {
		t.Fatal(err)
	}
	f2.Close()
	d2, _ := os.ReadFile(p2)
	if string(d2) != "a\n" {
		t.Fatalf("torn tail must gain one newline, got %q", d2)
	}

	// Empty (or missing) file: stays empty.
	p3 := filepath.Join(dir, "empty.jsonl")
	f3, err := os.OpenFile(p3, os.O_CREATE|os.O_RDWR|os.O_APPEND, 0600)
	if err != nil {
		t.Fatal(err)
	}
	if err := terminateTornTail(f3); err != nil {
		t.Fatal(err)
	}
	f3.Close()
	st, err := os.Stat(p3)
	if err != nil {
		t.Fatal(err)
	}
	if st.Size() != 0 {
		t.Fatalf("empty file must stay empty, got %d bytes", st.Size())
	}
}
