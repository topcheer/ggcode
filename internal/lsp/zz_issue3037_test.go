package lsp

// Regression probes for #3037:
//   - O1: the unsupported-kinds note must travel as a return value - two
//     concurrent parses cannot swallow each other's notes (the old
//     package-global atomic + TakeUnsupportedNote raced between workspaces,
//     regressing #1588-B's misleading bare "no edits returned").
//   - S1: concurrent acquire() calls for different workspaces must not be
//     serialized behind one workspace's startClient (spawn+handshake).
//   - S2: awaitProjectReady must give up after a bounded wait when the
//     project-ready signal never arrives (localized csharp-ls output).

import (
	"context"
	"os"
	"strings"
	"sync"
	"testing"
	"time"
)

func TestIssue3037_O1_ConcurrentParsersKeepTheirOwnNotes(t *testing.T) {
	rawA := []byte(`{"documentChanges":[{"kind":"rename","oldUri":"file:///wsA/old.go","newUri":"file:///wsA/new.go"}]}`)
	rawB := []byte(`{"documentChanges":[{"kind":"create","uri":"file:///wsB/created.go"}]}`)

	var wg sync.WaitGroup
	notes := make([]string, 50)
	for i := 0; i < 50; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			raw := rawA
			if i%2 == 1 {
				raw = rawB
			}
			_, note := parseWorkspaceEdit(raw)
			notes[i] = note
		}(i)
	}
	wg.Wait()
	for i, note := range notes {
		if note == "" {
			t.Fatalf("parse %d lost its note (global-channel race)", i)
		}
		if i%2 == 0 && !strings.Contains(note, "wsA") {
			t.Fatalf("parse %d expected the wsA rename note, got %q", i, note)
		}
		if i%2 == 1 && !strings.Contains(note, "wsB") {
			t.Fatalf("parse %d expected the wsB create note, got %q", i, note)
		}
	}
}

func TestIssue3037_O1_RenameEditsNoteIsPerCall(t *testing.T) {
	// The note is THIS call's own value: a parse producing an unsupported
	// note followed by a clean parse leaves nothing behind for a third
	// consumer (compile-shape is pinned by RenameEdits' signature).
	_, noted := parseWorkspaceEdit([]byte(`{"documentChanges":[{"kind":"rename","oldUri":"file:///a/old.go","newUri":"file:///a/new.go"}]}`))
	if noted == "" {
		t.Fatal("unsupported parse must yield a note")
	}
	clean := []byte(`{"documentChanges":[{"textDocument":{"uri":"file:///a/a.go"},"edits":[{"range":{"start":{"line":0,"character":0},"end":{"line":0,"character":1}},"newText":"x"}]}]}`)
	if edits, note := parseWorkspaceEdit(clean); note != "" || len(edits) != 1 {
		t.Fatalf("clean parse must yield 1 edit and no note, got %d edits note=%q", len(edits), note)
	}
}

func TestIssue3037_S2_AwaitProjectReadyTimesOut(t *testing.T) {
	prev := awaitProjectReadyTimeout
	awaitProjectReadyTimeout = 300 * time.Millisecond
	t.Cleanup(func() { awaitProjectReadyTimeout = prev })

	s := &sessionClient{readySignal: make(chan struct{})} // never signalled
	s.resolved = ResolvedServer{Binary: "csharp-ls"}
	start := time.Now()
	if err := s.awaitProjectReady(context.Background()); err != nil {
		t.Fatalf("timeout fallback must return nil (treat as ready), got %v", err)
	}
	if elapsed := time.Since(start); elapsed > 2*time.Second {
		t.Fatalf("bounded wait took %s; fallback did not fire", elapsed)
	}
}

func TestIssue3037_S2_AwaitProjectReadyReadySignalStillWins(t *testing.T) {
	s := &sessionClient{readySignal: make(chan struct{})}
	s.resolved = ResolvedServer{Binary: "csharp-ls"}
	close(s.readySignal)
	if err := s.awaitProjectReady(context.Background()); err != nil {
		t.Fatalf("ready signal path must return nil, got %v", err)
	}
	// Non-retry servers skip the wait entirely.
	plain := &sessionClient{readySignal: make(chan struct{})}
	plain.resolved = ResolvedServer{Binary: "gopls"}
	if err := plain.awaitProjectReady(context.Background()); err != nil {
		t.Fatalf("gopls-style session must skip waiting, got %v", err)
	}
}

func TestIssue3037_S1_AcquireDoesNotHoldManagerLockAcrossStartClient(t *testing.T) {
	// Two different workspaces acquiring concurrently must overlap in their
	// slow-start phases; the old defer-unlock serialized them behind one
	// workspace's spawn+handshake. A stub "server" that sleeps ~1s makes
	// serialization measurable (>= 2x) vs overlap (~1x).
	dir := t.TempDir()
	stub := dir + "/slow-server"
	if err := os.WriteFile(stub, []byte("#!/bin/sh\nsleep 1\n"), 0o755); err != nil {
		t.Skipf("cannot create stub server: %v", err)
	}

	m := &sessionManager{sessions: make(map[string]*sessionClient), stopCh: make(chan struct{})}
	defer m.shutdownAll()

	start := time.Now()
	var wg sync.WaitGroup
	for i := 0; i < 2; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			s, err := m.acquire(context.Background(), dir+"/ws"+string(rune('0'+i)), ResolvedServer{Binary: stub})
			if err == nil && s != nil {
				s.touch()
			}
		}(i)
	}
	wg.Wait()
	elapsed := time.Since(start)
	if elapsed >= 1900*time.Millisecond {
		t.Fatalf("concurrent cold starts serialized: took %s (lock held across startClient)", elapsed)
	}
}
