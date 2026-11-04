package sessionidx

import (
	"fmt"
	"os"
	"path/filepath"
	"testing"
)

// writeSession writes a minimal session jsonl with the given message lines.
func writeSession(t *testing.T, dir, id string, lines []string) {
	t.Helper()
	os.MkdirAll(dir, 0o700)
	var buf string
	for _, l := range lines {
		buf += l + "\n"
	}
	if err := os.WriteFile(filepath.Join(dir, id+".jsonl"), []byte(buf), 0o600); err != nil {
		t.Fatal(err)
	}
}

func msgLine(role, text string) string {
	// structural twin of a real "message" record (only fields decodeMessageRecord reads)
	return fmt.Sprintf(`{"type":"message","timestamp":"2026-11-04T10:00:00Z","message":{"role":%q,"content":[{"type":"text","text":%q}]}}`, role, text)
}

func TestIndexRoundTripAndQuery(t *testing.T) {
	dir := t.TempDir()
	writeSession(t, dir, "s1", []string{
		msgLine("user", "please fix the login race in auth module"),
		`{"type":"meta","title":"noise"}`,
		msgLine("assistant", "the race was fixed in commit abc123 by locking the session map"),
		msgLine("tool", `{"tool_result":"grep found race at auth.go:42"}`),
	})

	ix, err := Load(dir, "s1")
	if err != nil {
		t.Fatal(err)
	}
	if err := ix.Refresh(); err != nil {
		t.Fatal(err)
	}
	if ix.data.IndexedTo == 0 {
		t.Fatal("nothing indexed")
	}

	hits := ix.Query([]string{"race"}, 10)
	if len(hits) == 0 {
		t.Fatal("query 'race' expected hits")
	}
	// user line + assistant line both mention "race"
	if len(hits) < 2 {
		t.Fatalf("expected >=2 hits, got %d", len(hits))
	}

	// AND semantics: both terms must hit the same line
	hits = ix.Query([]string{"race", "auth"}, 10)
	if len(hits) == 0 {
		t.Fatal("AND query race+auth expected hits")
	}
	for _, h := range hits {
		line, err := ix.ReadLineAt(h.Offset, h.Length)
		if err != nil {
			t.Fatal(err)
		}
		s := string(line)
		if !containsFold(s, "race") || !containsFold(s, "auth") {
			t.Fatalf("hit line missing terms: %s", s[:min(80, len(s))])
		}
	}

	// no-hit term
	if hits := ix.Query([]string{"nonexistentzzz"}, 10); len(hits) != 0 {
		t.Fatalf("expected 0 hits, got %d", len(hits))
	}
}

func TestIncrementalTail(t *testing.T) {
	dir := t.TempDir()
	p := filepath.Join(dir, "s2.jsonl")
	os.WriteFile(p, []byte(msgLine("user", "first message about alpha")+string('\n')), 0o600)

	ix, _ := Load(dir, "s2")
	if err := ix.Refresh(); err != nil {
		t.Fatal(err)
	}
	after1 := ix.data.IndexedTo

	// append more (the only mutation path in production)
	f, _ := os.OpenFile(p, os.O_APPEND|os.O_WRONLY, 0o600)
	f.WriteString(msgLine("assistant", "second message about beta") + "\n")
	f.Close()

	if err := ix.Refresh(); err != nil {
		t.Fatal(err)
	}
	if ix.data.IndexedTo <= after1 {
		t.Fatal("tail did not advance")
	}
	if hits := ix.Query([]string{"beta"}, 10); len(hits) == 0 {
		t.Fatal("incremental term not found after tail")
	}
	if hits := ix.Query([]string{"alpha"}, 10); len(hits) == 0 {
		t.Fatal("old term lost after tail")
	}
}

func TestTruncatedSourceRebuild(t *testing.T) {
	dir := t.TempDir()
	p := filepath.Join(dir, "s3.jsonl")
	os.WriteFile(p, []byte(msgLine("user", "long content here")+string('\n')), 0o600)
	ix, _ := Load(dir, "s3")
	ix.Refresh()

	// simulate shrink (defensive path)
	os.WriteFile(p, []byte(msgLine("user", "tiny")+string('\n')), 0o600)
	if err := ix.Refresh(); err != nil {
		t.Fatal(err)
	}
	if hits := ix.Query([]string{"long"}, 10); len(hits) != 0 {
		t.Fatal("stale postings survived rebuild")
	}
}

func TestManagerQueryAcrossSessions(t *testing.T) {
	dir := t.TempDir()
	writeSession(t, dir, "live", []string{msgLine("user", "needle in live session")})
	writeSession(t, dir, "other", []string{msgLine("assistant", "needle in sibling session")})

	// simulate the sibling's owning instance having built its idx
	oix, _ := Load(dir, "other")
	oix.Refresh()

	m := NewManager(dir, "live")
	m.refreshEvery = 1 // fast for test
	m.Start()
	defer m.Stop()

	// self got indexed by Start
	if m.Self() == nil {
		t.Fatal("self index missing")
	}

	res := m.Query([]string{"needle"}, "", 10)
	if len(res) == 0 {
		t.Fatal("manager query found nothing")
	}
	sawLive, sawOther := false, false
	for _, r := range res {
		if r.SessionID == "live" {
			sawLive = true
		}
		if r.SessionID == "other" {
			sawOther = true
		}
	}
	if !sawLive {
		t.Fatal("live session hit missing")
	}
	if !sawOther {
		t.Fatal("sibling session hit missing (external idx not loaded)")
	}

	// targeted query
	res = m.Query([]string{"needle"}, "other", 10)
	if len(res) == 0 || res[0].SessionID != "other" {
		t.Fatal("session-targeted query failed")
	}
}

func containsFold(s, sub string) bool {
	return len(sub) > 0 && (s == sub || len(s) >= len(sub) && indexFold(s, sub) >= 0)
}

func indexFold(s, sub string) int {
	for i := 0; i+len(sub) <= len(s); i++ {
		if equalFold(s[i:i+len(sub)], sub) {
			return i
		}
	}
	return -1
}

func equalFold(a, b string) bool {
	if len(a) != len(b) {
		return false
	}
	for i := 0; i < len(a); i++ {
		ca, cb := a[i], b[i]
		if 'A' <= ca && ca <= 'Z' {
			ca += 32
		}
		if 'A' <= cb && cb <= 'Z' {
			cb += 32
		}
		if ca != cb {
			return false
		}
	}
	return true
}

func min(a, b int) int {
	if a < b {
		return a
	}
	return b
}
