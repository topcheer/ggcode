package platform

import (
	"errors"
	"os"
	"path/filepath"
	"testing"
)

func TestWorkspaceResolveInsideRoot(t *testing.T) {
	root := t.TempDir()
	sub := filepath.Join(root, "proj")
	if err := os.MkdirAll(sub, 0o755); err != nil {
		t.Fatal(err)
	}
	ws, _ := LoadWorkspaceSet(filepath.Join(t.TempDir(), "ws.json"))
	if err := ws.Add(root); err != nil {
		t.Fatal(err)
	}
	got, err := ws.Resolve(sub)
	// macOS /var -> /private/var: expectations must be symlink-resolved too.
	want, _ := filepath.EvalSymlinks(sub)
	if err != nil || got != want {
		t.Fatalf("Resolve(sub) = %q, %v; want %q", got, err, want)
	}
	// root itself is allowed
	if _, err := ws.Resolve(root); err != nil {
		t.Fatalf("Resolve(root): %v", err)
	}
}

func TestWorkspaceRejectsOutsideAndTraversal(t *testing.T) {
	root := t.TempDir()
	other := t.TempDir()
	ws, _ := LoadWorkspaceSet(filepath.Join(t.TempDir(), "ws.json"))
	_ = ws.Add(root)
	if _, err := ws.Resolve(other); !errors.Is(err, ErrWorkspaceNotAllowed) {
		t.Fatalf("outside root: want ErrWorkspaceNotAllowed, got %v", err)
	}
	// '..' traversal attempts end up outside after Clean, so rejected.
	escape := filepath.Join(root, "..", "elsewhere")
	_ = os.MkdirAll(escape, 0o755) // must exist so resolution reaches the allowlist check
	if _, err := ws.Resolve(escape); !errors.Is(err, ErrWorkspaceNotAllowed) {
		t.Fatalf("traversal: want ErrWorkspaceNotAllowed, got %v", err)
	}
}

func TestWorkspaceRejectsRelativeMissingNonDir(t *testing.T) {
	ws, _ := LoadWorkspaceSet(filepath.Join(t.TempDir(), "ws.json"))
	if _, err := ws.Resolve("relative/path"); err == nil || errors.Is(err, ErrWorkspaceNotAllowed) {
		t.Fatalf("relative path should error, got %v", err)
	}
	if _, err := ws.Resolve(filepath.Join(t.TempDir(), "nope")); err == nil {
		t.Fatal("missing dir accepted")
	}
	file := filepath.Join(t.TempDir(), "f.txt")
	_ = os.WriteFile(file, []byte("x"), 0o644)
	if _, err := ws.Resolve(file); err == nil {
		t.Fatal("file accepted as workspace")
	}
}

func TestWorkspaceAddRemovePersist(t *testing.T) {
	path := filepath.Join(t.TempDir(), "ws.json")
	root := t.TempDir()
	ws, _ := LoadWorkspaceSet(path)
	if err := ws.Add(root); err != nil {
		t.Fatal(err)
	}
	if err := ws.Add(root); err == nil {
		t.Fatal("duplicate root accepted")
	}
	if err := ws.Save(); err != nil {
		t.Fatal(err)
	}
	ws2, err := LoadWorkspaceSet(path)
	if err != nil {
		t.Fatal(err)
	}
	if len(ws2.List()) != 1 {
		t.Fatalf("persisted roots = %v", ws2.List())
	}
	if err := ws2.Remove(root); err != nil {
		t.Fatal(err)
	}
	if err := ws2.Remove(root); err == nil {
		t.Fatal("removing unknown root should fail")
	}
}
