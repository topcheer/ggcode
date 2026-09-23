package checkpoint

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestPersistAppendLoadRoundtrip(t *testing.T) {
	dir := t.TempDir()
	p := NewPersist(dir, "sess-1")
	ts := time.Now()
	p.Append(Checkpoint{ID: "c1", FilePath: "/p/a.go", OldContent: "old", NewContent: "new", Existed: true, Timestamp: ts, ToolCall: "edit_file"})
	p.Append(Checkpoint{ID: "c2", FilePath: "/p/b.go", OldContent: "", NewContent: "brand new", Existed: false, Timestamp: ts, ToolCall: "write_file"})
	if err := p.Close(); err != nil {
		t.Fatalf("Close: %v", err)
	}

	got, err := LoadSessionRecords(dir, "sess-1")
	if err != nil {
		t.Fatalf("LoadSessionRecords: %v", err)
	}
	if len(got) != 2 {
		t.Fatalf("got %d records, want 2", len(got))
	}
	if got[0].ID != "c1" || got[0].OldContent != "old" || !got[0].Existed {
		t.Errorf("record 0 mismatch: %+v", got[0])
	}
	if got[1].ID != "c2" || got[1].Existed || got[1].NewContent != "brand new" {
		t.Errorf("record 1 mismatch: %+v", got[1])
	}
}

func TestPersistTornTailTolerated(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, SessionFileName("s"))
	good := `{"id":"c1","file_path":"/p/a.go","old_content":"o","new_content":"n","existed":true}`
	if err := os.WriteFile(path, []byte(good+"\n{\"id\":\"c2\", \"file_pat"), 0o600); err != nil {
		t.Fatal(err)
	}
	got, err := LoadSessionRecords(dir, "s")
	if err != nil {
		t.Fatalf("LoadSessionRecords: %v", err)
	}
	if len(got) != 1 || got[0].ID != "c1" {
		t.Fatalf("want 1 intact record, got %+v", got)
	}
}

func TestPersistAppendWithoutSessionNoop(t *testing.T) {
	dir := t.TempDir()
	p := NewPersist(dir, "")
	p.Append(Checkpoint{ID: "c1", FilePath: "/p/a.go"})
	if err := p.Close(); err != nil {
		t.Fatalf("Close: %v", err)
	}
	sessions, err := ListPersistedSessions(dir)
	if err != nil {
		t.Fatal(err)
	}
	if len(sessions) != 0 {
		t.Fatalf("expected empty store, got %d sessions", len(sessions))
	}
}

func TestPersistSetSessionSwitchesFile(t *testing.T) {
	dir := t.TempDir()
	p := NewPersist(dir, "s1")
	p.Append(Checkpoint{ID: "c1", FilePath: "/p/a.go"})
	p.SetSession("s2")
	p.Append(Checkpoint{ID: "c2", FilePath: "/p/b.go"})
	if err := p.Close(); err != nil {
		t.Fatalf("Close: %v", err)
	}
	r1, err := LoadSessionRecords(dir, "s1")
	if err != nil || len(r1) != 1 || r1[0].ID != "c1" {
		t.Fatalf("s1 records: %+v err=%v", r1, err)
	}
	r2, err := LoadSessionRecords(dir, "s2")
	if err != nil || len(r2) != 1 || r2[0].ID != "c2" {
		t.Fatalf("s2 records: %+v err=%v", r2, err)
	}
}

func TestSessionFileNameSanitization(t *testing.T) {
	name := SessionFileName("../../etc/passwd")
	if strings.ContainsRune(name, '/') || filepath.Base(name) != name {
		t.Fatalf("path traversal survived sanitization: %q", name)
	}
	if !strings.HasPrefix(name, "ggundo-") || !strings.HasSuffix(name, ".jsonl") {
		t.Fatalf("unexpected name %q", name)
	}
	if SessionFileName("") != "ggundo-session.jsonl" {
		t.Fatalf("empty id: %q", SessionFileName(""))
	}
	// '.' is kept (safe inside a single component); '/' becomes '_'.
	if id, ok := sessionIDFromFileName(name); !ok || id != ".._.._etc_passwd" {
		t.Fatalf("roundtrip id=%q ok=%v", id, ok)
	}
}

func TestPrunePersistedSessions(t *testing.T) {
	dir := t.TempDir()
	base := time.Now().Add(-time.Hour)
	for i := 0; i < 5; i++ {
		path := filepath.Join(dir, SessionFileName(strings.Repeat("s", i+1)))
		if err := os.WriteFile(path, []byte{}, 0o600); err != nil {
			t.Fatal(err)
		}
		if err := os.Chtimes(path, base.Add(time.Duration(i)*time.Minute), base.Add(time.Duration(i)*time.Minute)); err != nil {
			t.Fatal(err)
		}
	}
	if err := PrunePersistedSessions(dir, 2); err != nil {
		t.Fatalf("Prune: %v", err)
	}
	left, err := ListPersistedSessions(dir)
	if err != nil {
		t.Fatal(err)
	}
	if len(left) != 2 {
		t.Fatalf("want 2 sessions left, got %d", len(left))
	}
	// Newest must survive: the last-written file (mtime base+4m).
	if left[0].ID != "sssss" {
		t.Fatalf("newest survivor = %q, want sssss", left[0].ID)
	}
}

func TestSessionBaselinesFirstRecordWins(t *testing.T) {
	records := []Checkpoint{
		{ID: "1", FilePath: "/p/a.go", OldContent: "v1", NewContent: "v2", Existed: true},
		{ID: "2", FilePath: "/p/b.go", OldContent: "", NewContent: "new file", Existed: false},
		{ID: "3", FilePath: "/p/a.go", OldContent: "v2", NewContent: "v3", Existed: true},
	}
	bl := SessionBaselines(records)
	if len(bl) != 2 {
		t.Fatalf("want 2 baselines, got %d", len(bl))
	}
	if bl[0].OldContent != "v1" || bl[0].FinalContent != "v3" {
		t.Errorf("a.go baseline wrong: %+v", bl[0])
	}
	if bl[1].Existed || bl[1].FinalContent != "new file" {
		t.Errorf("b.go baseline wrong: %+v", bl[1])
	}
}

func TestRollbackSessionBaselines(t *testing.T) {
	dir := t.TempDir()
	modified := filepath.Join(dir, "modified.txt")
	created := filepath.Join(dir, "created.txt")
	restored := filepath.Join(dir, "sub", "restored.txt")
	if err := os.MkdirAll(filepath.Dir(restored), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(modified, []byte("agent wrecked this"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(created, []byte("agent made this"), 0o644); err != nil {
		t.Fatal(err)
	}

	bl := []SessionBaseline{
		{FilePath: modified, Existed: true, OldContent: "original content", FinalContent: "agent wrecked this"},
		{FilePath: created, Existed: false, OldContent: "", FinalContent: "agent made this"},
		// File the agent modified and something later removed: restore recreates it.
		{FilePath: restored, Existed: true, OldContent: "precious", FinalContent: "gone"},
	}
	results := RollbackSessionBaselines(bl)
	actions := map[string]string{}
	for _, r := range results {
		if r.Err != nil {
			t.Fatalf("rollback %s: %v", r.FilePath, r.Err)
		}
		actions[r.FilePath] = r.Action
	}
	if actions[modified] != "restored" || actions[created] != "deleted" || actions[restored] != "restored" {
		t.Fatalf("actions = %v", actions)
	}
	if got, _ := os.ReadFile(modified); string(got) != "original content" {
		t.Errorf("modified.txt = %q", got)
	}
	if _, err := os.Stat(created); !os.IsNotExist(err) {
		t.Errorf("created.txt still exists: %v", err)
	}
	if got, _ := os.ReadFile(restored); string(got) != "precious" {
		t.Errorf("restored.txt = %q", got)
	}

	// Re-rolling back is idempotent: content already matches baseline.
	results = RollbackSessionBaselines(bl[:1])
	if results[0].Action != "unchanged" || results[0].Err != nil {
		t.Fatalf("idempotent rollback: %+v", results[0])
	}
}

func TestCountLines(t *testing.T) {
	cases := map[string]int{"": 0, "a": 1, "a\n": 1, "a\nb": 2, "a\nb\n": 2, "a\nb\nc": 3}
	for in, want := range cases {
		if got := CountLines(in); got != want {
			t.Errorf("CountLines(%q) = %d, want %d", in, got, want)
		}
	}
}

// TestManagerSavePersistsToDisk verifies the Manager→Persist write-through:
// a Manager with SetPersist mirrors every Save to the session's on-disk
// record; a Manager without one keeps working unchanged (persist is nil).
func TestManagerSavePersistsToDisk(t *testing.T) {
	dir := t.TempDir()
	m := NewManager(10)
	m.SetPersist(NewPersist(dir, "s-mgr"))
	m.StartRun("run-1")
	cp := m.SaveWithExistence("/p/x.go", "old body", "new body", "edit_file", true)

	got, err := LoadSessionRecords(dir, "s-mgr")
	if err != nil {
		t.Fatalf("LoadSessionRecords: %v", err)
	}
	if len(got) != 1 || got[0].ID != cp.ID || got[0].OldContent != "old body" || got[0].RunID != "run-1" {
		t.Fatalf("persisted record mismatch: %+v (cp %+v)", got, cp)
	}

	// A manager without persistence must not touch the store.
	m2 := NewManager(10)
	m2.Save("/p/y.go", "a", "b", "edit_file")
	entries, _ := os.ReadDir(dir)
	if len(entries) != 1 {
		t.Fatalf("persist-less manager wrote %d store files, want 1", len(entries))
	}
}
