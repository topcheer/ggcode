package wailskit

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/topcheer/ggcode/internal/agent"
	"github.com/topcheer/ggcode/internal/agentruntime"
	"github.com/topcheer/ggcode/internal/provider"
	"github.com/topcheer/ggcode/internal/session"
	"github.com/topcheer/ggcode/internal/tool"
)

// newSessionLifecycleTestBridge builds a ChatBridge with a JSONL store in a
// temp HOME so session lifecycle helpers can be exercised without touching
// the user's real ~/.ggcode.
func newSessionLifecycleTestBridge(t *testing.T) (*ChatBridge, *session.JSONLStore) {
	t.Helper()
	home := t.TempDir()
	t.Setenv("HOME", home)
	jsonl, err := session.NewDefaultStore()
	if err != nil {
		t.Fatalf("NewDefaultStore: %v", err)
	}
	b := &ChatBridge{sessionStore: jsonl, workingDir: home}
	return b, jsonl
}

func mustSession(t *testing.T, store session.Store) *session.Session {
	t.Helper()
	ses := session.NewSession("test", "test", "test-model")
	ses.Workspace = "test-ws"
	if err := store.Save(ses); err != nil {
		t.Fatalf("save session: %v", err)
	}
	return ses
}

// ─── #269: lock-guarded persist ───────────────────────────────────────

// TestPersistHandlerRefusesWriteUnderForeignLock verifies that the persist
// path refuses to append to a session whose ID differs from the held
// session lock (#269 guard ②).
func TestPersistHandlerRefusesWriteUnderForeignLock(t *testing.T) {
	b, jsonl := newSessionLifecycleTestBridge(t)

	sesA := mustSession(t, b.sessionStore)
	sesB := mustSession(t, b.sessionStore)

	storeDir, err := session.DefaultDir()
	if err != nil {
		t.Fatalf("DefaultDir: %v", err)
	}
	lockB, err := session.TryAcquireSessionLock(storeDir, sesB.ID)
	if err != nil || !lockB.Acquired() {
		t.Fatalf("acquire lock for B: err=%v acquired=%v", err, lockB.Acquired())
	}
	defer lockB.Release()
	b.mu.Lock()
	b.sessionLock = lockB
	b.mu.Unlock()

	// Attempt to persist into A while holding B's lock — must be refused.
	b.appendPersistMessage(jsonl, sesA, provider.Message{Role: "assistant", Content: []provider.ContentBlock{{Type: "text", Text: "must not land"}}})

	reloaded, err := b.sessionStore.Load(sesA.ID)
	if err != nil {
		t.Fatalf("reload A: %v", err)
	}
	if len(reloaded.Messages) != 0 {
		t.Fatalf("session A received %d messages under a foreign lock; want 0 (#269)", len(reloaded.Messages))
	}
}

// TestPersistHandlerAllowsWriteWithoutLock verifies the guard does not
// over-block: with no lock held (creation paths acquire locks best-effort),
// appends proceed.
func TestPersistHandlerAllowsWriteWithoutLock(t *testing.T) {
	b, jsonl := newSessionLifecycleTestBridge(t)
	ses := mustSession(t, b.sessionStore)

	b.appendPersistMessage(jsonl, ses, provider.Message{Role: "assistant", Content: []provider.ContentBlock{{Type: "text", Text: "ok"}}})

	reloaded, err := b.sessionStore.Load(ses.ID)
	if err != nil {
		t.Fatalf("reload: %v", err)
	}
	if len(reloaded.Messages) != 1 {
		t.Fatalf("expected 1 message without lock held, got %d", len(reloaded.Messages))
	}
}

// TestSessionLockMismatchGuard verifies sessionLockMismatchLocked semantics:
// nil lock or matching lock is not a mismatch; a different session's lock is.
func TestSessionLockMismatchGuard(t *testing.T) {
	b, _ := newSessionLifecycleTestBridge(t)
	sesA := &session.Session{ID: "ses-a"}
	sesB := &session.Session{ID: "ses-b"}

	b.mu.Lock()
	if b.sessionLockMismatchLocked(sesA) {
		t.Fatal("nil lock must not be a mismatch")
	}
	if b.sessionLockMismatchLocked(nil) {
		t.Fatal("nil session must not be a mismatch")
	}
	b.mu.Unlock()

	storeDir, _ := session.DefaultDir()
	lockB, err := session.TryAcquireSessionLock(storeDir, sesB.ID)
	if err != nil || !lockB.Acquired() {
		t.Fatalf("acquire B: err=%v acquired=%v", err, lockB.Acquired())
	}
	defer lockB.Release()

	b.mu.Lock()
	b.sessionLock = lockB
	if !b.sessionLockMismatchLocked(sesA) {
		t.Fatal("A vs B lock must be a mismatch")
	}
	if b.sessionLockMismatchLocked(sesB) {
		t.Fatal("B vs B lock must not be a mismatch")
	}
	b.mu.Unlock()
}

// ─── #269: LoadSession InitAgent-failure rollback ─────────────────────

// TestRollbackSessionLoadRestoresPreviousSession verifies that after a failed
// switch, the bridge rolls back to the previous (persisted, non-ephemeral)
// session and re-acquires its lock (#269 guard ①).
func TestRollbackSessionLoadRestoresPreviousSession(t *testing.T) {
	b, _ := newSessionLifecycleTestBridge(t)

	oldSes := mustSession(t, b.sessionStore)
	oldSes.Messages = append(oldSes.Messages, provider.Message{Role: "user", Content: []provider.ContentBlock{{Type: "text", Text: "hi"}}})
	if err := b.sessionStore.Save(oldSes); err != nil {
		t.Fatalf("resave: %v", err)
	}
	b.setSessionState(agentruntime.AdoptSession(oldSes))

	// Snapshot BEFORE the switch attempt — mirrors LoadSession, which takes
	// the snapshot before cleanup/switch, so it captures the old session.
	prev := b.snapshotSessionState()
	if prev.ses != oldSes {
		t.Fatalf("snapshot captured %p, want old session", prev.ses)
	}
	if prev.deletedByCleanup {
		t.Fatal("old session has messages; snapshot must not predict deletion")
	}

	failedSes := mustSession(t, b.sessionStore)
	b.setSessionState(agentruntime.AdoptSession(failedSes)) // the switch that will "fail"

	// Simulate the failure branch: lock already released by the branch above
	// this call in LoadSession; rollback must restore oldSes.
	b.ResetAgent()
	b.rollbackSessionLoad(failedSes, prev)

	if b.CurrentSessionID() != oldSes.ID {
		t.Fatalf("after rollback current=%q, want old session %q (#269)", b.CurrentSessionID(), oldSes.ID)
	}
	b.mu.Lock()
	lock := b.sessionLock
	b.mu.Unlock()
	if lock == nil || lock.SessionID() != oldSes.ID {
		t.Fatalf("rollback did not re-acquire old session lock: %+v", lock)
	}
	if b.sessionEphemeral {
		t.Fatal("rollback must not mark a persisted session ephemeral")
	}
}

// TestRollbackSessionLoadClearsWhenPreviousDeleted verifies the ephemeral
// previous-session case: rollback cannot resurrect a deleted session, so the
// current session must be cleared instead (#269).
func TestRollbackSessionLoadClearsWhenPreviousDeleted(t *testing.T) {
	b, _ := newSessionLifecycleTestBridge(t)

	ephemeral := mustSession(t, b.sessionStore) // no messages → deleted by cleanup
	failedSes := mustSession(t, b.sessionStore)
	b.setSessionState(agentruntime.AdoptSession(failedSes))

	prev := prevSessionState{ses: ephemeral, ephemeral: true, deletedByCleanup: true}
	_ = b.sessionStore.Delete(ephemeral.ID) // the cleanup that ran before the switch

	b.rollbackSessionLoad(failedSes, prev)

	if b.CurrentSessionID() != "" {
		t.Fatalf("rollback left current=%q after deleted previous session; want cleared (#269)", b.CurrentSessionID())
	}
	b.mu.Lock()
	eph := b.sessionEphemeral
	b.mu.Unlock()
	if eph {
		t.Fatal("ephemeral flag must be cleared when rolling back to nothing")
	}
}

// TestRollbackSessionLoadNoopWhenCurrentChanged verifies rollback is a no-op
// if another path already switched the session (double-switch protection).
func TestRollbackSessionLoadNoopWhenCurrentChanged(t *testing.T) {
	b, _ := newSessionLifecycleTestBridge(t)
	oldSes := mustSession(t, b.sessionStore)
	otherSes := mustSession(t, b.sessionStore)
	b.setSessionState(agentruntime.AdoptSession(otherSes))

	prev := prevSessionState{ses: oldSes}
	failedSes := &session.Session{ID: "failed"} // not current anymore
	b.rollbackSessionLoad(failedSes, prev)

	if b.CurrentSessionID() != otherSes.ID {
		t.Fatalf("rollback clobbered concurrent switch: current=%q, want %q", b.CurrentSessionID(), otherSes.ID)
	}
}

// ─── #270: run-start snapshot semantics ───────────────────────────────

// TestPersistRunMessagesUsesRunSnapshot verifies persistRunMessages appends
// to the run-start snapshot, not the write-time current session (#270).
func TestPersistRunMessagesUsesRunSnapshot(t *testing.T) {
	b, _ := newSessionLifecycleTestBridge(t)

	runSes := mustSession(t, b.sessionStore)
	curSes := mustSession(t, b.sessionStore)

	ag := agent.NewAgent(nil, tool.NewRegistry(), "sys", 1)
	ag.AddMessage(provider.Message{Role: "user", Content: []provider.ContentBlock{{Type: "text", Text: "q"}}})
	ag.AddMessage(provider.Message{Role: "assistant", Content: []provider.ContentBlock{{Type: "text", Text: "a"}}})

	b.mu.Lock()
	b.runSes = runSes     // snapshot taken at run start
	b.currentSes = curSes // session switched mid-run
	b.agent = ag
	b.mu.Unlock()

	// Expected delta: everything the agent added since run start (system
	// message included), with a leading user message stripped.
	expected := len(ag.AddedSinceRunStart())
	if expected > 0 && ag.AddedSinceRunStart()[0].Role == "user" {
		expected--
	}
	runBefore, curBefore := len(runSes.Messages), len(curSes.Messages)

	b.persistRunMessages()

	if got := len(runSes.Messages) - runBefore; got != expected {
		t.Fatalf("run session must receive the run's %d messages, got %d (#270)", expected, got)
	}
	if len(curSes.Messages) != curBefore {
		t.Fatalf("current session must not receive run messages, got %d extra (#270)", len(curSes.Messages)-curBefore)
	}
}

// ─── #279: conditional ephemeral cleanup ──────────────────────────────

// TestCleanupEphemeralFinalizeKeepsConcurrentEphemeral verifies the #279
// race: while lock.Release() was blocked outside b.mu, a concurrent
// EnsureSession created a new ephemeral session (installed a new lock and
// set sessionEphemeral=true). The waking cleanup must not wipe that flag.
func TestCleanupEphemeralFinalizeKeepsConcurrentEphemeral(t *testing.T) {
	b, _ := newSessionLifecycleTestBridge(t)

	oldSes := mustSession(t, b.sessionStore)
	oldLock, err := session.TryAcquireSessionLock(sessionStoreDir(t), oldSes.ID)
	if err != nil || !oldLock.Acquired() {
		t.Fatalf("old lock: err=%v acquired=%v", err, oldLock.Acquired())
	}
	b.mu.Lock()
	b.currentSes = oldSes
	b.sessionLock = oldLock
	b.mu.Unlock()

	// Simulate the release happening (outside b.mu in production) and, in the
	// window, EnsureSession installing a NEW ephemeral session + lock.
	oldLock.Release()
	newSes := mustSession(t, b.sessionStore)
	newLock, err := session.TryAcquireSessionLock(sessionStoreDir(t), newSes.ID)
	if err != nil || !newLock.Acquired() {
		t.Fatalf("new lock: err=%v acquired=%v", err, newLock.Acquired())
	}
	defer newLock.Release()
	b.mu.Lock()
	b.currentSes = newSes
	b.sessionLock = newLock
	b.sessionEphemeral = true // the concurrent creator's flag
	b.mu.Unlock()

	// The cleanup's final critical section wakes up — must keep B's state.
	b.cleanupEphemeralFinalize(oldSes, oldLock)

	b.mu.Lock()
	defer b.mu.Unlock()
	if b.sessionLock != newLock {
		t.Fatal("cleanup clobbered the concurrent session's lock (#279)")
	}
	if !b.sessionEphemeral {
		t.Fatal("cleanup wiped the concurrent session's ephemeral flag — its empty JSONL would be orphaned (#279)")
	}
	if b.currentSes != newSes {
		t.Fatal("cleanup clobbered the concurrent session reference (#279)")
	}
}

// TestCleanupEphemeralFinalizeClearsOwnState verifies the normal path: the
// lock is still ours, so both the lock and the ephemeral flag are cleared.
func TestCleanupEphemeralFinalizeClearsOwnState(t *testing.T) {
	b, _ := newSessionLifecycleTestBridge(t)
	ses := mustSession(t, b.sessionStore)
	lock, err := session.TryAcquireSessionLock(sessionStoreDir(t), ses.ID)
	if err != nil || !lock.Acquired() {
		t.Fatalf("lock: err=%v acquired=%v", err, lock.Acquired())
	}
	defer lock.Release()
	b.mu.Lock()
	b.currentSes = ses
	b.sessionLock = lock
	b.sessionEphemeral = true
	b.mu.Unlock()

	b.cleanupEphemeralFinalize(ses, lock)

	b.mu.Lock()
	defer b.mu.Unlock()
	if b.sessionLock != nil {
		t.Fatal("own lock must be cleared")
	}
	if b.sessionEphemeral {
		t.Fatal("own ephemeral flag must be cleared")
	}
}

// TestSnapshotSessionStatePredictsEphemeralDeletion verifies the snapshot's
// deletedByCleanup prediction matches DeleteSessionIfEmpty's predicate.
func TestSnapshotSessionStatePredictsEphemeralDeletion(t *testing.T) {
	b, _ := newSessionLifecycleTestBridge(t)

	// Ephemeral + no messages → will be deleted by cleanup.
	eph := mustSession(t, b.sessionStore)
	b.mu.Lock()
	b.currentSes = eph
	b.sessionEphemeral = true
	b.mu.Unlock()
	if s := b.snapshotSessionState(); !s.deletedByCleanup {
		t.Fatal("empty ephemeral session must snapshot as deletable")
	}

	// Ephemeral + messages → kept.
	eph.Messages = append(eph.Messages, provider.Message{Role: "user", Content: []provider.ContentBlock{{Type: "text", Text: "x"}}})
	if s := b.snapshotSessionState(); s.deletedByCleanup {
		t.Fatal("non-empty ephemeral session must snapshot as kept")
	}

	// Not ephemeral → kept.
	b.mu.Lock()
	b.sessionEphemeral = false
	b.mu.Unlock()
	if s := b.snapshotSessionState(); s.deletedByCleanup {
		t.Fatal("non-ephemeral session must snapshot as kept")
	}
}

func sessionStoreDir(t *testing.T) string {
	t.Helper()
	dir, err := session.DefaultDir()
	if err != nil {
		t.Fatalf("DefaultDir: %v", err)
	}
	return dir
}

// TestCleanupEphemeralSessionEndToEnd verifies the full cleanup path
// deletes the empty ephemeral session's JSONL from disk (#279 orphan fix).
func TestCleanupEphemeralSessionEndToEnd(t *testing.T) {
	b, _ := newSessionLifecycleTestBridge(t)
	ses := mustSession(t, b.sessionStore)
	lock, err := session.TryAcquireSessionLock(sessionStoreDir(t), ses.ID)
	if err != nil || !lock.Acquired() {
		t.Fatalf("lock: err=%v acquired=%v", err, lock.Acquired())
	}
	defer lock.Release()
	b.mu.Lock()
	b.currentSes = ses
	b.sessionLock = lock
	b.sessionEphemeral = true
	b.mu.Unlock()

	b.cleanupEphemeralSession()

	if _, err := os.Stat(filepath.Join(sessionStoreDir(t), ses.ID+".jsonl")); !os.IsNotExist(err) {
		t.Fatalf("empty ephemeral session JSONL still on disk: %v", err)
	}
	b.mu.Lock()
	lock2 := b.sessionLock
	eph := b.sessionEphemeral
	b.mu.Unlock()
	if lock2 != nil || eph {
		t.Fatalf("cleanup left lock=%v ephemeral=%v; want nil/false", lock2 != nil, eph)
	}
}
