package session

import (
	"bufio"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/topcheer/ggcode/internal/provider"
)

// TestSearchSessions_OverlongLineKeepsHits (#478): a session file containing
// a single >10MB JSONL line (e.g. a base64 image blob) must not abort the
// whole scan. Hits from lines BEFORE the oversized line and from lines AFTER
// it must both be returned; the oversized line itself is skipped.
func TestSearchSessions_OverlongLineKeepsHits(t *testing.T) {
	dir := t.TempDir()
	store, err := NewJSONLStore(dir)
	if err != nil {
		t.Fatal(err)
	}

	ses := NewSession("zai", "default", "glm")
	ses.Title = "Blob Session"
	ses.Messages = []provider.Message{
		{Role: "user", Content: []provider.ContentBlock{{Type: "text", Text: "seed"}}},
	}
	if err := store.Save(ses); err != nil {
		t.Fatal(err)
	}
	// AppendMessagesBatchToDisk writes the JSONL AND registers the session
	// in the on-disk index (updateIndex) — loadIndex-driven SearchSessions
	// only sees sessions present there.
	if err := store.AppendMessagesBatchToDisk(ses, ses.Messages); err != nil {
		t.Fatal(err)
	}

	// Hand-craft the JSONL on disk: hit -> >10MB oversized line -> hit.
	// This mirrors the issue's reproduction (base64 ImageData inline).
	path := filepath.Join(dir, ses.ID+".jsonl")
	hit1 := `{"type":"message","message":{"role":"user","content":[{"type":"text","text":"How do I implement OAuth2 token refresh?"}]}}`
	hit2 := `{"type":"message","message":{"role":"assistant","content":[{"type":"text","text":"OAuth2 refresh token grant flow explained"}]}}`
	blob := `{"type":"message","message":{"role":"user","content":[{"type":"image","source":{"type":"base64","data":"` +
		strings.Repeat("A", 11*1024*1024) + `"}}]}}`
	content := hit1 + "\n" + blob + "\n" + hit2 + "\n"
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}

	results, err := store.SearchSessions("oauth2", 0)
	if err != nil {
		t.Fatalf("SearchSessions returned error: %v", err)
	}
	if len(results) != 2 {
		t.Fatalf("expected 2 hits around the oversized line, got %d — hits silently dropped (#478)", len(results))
	}
	// Both sides of the blob must be present.
	seenUser, seenAssistant := false, false
	for _, r := range results {
		if r.Role == "user" {
			seenUser = true
		}
		if r.Role == "assistant" {
			seenAssistant = true
		}
	}
	if !seenUser || !seenAssistant {
		t.Fatalf("expected hits from both before (user) and after (assistant) the blob line, got user=%v assistant=%v", seenUser, seenAssistant)
	}
}

// TestReadLineLimited verifies the bounded reader primitive: normal lines
// pass through, oversized lines are consumed-and-skipped without killing
// the stream.
func TestReadLineLimited(t *testing.T) {
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
	l1, e1 := readLineLimited(br, 10)
	if e1 != nil || strings.TrimSpace(string(l1)) != "one" {
		t.Fatalf("line1: %q err=%v", l1, e1)
	}
	l2, e2 := readLineLimited(br, 10)
	if e2 != errLineTooLong || len(l2) != 0 {
		t.Fatalf("oversized line must yield errLineTooLong, got %q err=%v", l2, e2)
	}
	l3, e3 := readLineLimited(br, 10)
	if e3 != nil || strings.TrimSpace(string(l3)) != "three" {
		t.Fatalf("line3 after skip: %q err=%v", l3, e3)
	}
}
