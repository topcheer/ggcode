package checkpoint

// Regression tests for issue #685 (regression of #678):
//  1. Revert with partial write-back failure must iterate files in
//     deterministic checkpoint order (not Go's randomized map order) so the
//     set of restored files is reproducible, and the error must disclose
//     exactly which files WERE restored and which remain pending.
//  2. The history must stay intact on failure (retryable), matching
//     writeBaselines' policy.

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// failingFS simulates write failures for specific paths. It records every
// attempted write so tests can assert both order and disclosure.
type issue685FS struct {
	real      string // backing dir for successful writes
	failPaths map[string]bool
	attempts  []string
}

func (f *issue685FS) writeFile(path string, data []byte) error {
	f.attempts = append(f.attempts, path)
	if f.failPaths[path] {
		return &os.PathError{Op: "write", Path: path, Err: os.ErrPermission}
	}
	return os.WriteFile(filepath.Join(f.real, filepath.Base(path)), data, 0644)
}

func TestIssue685_RevertDeterministicOrderAndDisclosure(t *testing.T) {
	dir := t.TempDir()
	m := NewManager(50)

	// Simulate a checkpoint chain: cp0 (a.go), cp1 (b.go), cp2 (c.go).
	// Reverting to cp0 must restore a.go, b.go, c.go — in first-touch order.
	steps := []struct {
		path, old, new string
		existed        bool
	}{
		{filepath.Join(dir, "a.go"), "old-a", "new-a", true},
		{filepath.Join(dir, "b.go"), "old-b", "new-b", true},
		{filepath.Join(dir, "c.go"), "", "new-c", false},
	}
	for _, st := range steps {
		if err := os.WriteFile(st.path, []byte(st.old), 0644); err != nil && st.existed {
			t.Fatal(err)
		}
		m.SaveWithExistence(st.path, st.old, st.new, "edit_file", st.existed)
	}

	cps := m.List()
	target := cps[0].ID
	cp, err := m.Revert(target)
	if err != nil {
		t.Fatalf("revert: %v", err)
	}
	if cp == nil {
		t.Fatal("expected checkpoint result")
	}
	// All three files should have been restored (write to real paths).
	if len(m.checkpoints) != 0 {
		t.Fatalf("history must be truncated after successful revert, len=%d", len(m.checkpoints))
	}
}

func TestIssue685_RevertFailure_DisclosesRestoredAndPending(t *testing.T) {
	dir := t.TempDir()
	m := NewManager(50)

	aPath := filepath.Join(dir, "a.go")
	bPath := filepath.Join(dir, "b.go")
	cPath := filepath.Join(dir, "c.go")
	for _, p := range []string{aPath, bPath, cPath} {
		if err := os.WriteFile(p, []byte("old"), 0644); err != nil {
			t.Fatal(err)
		}
	}

	// Record checkpoints in order a, b, c.
	for _, p := range []string{aPath, bPath, cPath} {
		m.SaveWithExistence(p, "old", "new", "edit_file", true)
	}

	// Make the c.go write-back fail deterministically on every platform:
	// replace the file with a same-name directory — every write strategy
	// (direct write, temp-file + rename) fails; os.Chmod injection is a no-op
	// on Windows (same lesson as #667/#673, 3905b3c6).
	if err := os.Remove(cPath); err != nil {
		t.Fatalf("remove for dir-swap: %v", err)
	}
	if err := os.Mkdir(cPath, 0o755); err != nil {
		t.Fatalf("dir-swap: %v", err)
	}
	t.Cleanup(func() { _ = os.RemoveAll(cPath) })

	target := m.List()[0].ID
	_, err := m.Revert(target)
	if err == nil {
		t.Fatal("expected revert failure when last file is unwritable")
	}
	// Disclosure: the error must name c.go as the failure and disclose that
	// a.go and b.go were restored (partial state).
	msg := err.Error()
	if !strings.Contains(msg, cPath) {
		t.Fatalf("error must name the failing file: %q", msg)
	}
	if !strings.Contains(msg, "restored") || !strings.Contains(msg, aPath) || !strings.Contains(msg, bPath) {
		t.Fatalf("error must disclose restored files a,b: %q", msg)
	}
	if !strings.Contains(msg, "pending") {
		t.Fatalf("error must disclose pending state: %q", msg)
	}
	// History must remain intact (retryable).
	if len(m.checkpoints) != 3 {
		t.Fatalf("history must be kept on failure, len=%d", len(m.checkpoints))
	}
}

func TestIssue685_RevertFailure_FirstFileFails_NoPartialClaim(t *testing.T) {
	dir := t.TempDir()
	m := NewManager(50)

	aPath := filepath.Join(dir, "a.go")
	bPath := filepath.Join(dir, "b.go")
	for _, p := range []string{aPath, bPath} {
		if err := os.WriteFile(p, []byte("old"), 0644); err != nil {
			t.Fatal(err)
		}
	}
	for _, p := range []string{aPath, bPath} {
		m.SaveWithExistence(p, "old", "new", "edit_file", true)
	}

	// First-file failure injection: same-name directory swap on a.go
	// (deterministic cross-platform, see test above).
	if err := os.Remove(aPath); err != nil {
		t.Fatalf("remove for dir-swap: %v", err)
	}
	if err := os.Mkdir(aPath, 0o755); err != nil {
		t.Fatalf("dir-swap: %v", err)
	}
	t.Cleanup(func() { _ = os.RemoveAll(aPath) })

	target := m.List()[0].ID
	_, err := m.Revert(target)
	if err == nil {
		t.Fatal("expected failure")
	}
	// First-file failure: no "restored" list, no false partial claim.
	msg := err.Error()
	if strings.Contains(msg, "restored") {
		t.Fatalf("first-file failure must not claim partial restore: %q", msg)
	}
	if !strings.Contains(msg, aPath) {
		t.Fatalf("error must name failing file: %q", msg)
	}
}
