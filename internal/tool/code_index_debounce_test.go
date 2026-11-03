package tool

import (
	"os"
	"path/filepath"
	"testing"
	"time"
)

func TestCodeIndexDebouncedRebuild(t *testing.T) {

	dir := t.TempDir()
	srcFile := filepath.Join(dir, "auth.go")
	if err := os.WriteFile(srcFile, []byte("package auth\n\nfunc Login() {}\n"), 0644); err != nil {
		t.Fatalf("write file: %v", err)
	}

	m := NewCodeIndexManager(dir)
	defer m.Stop()
	m.StartBackgroundIndex()

	waitForReady(t, m, 15*time.Second)

	results, err := m.Search("login", 10)
	if err != nil {
		t.Fatalf("initial search failed: %v", err)
	}
	if len(results) == 0 {
		t.Fatal("expected search results for 'login'")
	}

	time.Sleep(1500 * time.Millisecond)

	if err := os.WriteFile(srcFile, []byte("package auth\n\nfunc Login() {}\n\nfunc Logout() {}\n"), 0644); err != nil {
		t.Fatalf("write file: %v", err)
	}

	m.MarkDirty([]string{srcFile})

	deadline := time.Now().Add(20 * time.Second)
	found := false
	for time.Now().Before(deadline) {
		results, err := m.Search("logout", 10)
		if err == nil && len(results) > 0 {
			found = true
			break
		}
		time.Sleep(500 * time.Millisecond)
	}
	if !found {
		t.Fatal("expected 'logout' in search results after debounced rebuild, but got none")
	}
}

func TestCodeIndexMarkDirtyNoPanicWhenNotStarted(t *testing.T) {
	dir := t.TempDir()
	m := NewCodeIndexManager(dir)
	defer m.Stop()

	m.MarkDirty([]string{filepath.Join(dir, "test.go")})
}

func waitForReady(t *testing.T, m *CodeIndexManager, timeout time.Duration) {
	t.Helper()
	deadline := time.Now().Add(timeout)
	for time.Now().Before(deadline) {
		if m.IsReady() {
			return
		}
		time.Sleep(200 * time.Millisecond)
	}
	t.Fatalf("index not ready within %v", timeout)
}
