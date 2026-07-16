package session

import (
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/topcheer/ggcode/internal/provider"
)

func TestTryAcquireSessionLock_FirstInstance(t *testing.T) {
	dir := t.TempDir()
	sessionID := "test-session-1"

	lock, err := TryAcquireSessionLock(dir, sessionID)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if lock == nil {
		t.Fatal("expected non-nil lock")
	}
	if !lock.Acquired() {
		t.Fatal("expected lock to be acquired")
	}
	defer lock.Release()

	// Lock file should exist.
	lockPath := LockFilePath(dir, sessionID)
	if _, err := os.Stat(lockPath); os.IsNotExist(err) {
		t.Error("lock file should exist")
	}
}

func TestTryAcquireSessionLock_SecondInstanceDenied(t *testing.T) {
	dir := t.TempDir()
	sessionID := "test-session-2"

	// First lock.
	lock1, err := TryAcquireSessionLock(dir, sessionID)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !lock1.Acquired() {
		t.Fatal("first lock should be acquired")
	}
	defer lock1.Release()

	// Second lock attempt should fail (return non-acquired lock).
	lock2, err := TryAcquireSessionLock(dir, sessionID)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if lock2 == nil {
		t.Fatal("expected non-nil lock (to report holder)")
	}
	if lock2.Acquired() {
		t.Fatal("second lock should NOT be acquired")
	}
}

func TestSessionLock_Release(t *testing.T) {
	dir := t.TempDir()
	sessionID := "test-session-3"

	lock1, _ := TryAcquireSessionLock(dir, sessionID)
	if !lock1.Acquired() {
		t.Fatal("first lock should be acquired")
	}
	lock1.Release()

	// After release, should be able to acquire again.
	lock2, err := TryAcquireSessionLock(dir, sessionID)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !lock2.Acquired() {
		t.Fatal("should acquire after release")
	}
	lock2.Release()
}

func TestIsSessionLocked(t *testing.T) {
	dir := t.TempDir()
	sessionID := "test-session-4"

	if IsSessionLocked(dir, sessionID) {
		t.Fatal("should not be locked initially")
	}

	lock, _ := TryAcquireSessionLock(dir, sessionID)
	if !lock.Acquired() {
		t.Fatal("should acquire")
	}
	defer lock.Release()

	if !IsSessionLocked(dir, sessionID) {
		t.Fatal("should be locked after acquire")
	}
}

func TestIsSessionLocked_NoLockFile(t *testing.T) {
	dir := t.TempDir()
	if IsSessionLocked(dir, "nonexistent") {
		t.Fatal("should not be locked when no lock file exists")
	}
}

func TestLockFilePath(t *testing.T) {
	path := LockFilePath("/tmp/sessions", "abc-123")
	expected := filepath.Join("/tmp/sessions", "abc-123.lock")
	if path != expected {
		t.Errorf("got %q, want %q", path, expected)
	}
}

func TestLatestForWorkspace(t *testing.T) {
	dir := t.TempDir()
	store, err := NewJSONLStore(dir)
	if err != nil {
		t.Fatal(err)
	}

	// Create two sessions for workspace-a and one for workspace-b.
	ses1 := NewSession("vendor", "endpoint", "model")
	ses1.Workspace = "/tmp/ws-a"
	ses1.Messages = []provider.Message{
		{Role: "user", Content: []provider.ContentBlock{{Type: "text", Text: "hello"}}},
	}
	saveFullForTest(t, store, ses1)

	// Small delay to ensure different UpdatedAt.
	time.Sleep(10 * time.Millisecond)

	ses2 := NewSession("vendor", "endpoint", "model")
	ses2.Workspace = "/tmp/ws-a"
	ses2.Messages = []provider.Message{
		{Role: "user", Content: []provider.ContentBlock{{Type: "text", Text: "world"}}},
	}
	saveFullForTest(t, store, ses2)

	ses3 := NewSession("vendor", "endpoint", "model")
	ses3.Workspace = "/tmp/ws-b"
	ses3.Messages = []provider.Message{
		{Role: "user", Content: []provider.ContentBlock{{Type: "text", Text: "other"}}},
	}
	saveFullForTest(t, store, ses3)

	// ws-a should return ses2 (most recently updated).
	latest, err := store.LatestForWorkspace("/tmp/ws-a")
	if err != nil {
		t.Fatal(err)
	}
	if latest == nil {
		t.Fatal("expected non-nil session for ws-a")
	}
	if latest.ID != ses2.ID {
		t.Errorf("expected session %s, got %s", ses2.ID, latest.ID)
	}

	// ws-b should return ses3.
	latest3, err := store.LatestForWorkspace("/tmp/ws-b")
	if err != nil {
		t.Fatal(err)
	}
	if latest3 == nil || latest3.ID != ses3.ID {
		t.Errorf("expected session %s for ws-b", ses3.ID)
	}

	// Non-existent workspace should return nil.
	latest4, err := store.LatestForWorkspace("/tmp/nope")
	if err != nil {
		t.Fatal(err)
	}
	if latest4 != nil {
		t.Error("expected nil for non-existent workspace")
	}
}

func TestLatestForWorkspace_EmptySession(t *testing.T) {
	dir := t.TempDir()
	store, err := NewJSONLStore(dir)
	if err != nil {
		t.Fatal(err)
	}

	// Create a session with no messages.
	ses1 := NewSession("vendor", "endpoint", "model")
	ses1.Workspace = "/tmp/ws-empty"
	saveFullForTest(t, store, ses1)

	// Should return nil — no session with messages.
	latest, err := store.LatestForWorkspace("/tmp/ws-empty")
	if err != nil {
		t.Fatal(err)
	}
	if latest != nil {
		t.Error("expected nil for workspace with only empty sessions")
	}
}

func TestSessionLock_ReleaseDeletesFile(t *testing.T) {
	dir := t.TempDir()
	sessionID := "test-release-deletes"

	lock, _ := TryAcquireSessionLock(dir, sessionID)
	if !lock.Acquired() {
		t.Fatal("should acquire")
	}

	lockPath := LockFilePath(dir, sessionID)
	if _, err := os.Stat(lockPath); err != nil {
		t.Fatalf("lock file should exist after acquire: %v", err)
	}

	lock.Release()

	if _, err := os.Stat(lockPath); !os.IsNotExist(err) {
		t.Errorf("lock file should be deleted after Release, got err=%v", err)
	}
}

func TestIsSessionLocked_RemovesStaleLock(t *testing.T) {
	dir := t.TempDir()
	sessionID := "test-stale-removal"

	// Create a stale lock file (no flock held — simulates crashed process).
	lockPath := LockFilePath(dir, sessionID)
	if err := os.WriteFile(lockPath, []byte("99999"), 0o600); err != nil {
		t.Fatal(err)
	}

	// IsSessionLocked should detect it's stale and remove it.
	if IsSessionLocked(dir, sessionID) {
		t.Fatal("stale lock should not report as locked")
	}

	if _, err := os.Stat(lockPath); !os.IsNotExist(err) {
		t.Errorf("stale lock file should be removed, got err=%v", err)
	}
}

func TestCleanupStaleLocks(t *testing.T) {
	dir := t.TempDir()

	// Create two stale lock files (no flock held).
	stale1 := LockFilePath(dir, "stale-session-1")
	stale2 := LockFilePath(dir, "stale-session-2")
	_ = os.WriteFile(stale1, []byte("11111"), 0o600)
	_ = os.WriteFile(stale2, []byte("22222"), 0o600)

	// Also create a live lock that should NOT be removed.
	liveLock, _ := TryAcquireSessionLock(dir, "live-session")
	if !liveLock.Acquired() {
		t.Fatal("should acquire live lock")
	}
	defer liveLock.Release()

	CleanupStaleLocks(dir)

	// Stale files should be gone.
	if _, err := os.Stat(stale1); !os.IsNotExist(err) {
		t.Errorf("stale lock 1 should be cleaned up, got err=%v", err)
	}
	if _, err := os.Stat(stale2); !os.IsNotExist(err) {
		t.Errorf("stale lock 2 should be cleaned up, got err=%v", err)
	}

	// Live lock file should still exist.
	livePath := LockFilePath(dir, "live-session")
	if _, err := os.Stat(livePath); err != nil {
		t.Errorf("live lock should NOT be cleaned up, got err=%v", err)
	}
}
