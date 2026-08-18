package session

import (
	"bufio"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/topcheer/ggcode/internal/provider"
)

// TestLoadSession_OverlongLineNotFatal (#656): a single >10MB JSONL line
// (e.g. a base64 image blob) must not abort the whole session load. Records
// before AND after the oversized line must come back; the poison line itself
// is skipped. Previously bufio.Scanner's ErrTooLong made loadSession return
// a hard error and the ENTIRE session became unrecoverable (the #478 fix
// only covered the search path).
func TestLoadSession_OverlongLineNotFatal(t *testing.T) {
	dir := t.TempDir()
	store, err := NewJSONLStore(dir)
	if err != nil {
		t.Fatal(err)
	}

	ses := NewSession("zai", "cn-coding-openai", "glm-5-turbo")
	ses.Title = "Blob Session"
	ses.Workspace = "/tmp/ws-656"
	ses.Messages = []provider.Message{
		{Role: "user", Content: []provider.ContentBlock{{Type: "text", Text: "before the blob"}}},
		{Role: "assistant", Content: []provider.ContentBlock{{Type: "text", Text: "after the blob"}}},
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

	// Splice an >10MB poison line between the two message records, mirroring
	// the issue's reproduction (base64 ImageData inline).
	path := filepath.Join(dir, ses.ID+".jsonl")
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	lines := strings.Split(strings.TrimRight(string(data), "\n"), "\n")
	if len(lines) < 3 {
		t.Fatalf("unexpected seed file layout: %d lines", len(lines))
	}
	blob := `{"type":"message","message":{"role":"user","content":[{"type":"image","source":{"type":"base64","data":"` +
		strings.Repeat("A", 11*1024*1024) + `"}}]}}`
	spliced := strings.Join(append([]string{lines[0], blob}, lines[1:]...), "\n") + "\n"
	if err := os.WriteFile(path, []byte(spliced), 0o644); err != nil {
		t.Fatal(err)
	}

	loaded, err := store.Load(ses.ID)
	if err != nil {
		t.Fatalf("Load must survive an over-long line instead of hard-failing (#656): %v", err)
	}
	var sawBefore, sawAfter bool
	for _, m := range loaded.Messages {
		for _, b := range m.Content {
			if strings.Contains(b.Text, "before the blob") {
				sawBefore = true
			}
			if strings.Contains(b.Text, "after the blob") {
				sawAfter = true
			}
		}
	}
	if !sawBefore || !sawAfter {
		t.Fatalf("messages around the poison line must both load, got before=%v after=%v", sawBefore, sawAfter)
	}
}

// TestLoadSession_OverlongTornTailLineNotFatal (#656 variant): the poison
// line is ALSO the last line and lacks a trailing newline (torn oversized
// write). The load must still succeed with everything before it.
func TestLoadSession_OverlongTornTailLineNotFatal(t *testing.T) {
	dir := t.TempDir()
	store, err := NewJSONLStore(dir)
	if err != nil {
		t.Fatal(err)
	}

	ses := NewSession("zai", "cn-coding-openai", "glm-5-turbo")
	ses.Title = "Torn Blob"
	ses.Workspace = "/tmp/ws-656b"
	ses.Messages = []provider.Message{
		{Role: "user", Content: []provider.ContentBlock{{Type: "text", Text: "survivor"}}},
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

	path := filepath.Join(dir, ses.ID+".jsonl")
	f, err := os.OpenFile(path, os.O_WRONLY|os.O_APPEND, 0600)
	if err != nil {
		t.Fatal(err)
	}
	// >10MB line WITHOUT trailing newline: must be skipped, not fatal.
	if _, err := f.WriteString(`{"type":"message","junk":"` + strings.Repeat("B", 11*1024*1024)); err != nil {
		t.Fatal(err)
	}
	f.Close()

	loaded, err := store.Load(ses.ID)
	if err != nil {
		t.Fatalf("Load must survive a torn over-long tail line (#656): %v", err)
	}
	if len(loaded.Messages) == 0 {
		t.Fatal("messages before the torn tail must still load")
	}
}

// TestReadLineLimitedCounted verifies the counted bounded-reader primitive:
// normal lines pass through with their exact consumed size, oversized lines
// are consumed-and-skipped reporting the discarded byte count.
func TestReadLineLimitedCounted(t *testing.T) {
	dir := t.TempDir()
	p := filepath.Join(dir, "f.txt")
	big := strings.Repeat("x", 50)
	if err := os.WriteFile(p, []byte("one\n"+big+"\nthree\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	f, err := os.Open(p)
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()

	br := bufio.NewReader(f)
	l1, c1, e1 := readLineLimitedCounted(br, 10)
	if e1 != nil || strings.TrimSpace(string(l1)) != "one" || c1 != 4 {
		t.Fatalf("line1: %q consumed=%d err=%v", l1, c1, e1)
	}
	l2, c2, e2 := readLineLimitedCounted(br, 10)
	if e2 != errLineTooLong || len(l2) != 0 || c2 != 51 {
		t.Fatalf("oversized line must yield errLineTooLong with discarded=51, got %q consumed=%d err=%v", l2, c2, e2)
	}
	l3, c3, e3 := readLineLimitedCounted(br, 10)
	if e3 != nil || strings.TrimSpace(string(l3)) != "three" || c3 != 6 {
		t.Fatalf("line3 after skip: %q consumed=%d err=%v", l3, c3, e3)
	}
	// EOF after a clean terminator: empty read, io.EOF, no phantom loop.
	l4, c4, _ := readLineLimitedCounted(br, 10)
	if len(l4) != 0 || c4 != 0 {
		t.Fatalf("post-EOF read must be empty, got %q consumed=%d", l4, c4)
	}
}
