package checkpoint

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
)

type seamRecorder struct {
	mu        sync.Mutex
	attempts  []string
	failPaths map[string]error
	_         map[string]string
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
			if err := os.Remove(path); err != nil && !errors.Is(err, os.ErrNotExist) {
				return err
			}
			return nil
		}
		return os.WriteFile(path, []byte(content), 0o644)
	}
	t.Cleanup(func() { restoreFile = old })
}

func TestIssue685_SuccessDeterministicFirstTouchOrder(t *testing.T) {
	dir := t.TempDir()
	m := NewManager(50)

	aPath := filepath.Join(dir, "a.go")
	bPath := filepath.Join(dir, "b.go")
	cPath := filepath.Join(dir, "c.go")

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

	wantOrder := []string{aPath, bPath, cPath}
	if fmt.Sprint(rec.attempts) != fmt.Sprint(wantOrder) {
		t.Fatalf("write order must be deterministic first-touch order %v, got %v", wantOrder, rec.attempts)
	}

	for path, want := range map[string]string{aPath: "old-a", bPath: "old-b"} {
		got, err := os.ReadFile(path)
		if err != nil {
			t.Fatalf("read %s: %v", path, err)
		}
		if string(got) != want {
			t.Fatalf("%s content = %q, want %q", path, got, want)
		}
	}
	if _, err := os.Stat(cPath); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("created-then-reverted file must be removed, stat err = %v", err)
	}
	if len(m.checkpoints) != 0 {
		t.Fatalf("history must be truncated after successful revert, len=%d", len(m.checkpoints))
	}
}

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

	wantOrder := []string{aPath, bPath, cPath}
	if fmt.Sprint(rec.attempts) != fmt.Sprint(wantOrder) {
		t.Fatalf("write order must be %v, got %v", wantOrder, rec.attempts)
	}

	if !strings.Contains(msg, cPath) {
		t.Fatalf("error must name the failing file: %q", msg)
	}
	if !assertPathListExact(msg, "restored ", []string{aPath, bPath}) {
		t.Fatalf("error's restored list must be exactly [a b]: %q", msg)
	}
	if !assertPathListExact(msg, "still pending ", []string{cPath}) {
		t.Fatalf("error's pending list must be exactly [c]: %q", msg)
	}

	for _, p := range []string{aPath, bPath} {
		got, rerr := os.ReadFile(p)
		if rerr != nil || string(got) != "old" {
			t.Fatalf("%s must be restored to %q, got %q (err=%v)", p, "old", got, rerr)
		}
	}
	if len(m.checkpoints) != 3 {
		t.Fatalf("history must be kept on failure, len=%d", len(m.checkpoints))
	}
}

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

	if len(rec.attempts) != 1 || rec.attempts[0] != aPath {
		t.Fatalf("only the first file may be attempted, got %v", rec.attempts)
	}
	if strings.Contains(msg, "partial state") || strings.Contains(msg, "restored") {
		t.Fatalf("first-file failure must not claim partial restore: %q", msg)
	}
	if !strings.Contains(msg, aPath) {
		t.Fatalf("error must name failing file: %q", msg)
	}
	if len(m.checkpoints) != 2 {
		t.Fatalf("history must be kept on failure, len=%d", len(m.checkpoints))
	}
}

func assertPathListExact(msg, label string, want []string) bool {
	if len(want) == 0 {
		return !strings.Contains(msg, label)
	}
	rend := fmt.Sprintf("%v", want)
	return strings.Contains(msg, label+rend)
}
