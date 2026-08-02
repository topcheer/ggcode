package tool

import (
	"os"
	"path/filepath"
	"testing"
	"time"
)

// TestCodeIndexDebouncedRebuild verifies that MarkDirty triggers a prompt
// incremental rebuild (within the debounce window) rather than waiting for
// the 5-minute periodic tick.
func TestCodeIndexDebouncedRebuild(t *testing.T) {
	// Create a temporary working directory with a source file.
	dir := t.TempDir()
	srcFile := filepath.Join(dir, "auth.go")
	if err := os.WriteFile(srcFile, []byte("package auth\n\nfunc Login() {}\n"), 0644); err != nil {
		t.Fatalf("write file: %v", err)
	}

	m := NewCodeIndexManager(dir)
	defer m.Stop()
	m.StartBackgroundIndex()

	// Wait for the initial build to complete.
	waitForReady(t, m, 15*time.Second)

	// Verify initial search works.
	results, err := m.Search("login", 10)
	if err != nil {
		t.Fatalf("initial search failed: %v", err)
	}
	if len(results) == 0 {
		t.Fatal("expected search results for 'login'")
	}

	// Sleep to ensure mtime changes at second granularity.
	// doBuild compares ModTime().Unix() (seconds), so writes within the
	// same second are indistinguishable.
	time.Sleep(1500 * time.Millisecond)

	// Modify the file: add a new function with a unique name.
	if err := os.WriteFile(srcFile, []byte("package auth\n\nfunc Login() {}\n\nfunc Logout() {}\n"), 0644); err != nil {
		t.Fatalf("write file: %v", err)
	}

	// Signal dirty - this should trigger a debounced rebuild.
	m.MarkDirty([]string{srcFile})

	// Wait for the debounced rebuild to complete (debounce 3s + build).
	// We poll until searching for "logout" returns results, or timeout.
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

// TestCodeIndexMarkDirtyNoPanicWhenNotStarted verifies MarkDirty is safe
// to call before the background loop is running.
func TestCodeIndexMarkDirtyNoPanicWhenNotStarted(t *testing.T) {
	dir := t.TempDir()
	m := NewCodeIndexManager(dir)
	defer m.Stop()

	// MarkDirty before StartBackgroundIndex should not panic.
	m.MarkDirty([]string{filepath.Join(dir, "test.go")})
}

// waitForReady polls IsReady until the index is available or deadline expires.
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
