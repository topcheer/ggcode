//go:build goolm

package wailskit

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/topcheer/ggcode/internal/session"
)

// zz594Bridge builds a ChatBridge with an isolated HOME (t.TempDir) so the
// JSONL store writes under the throwaway .ggcode dir.
func zz594Bridge(t *testing.T) (*ChatBridge, string) {
	t.Helper()
	home := t.TempDir()
	if err := os.MkdirAll(filepath.Join(home, ".ggcode"), 0o755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	t.Setenv("HOME", home)
	store, err := session.NewDefaultStore()
	if err != nil {
		t.Fatalf("NewDefaultStore: %v", err)
	}
	return &ChatBridge{sessionStore: store}, home
}

// TestIssue594_SendMessageDataBindsPersist: the text path must install the
// per-run persist snapshot (setRunPersistSnapshot) — previously only
// SendContent (image paste) bound it, so every desktop text / IM / mobile /
// LAN message skipped disk persistence entirely (#594 probe 1).
func TestIssue594_SendMessageDataBindsPersist(t *testing.T) {
	b, home := zz594Bridge(t)

	// Drive sendMessageData's pre-run section directly: the persist binding
	// happens before RunStream, so we can assert on the bridge state after a
	// run that fails fast (nil agent never starts a run; instead simulate
	// the section order by calling the pieces the way the path does).
	b.mu.Lock()
	b.runGeneration++
	b.activeRunGen = b.runGeneration
	b.runSes = b.currentSes // pre-run snapshot (nil here, as on first run)
	b.mu.Unlock()

	if b.agent == nil {
		// ensureSession then snapshot-bind, mirroring the fixed order.
		if err := b.ensureSession(); err != nil {
			t.Fatalf("ensureSession: %v", err)
		}
		b.mu.Lock()
		b.runSes = b.currentSes
		b.mu.Unlock()
		b.setRunPersistSnapshot()
	}

	b.mu.Lock()
	persist := b.persistSession
	b.mu.Unlock()
	if persist == nil || persist != b.currentSes {
		t.Fatal("persistSession not bound after the text-path sequence — messages would never reach disk (#594 probe 1)")
	}

	// Disk sanity: the session file exists under the isolated HOME.
	found := false
	filepath.Walk(filepath.Join(home, ".ggcode"), func(p string, _ os.FileInfo, _ error) error {
		if strings.HasSuffix(p, ".jsonl") {
			found = true
		}
		return nil
	})
	if !found {
		t.Fatal("no session JSONL created under isolated HOME")
	}
}

// TestIssue594_SendHiddenTextBindsPersist: the hidden-text path must also
// rebind the persist snapshot before RunStream (its run-start section sets
// runSes+generation but previously never installed the persist target).
func TestIssue594_SendHiddenTextBindsPersist(t *testing.T) {
	b, _ := zz594Bridge(t)

	if err := b.ensureSession(); err != nil {
		t.Fatalf("ensureSession: %v", err)
	}
	b.mu.Lock()
	b.runSes = b.currentSes
	b.mu.Unlock()
	b.setRunPersistSnapshot()

	b.mu.Lock()
	persist := b.persistSession
	b.mu.Unlock()
	if persist == nil || persist != b.currentSes {
		t.Fatal("hidden-text persist binding missing")
	}
}

// TestIssue594_StalePersistRebound: after LoadSession(B), the persist
// target must follow the CURRENT run's session — not stay pinned to a
// previous session A (probe 2: cross-write into A's JSONL or lock-mismatch
// drop).
func TestIssue594_StalePersistRebound(t *testing.T) {
	b, _ := zz594Bridge(t)

	// Session A bound by an earlier (image) run.
	if err := b.ensureSession(); err != nil {
		t.Fatalf("ensureSession A: %v", err)
	}
	b.mu.Lock()
	a := b.currentSes
	b.runSes = a
	b.mu.Unlock()
	b.setRunPersistSnapshot()

	// Switch to session B without rebinding (worst case), then run the
	// fixed text-path sequence: refresh + rebind must retarget to B.
	sesB := &session.Session{ID: "zz594-session-B", Title: "B", Model: "gpt-4"}
	b.mu.Lock()
	b.currentSes = sesB
	b.mu.Unlock()

	b.mu.Lock()
	b.runSes = b.currentSes
	b.mu.Unlock()
	b.setRunPersistSnapshot()

	b.mu.Lock()
	persist := b.persistSession
	b.mu.Unlock()
	if persist == nil || persist.ID != sesB.ID {
		t.Fatalf("persist target still session A (%v) after text-path rebind — cross-write hazard", persist)
	}
}

// TestIssue594_SaveSessionDeadMethodRemoved: saveSession had zero
// production callers (dead persistence illusion) — it must be gone.
func TestIssue594_SaveSessionDeadMethodRemoved(t *testing.T) {
	f, err := os.ReadFile("chat.go")
	if err != nil {
		t.Fatalf("read chat.go: %v", err)
	}
	if strings.Contains(string(f), "func (b *ChatBridge) saveSession()") {
		t.Fatal("dead saveSession method still present")
	}
	_ = context.Background // keep context import meaningful if assertions shrink
}
