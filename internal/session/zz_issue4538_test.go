package session

import (
	"os"
	"path/filepath"
	"testing"
)

// #4538: EnsureMeta's failed-write cleanup must not delete records another
// process appended into the just-O_EXCL-created session file. The guard is
// removeCreatedFileIfUnappended: under the same flock appendRecordLines uses,
// remove only when the file never grew past our own bytes.

func writeFileT(t *testing.T, path, content string) {
	t.Helper()
	if err := os.WriteFile(path, []byte(content), 0600); err != nil {
		t.Fatalf("write %s: %v", path, err)
	}
}

func TestIssue4538_CleanupRemovesUnappendedCreatedFile(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "sess-4538a.jsonl")
	// File contains exactly what our own failed write could have produced.
	writeFileT(t, path, `{"type":"meta","session_id":"a"}`)

	removeCreatedFileIfUnappended(path, int64(len(`{"type":"meta","session_id":"a"}`)))

	if _, err := os.Stat(path); !os.IsNotExist(err) {
		t.Fatalf("unappended file should be removed, stat err = %v", err)
	}
}

func TestIssue4538_CleanupKeepsFileWithConcurrentAppends(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "sess-4538b.jsonl")
	self := `{"type":"meta","session_id":"b"}` + "\n"
	foreign := `{"type":"message","role":"user","content":"appended by other process"}` + "\n"
	// Other process appended after our create: file is larger than selfBytes.
	writeFileT(t, path, self+foreign)

	removeCreatedFileIfUnappended(path, int64(len(self)))

	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("file with concurrent appends must be preserved: %v", err)
	}
	if string(data) != self+foreign {
		t.Fatalf("preserved content mutated: %q", string(data))
	}
}

func TestIssue4538_CleanupToleratesMissingFile(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "sess-4538c.jsonl")
	// No panic, no error surface: helper is best-effort cleanup.
	removeCreatedFileIfUnappended(path, 0)
	if _, err := os.Stat(path); !os.IsNotExist(err) {
		t.Fatalf("file should still not exist, stat err = %v", err)
	}
}

func TestIssue4538_CleanupWaitsForSessionFlock(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "sess-4538d.jsonl")
	self := `{"type":"meta"}`
	writeFileT(t, path, self)

	// Simulate appendRecordLines in another process: hold the flock, append
	// foreign bytes, then release. The helper must serialize behind the lock
	// and observe the post-append size, not the pre-append one.
	unlock, err := lockSessionFile(path)
	if err != nil {
		t.Fatalf("lockSessionFile: %v", err)
	}
	foreign := "foreign-append"
	go func() {
		defer unlock()
		f, err := os.OpenFile(path, os.O_WRONLY|os.O_APPEND, 0600)
		if err != nil {
			return
		}
		f.WriteString(foreign)
		f.Close()
	}()

	removeCreatedFileIfUnappended(path, int64(len(self)))

	if _, err := os.Stat(path); os.IsNotExist(err) {
		t.Fatal("file holding another process's append was removed")
	}
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read: %v", err)
	}
	if string(data) != self+foreign {
		t.Fatalf("appended bytes lost: %q", string(data))
	}
}
