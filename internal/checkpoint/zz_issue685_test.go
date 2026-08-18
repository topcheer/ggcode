package checkpoint

// Deterministic regression tests for #685 (and #696/#697, the test-quality
// follow-up): Revert must iterate files in deterministic checkpoint
// first-touch order — never Go's randomized map order — and, on partial
// write-back failure, disclose EXACTLY which files were restored and which
// remain pending.
//
// #696/#697: the previous version of this file pinned order only
// probabilistically (a map-order regression passed ~1/3 of runs because the
// substring assertions happened to hold for wrong-but-lucky permutations) and
// never checked disclosure accuracy against actual writes. These tests swap
// the restoreFile seam (checkpoint.go) so every attempted write is recorded
// and failures are injected per-path — deterministic regardless of map
// iteration order.

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
)

// seamRecorder wraps the restoreFile seam to record the exact sequence of
// write attempts. failPaths return errors instead of writing.
type seamRecorder struct {
	mu        sync.Mutex
	attempts  []string          // every path passed to the seam, in call order
	failPaths map[string]error  // paths whose write must fail
	_         map[string]string // placeholder for future content capture
}

func newSeamRecorder(failPaths map[string]error) *seamRecorder {
	return &seamRecorder{failPaths: failPaths}
}

func (r *seamRecorder) install(t *testing.T) {
	t.Helper()
	old := restoreFile
	restoreFile = func(path, content string, existed bool) error {
		r.mu.Lock()
		defer r.mu.Unlock()
		r.attempts = append(r.attempts, path)
		if err, ok := r.failPaths[path]; ok {
			return err
		}
		if !existed && content == "" {
			// Mirror restoreCheckpointState's creation-removal semantics.
			if err := os.Remove(path); err != nil && !errors.Is(err, os.ErrNotExist) {
				return err
			}
			return nil
		}
		return os.WriteFile(path, []byte(content), 0o644)
	}
	t.Cleanup(func() { restoreFile = old })
}

// TestIssue685_SuccessDeterministicFirstTouchOrder pins the success path:
// three checkpoints a→b→c (c a creation) must be written back in exactly
// a,b,c order, disk must hold the pre-edit contents afterwards, and the
// created file must be removed. A regression to `for f := range targets`
// (map order) fails the exact-sequence assertion on ~5/6 of runs — and any
// order missing one of the three files fails 100% of runs.
func TestIssue685_SuccessDeterministicFirstTouchOrder(t *testing.T) {
	dir := t.TempDir()
	m := NewManager(50)

	aPath := filepath.Join(dir, "a.go")
	bPath := filepath.Join(dir, "b.go")
	cPath := filepath.Join(dir, "c.go")

	// Simulate the edit chain: a (old-a → new-a), b (old-b → new-b),
	// c (created, existed=false).
	if err := os.WriteFile(aPath, []byte("old-a"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(bPath, []byte("old-b"), 0o644); err != nil {
		t.Fatal(err)
	}
	m.SaveWithExistence(aPath, "old-a", "new-a", "edit_file", true)
	m.SaveWithExistence(bPath, "old-b", "new-b", "edit_file", true)
	m.SaveWithExistence(cPath, "", "new-c", "edit_file", false)

	rec := newSeamRecorder(nil)
	rec.install(t)

	target := m.List()[0].ID
	if _, err := m.Revert(target); err != nil {
		t.Fatalf("revert: %v", err)
	}

	// 1. Exact deterministic order — first-touch checkpoint order.
	wantOrder := []string{aPath, bPath, cPath}
	if fmt.Sprint(rec.attempts) != fmt.Sprint(wantOrder) {
		t.Fatalf("write order must be deterministic first-touch order %v, got %v", wantOrder, rec.attempts)
	}

	// 2. Disk contents restored to pre-edit states.
	for path, want := range map[string]string{aPath: "old-a", bPath: "old-b"} {
		got, err := os.ReadFile(path)
		if err != nil {
			t.Fatalf("read %s: %v", path, err)
		}
		if string(got) != want {
			t.Fatalf("%s content = %q, want %q", path, got, want)
		}
	}
	// 3. The created-then-reverted file must be gone (not a stray 0-byte file).
	if _, err := os.Stat(cPath); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("created-then-reverted file must be removed, stat err = %v", err)
	}
	// 4. History truncated after success.
	if len(m.checkpoints) != 0 {
		t.Fatalf("history must be truncated after successful revert, len=%d", len(m.checkpoints))
	}
}

// TestIssue685_FailureC_DisclosureAccurate pins the 3-file partial-failure
// case: c fails, a and b restored. The error message's restored list must
// contain EXACTLY {a,b} and the pending list EXACTLY {c} — a lying disclosure
// (listing b as restored when order was [a,c,b] and b was never written)
// previously escaped 100% of runs.
func TestIssue685_FailureC_DisclosureAccurate(t *testing.T) {
	dir := t.TempDir()
	m := NewManager(50)

	aPath := filepath.Join(dir, "a.go")
	bPath := filepath.Join(dir, "b.go")
	cPath := filepath.Join(dir, "c.go")
	for _, p := range []string{aPath, bPath, cPath} {
		if err := os.WriteFile(p, []byte("old"), 0o644); err != nil {
			t.Fatal(err)
		}
		m.SaveWithExistence(p, "old", "new", "edit_file", true)
	}

	rec := newSeamRecorder(map[string]error{
		cPath: &os.PathError{Op: "write", Path: cPath, Err: os.ErrPermission},
	})
	rec.install(t)

	_, err := m.Revert(m.List()[0].ID)
	if err == nil {
		t.Fatal("expected revert failure when c.go write fails")
	}
	msg := err.Error()

	// Order: deterministic a,b,c; the attempt on c failed so only a,b hit disk.
	wantOrder := []string{aPath, bPath, cPath}
	if fmt.Sprint(rec.attempts) != fmt.Sprint(wantOrder) {
		t.Fatalf("write order must be %v, got %v", wantOrder, rec.attempts)
	}

	// Disclosure accuracy: message must name the failing file, list restored
	// EXACTLY {a,b}, and pending EXACTLY {c} (Go %v slice rendering, e.g.
	// "restored [.../a.go .../b.go]; still pending [.../c.go]").
	if !strings.Contains(msg, cPath) {
		t.Fatalf("error must name the failing file: %q", msg)
	}
	if !assertPathListExact(msg, "restored ", []string{aPath, bPath}) {
		t.Fatalf("error's restored list must be exactly [a b]: %q", msg)
	}
	if !assertPathListExact(msg, "still pending ", []string{cPath}) {
		t.Fatalf("error's pending list must be exactly [c]: %q", msg)
	}

	// Disk state matches the disclosure: a and b restored, c untouched.
	for _, p := range []string{aPath, bPath} {
		got, rerr := os.ReadFile(p)
		if rerr != nil || string(got) != "old" {
			t.Fatalf("%s must be restored to %q, got %q (err=%v)", p, "old", got, rerr)
		}
	}
	// History stays intact — retry remains possible.
	if len(m.checkpoints) != 3 {
		t.Fatalf("history must be kept on failure, len=%d", len(m.checkpoints))
	}
}

// TestIssue685_FailureA_NoPartialClaim pins the first-file-failure case:
// nothing restored, so the error must NOT claim any partial restore.
func TestIssue685_FailureA_NoPartialClaim(t *testing.T) {
	dir := t.TempDir()
	m := NewManager(50)

	aPath := filepath.Join(dir, "a.go")
	bPath := filepath.Join(dir, "b.go")
	for _, p := range []string{aPath, bPath} {
		if err := os.WriteFile(p, []byte("old"), 0o644); err != nil {
			t.Fatal(err)
		}
		m.SaveWithExistence(p, "old", "new", "edit_file", true)
	}

	rec := newSeamRecorder(map[string]error{
		aPath: &os.PathError{Op: "write", Path: aPath, Err: os.ErrPermission},
	})
	rec.install(t)

	_, err := m.Revert(m.List()[0].ID)
	if err == nil {
		t.Fatal("expected failure")
	}
	msg := err.Error()

	// Only a was attempted (first in order) and it failed.
	if len(rec.attempts) != 1 || rec.attempts[0] != aPath {
		t.Fatalf("only the first file may be attempted, got %v", rec.attempts)
	}
	if strings.Contains(msg, "partial state") || strings.Contains(msg, "restored") {
		t.Fatalf("first-file failure must not claim partial restore: %q", msg)
	}
	if !strings.Contains(msg, aPath) {
		t.Fatalf("error must name failing file: %q", msg)
	}
	// History intact — retry remains possible.
	if len(m.checkpoints) != 2 {
		t.Fatalf("history must be kept on failure, len=%d", len(m.checkpoints))
	}
}

// assertPathListExact checks that msg contains the Go %v slice rendering of
// exactly the given paths after label ("<label>[a b]") — i.e. the disclosure
// lists the files and ONLY those files. A swapped, missing, or extra entry
// fails the full-string containment.
func assertPathListExact(msg, label string, want []string) bool {
	if len(want) == 0 {
		return !strings.Contains(msg, label)
	}
	rend := fmt.Sprintf("%v", want) // [path1 path2]
	return strings.Contains(msg, label+rend)
}
