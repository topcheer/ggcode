package wailskit

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/topcheer/ggcode/internal/session"
)

// Issue #2741: LoadSession's busy guard is one-shot — a run started during
// the disk-IO window (session-lock acquire, store.Load) left b.cancel set
// but the load proceeded to install the new session anyway. LoadSession
// never bumped runGeneration, so the still-draining run's emitIfCurrent
// guard passed and the old session's stream events polluted the new
// session's liveHistory/frontend, while run_done fired against the new
// turn. The fix re-checks busy after the IO window and refuses the load
// (mirroring ClearCurrentSession #550 E1), bumping runGeneration on the
// install path to supersede any residual-window run.

const issue2741Session = `{"type":"meta","session_id":"%s","title":"%s","created_at":"%s","updated_at":"%s"}` + "\n" +
	`{"type":"message","session_id":"%s","timestamp":"%s","message":{"role":"user","content":[{"type":"text","text":"hello"}]}}` + "\n"

func issue2741WriteSession(t *testing.T, dir, id, title string) {
	t.Helper()
	now := time.Now().Format(time.RFC3339Nano)
	body := fmt.Sprintf(issue2741Session, id, title, now, now, id, now)
	if err := os.MkdirAll(dir, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, id+".jsonl"), []byte(body), 0o600); err != nil {
		t.Fatal(err)
	}
}

// blockingLoadStore gates store.Load to hold LoadSession inside the
// disk-IO window while the test injects a run start.
type blockingLoadStore struct {
	session.Store
	entered chan struct{}
	release chan struct{}
}

func (s *blockingLoadStore) Load(id string) (*session.Session, error) {
	s.entered <- struct{}{}
	<-s.release
	return s.Store.Load(id)
}

func issue2741CurrentSessionID(b *ChatBridge) string {
	b.mu.Lock()
	defer b.mu.Unlock()
	if b.currentSes == nil {
		return ""
	}
	return b.currentSes.ID
}

// Run starts mid-IO must abort the load: the previously-selected session
// stays current, the run's cancel stays untouched, the target session's
// lock is released, and no generation bump happens (the in-flight run must
// keep owning the finish path for the session it belongs to).
func TestIssue2741_LoadSessionRefusesWhenRunStartsDuringIO(t *testing.T) {
	tmp := t.TempDir()
	t.Setenv("HOME", tmp) // NewDefaultStore -> $HOME/.ggcode/sessions
	dir := filepath.Join(tmp, ".ggcode", "sessions")
	issue2741WriteSession(t, dir, "sess2741a", "Session A")
	issue2741WriteSession(t, dir, "sess2741b", "Session B")

	bridge, err := NewChatBridge()
	if err != nil {
		t.Fatalf("NewChatBridge: %v", err)
	}
	SetChatBridge(bridge)
	t.Cleanup(func() { SetChatBridge(nil); bridge.Close() })
	if err := bridge.LoadSession("sess2741a"); err != nil {
		t.Fatalf("setup: LoadSession(A): %v", err)
	}

	// Gate the store so LoadSession(B) parks inside the IO window.
	bridge.mu.Lock()
	realStore := bridge.sessionStore
	blk := &blockingLoadStore{
		Store:   realStore,
		entered: make(chan struct{}, 1),
		release: make(chan struct{}),
	}
	bridge.sessionStore = blk
	bridge.mu.Unlock()

	errCh := make(chan error, 1)
	go func() { errCh <- bridge.LoadSession("sess2741b") }()

	select {
	case <-blk.entered:
	case <-time.After(5 * time.Second):
		t.Fatal("LoadSession never reached the store IO window")
	}

	// Simulate a run starting during the window (IM/cron auto-injection):
	// same critical-section shape as hidden-run start (cancel + generation).
	_, runCancel := context.WithCancel(context.Background())
	defer runCancel()
	bridge.mu.Lock()
	bridge.cancel = runCancel
	bridge.cancelled = false
	bridge.runGeneration++
	bridge.activeRunGen = bridge.runGeneration
	genAfterRunStart := bridge.runGeneration
	bridge.mu.Unlock()

	close(blk.release)

	select {
	case err := <-errCh:
		if err == nil || !strings.Contains(err.Error(), "agent is running") {
			t.Fatalf("LoadSession(B) during run = %v, want busy refusal", err)
		}
	case <-time.After(10 * time.Second):
		t.Fatal("LoadSession(B) did not return after the gate opened")
	}

	if got := issue2741CurrentSessionID(bridge); got != "sess2741a" {
		t.Fatalf("load installed despite in-flight run: currentSes=%q, want sess2741a", got)
	}

	bridge.mu.Lock()
	stillBusy := bridge.cancel != nil
	genNow := bridge.runGeneration
	bridge.mu.Unlock()
	if !stillBusy {
		t.Fatal("in-flight run's cancel was disturbed by the refused load")
	}
	if genNow != genAfterRunStart {
		t.Fatalf("refuse path bumped runGeneration: %d -> %d (in-flight run must stay current)", genAfterRunStart, genNow)
	}

	// The target session's lock must have been released, or a retry of
	// LoadSession(B) reports "locked by another instance" (#246 class).
	lock, err := session.TryAcquireSessionLock(dir, "sess2741b")
	if err != nil || lock == nil || !lock.Acquired() {
		t.Fatalf("session B lock still held after refusal: lock=%v err=%v", lock, err)
	}
	lock.Release()
}

// A clean load (no in-flight run) must bump runGeneration exactly once so
// any run that raced into the residual window between the busy re-check
// and setSessionState is superseded and self-drops (#489 emitIfCurrent).
func TestIssue2741_LoadSessionBumpsGenerationOnInstall(t *testing.T) {
	tmp := t.TempDir()
	t.Setenv("HOME", tmp)
	dir := filepath.Join(tmp, ".ggcode", "sessions")
	issue2741WriteSession(t, dir, "sess2741c", "Session C")

	bridge, err := NewChatBridge()
	if err != nil {
		t.Fatalf("NewChatBridge: %v", err)
	}
	SetChatBridge(bridge)
	t.Cleanup(func() { SetChatBridge(nil); bridge.Close() })

	bridge.mu.Lock()
	genBefore := bridge.runGeneration
	bridge.mu.Unlock()

	if err := bridge.LoadSession("sess2741c"); err != nil {
		t.Fatalf("LoadSession(C): %v", err)
	}

	bridge.mu.Lock()
	genAfter := bridge.runGeneration
	bridge.mu.Unlock()
	if genAfter != genBefore+1 {
		t.Fatalf("install path runGeneration = %d, want exactly before+1 (%d)", genAfter, genBefore+1)
	}
	if got := issue2741CurrentSessionID(bridge); got != "sess2741c" {
		t.Fatalf("currentSes = %q, want sess2741c", got)
	}
}
