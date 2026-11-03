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

func TestIssue594_SendMessageDataBindsPersist(t *testing.T) {
	b, home := zz594Bridge(t)

	b.mu.Lock()
	b.runGeneration++
	b.activeRunGen = b.runGeneration
	b.runSes = b.currentSes
	b.mu.Unlock()

	if b.agent == nil {

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

func TestIssue594_StalePersistRebound(t *testing.T) {
	b, _ := zz594Bridge(t)

	if err := b.ensureSession(); err != nil {
		t.Fatalf("ensureSession A: %v", err)
	}
	b.mu.Lock()
	a := b.currentSes
	b.runSes = a
	b.mu.Unlock()
	b.setRunPersistSnapshot()

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

func TestIssue594_SaveSessionDeadMethodRemoved(t *testing.T) {
	f, err := os.ReadFile("chat.go")
	if err != nil {
		t.Fatalf("read chat.go: %v", err)
	}
	if strings.Contains(string(f), "func (b *ChatBridge) saveSession()") {
		t.Fatal("dead saveSession method still present")
	}
	_ = context.Background
}
