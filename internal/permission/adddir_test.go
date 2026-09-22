package permission

import (
	"os"
	"path/filepath"
	"testing"
)

func TestPathSandboxAddAllowedDir(t *testing.T) {
	root := t.TempDir()
	outside := t.TempDir()
	// Pre-create nested dir so resolvePath resolves it fully.
	nested := filepath.Join(root, "sibling-repo")
	if err := os.MkdirAll(nested, 0o755); err != nil {
		t.Fatal(err)
	}

	s := NewPathSandbox([]string{root})
	if !s.Allowed(filepath.Join(nested, "file.txt")) {
		t.Fatal("file inside root should already be allowed")
	}

	if !s.AddAllowedDir(outside) {
		t.Fatal("AddAllowedDir(outside) = false, want true")
	}
	if !s.Allowed(filepath.Join(outside, "x.go")) {
		t.Error("path in newly added dir should be allowed")
	}

	// Duplicate add must be a no-op and must not grow the list.
	if s.AddAllowedDir(outside) {
		t.Error("duplicate AddAllowedDir = true, want false")
	}
	if got := len(s.AllowedDirs()); got != 2 {
		t.Errorf("AllowedDirs len = %d, want 2", got)
	}

	// Empty string is rejected.
	if s.AddAllowedDir("   ") {
		t.Error("AddAllowedDir(whitespace) = true, want false")
	}
}

func TestPathSandboxAddAllowedDirFailClosed(t *testing.T) {
	// Simulate the #573-F fail-closed sandbox: no resolvable dirs.
	s := &PathSandbox{getwdFailed: true}
	if s.AddAllowedDir(t.TempDir()) {
		t.Error("AddAllowedDir on fail-closed sandbox = true, want false")
	}
}

func TestConfigPolicyAddAllowedDir(t *testing.T) {
	root := t.TempDir()
	outside := t.TempDir()

	p := NewConfigPolicy(nil, []string{root})
	if len(p.AllowedDirs()) != 1 {
		t.Fatalf("initial AllowedDirs = %v, want single root", p.AllowedDirs())
	}
	if !p.AddAllowedDir(outside) {
		t.Fatal("ConfigPolicy.AddAllowedDir = false, want true")
	}
	if len(p.AllowedDirs()) != 2 {
		t.Errorf("AllowedDirs after add = %v, want 2 entries", p.AllowedDirs())
	}
	// Concurrent snapshot must not alias the sandbox slice.
	snap := p.AllowedDirs()
	_ = p.AddAllowedDir(t.TempDir())
	if len(snap) != 2 {
		t.Errorf("snapshot mutated by later add: len = %d, want 2", len(snap))
	}
}
