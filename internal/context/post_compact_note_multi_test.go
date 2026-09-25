package context

import (
	"context"
	"strings"
	"testing"

	"github.com/topcheer/ggcode/internal/provider"
)

// Tests for AddPostCompactNoteProvider: multiple providers concatenate into
// the durable post-compaction note without clobbering Set-registered ones
// (task-board rehydration + agent hook-deny ledger coexistence, r68).

func buildThreeMessageContext(cm *Manager) {
	sys := provider.Message{Role: "system"}
	sys.Content = []provider.ContentBlock{{Type: "text", Text: "System prompt."}}
	u := provider.Message{Role: "user"}
	u.Content = []provider.ContentBlock{{Type: "text", Text: "First message."}}
	a := provider.Message{Role: "assistant"}
	a.Content = []provider.ContentBlock{{Type: "text", Text: "First response."}}
	cm.Add(sys)
	cm.Add(u)
	cm.Add(a)
}

func TestAddPostCompactNoteProviderConcatenates(t *testing.T) {
	cm := NewManager(10000)
	buildThreeMessageContext(cm)
	cm.SetPostCompactNoteProvider(func() string { return "board v1" })
	cm.AddPostCompactNoteProvider(func() string { return "Hook policy state: Bash x2" })

	if err := cm.Summarize(context.Background(), &mockProvider{}); err != nil {
		t.Fatalf("Summarize failed: %v", err)
	}

	found := 0
	for _, m := range cm.Messages() {
		if m.Role != "system" {
			continue
		}
		for _, c := range m.Content {
			if strings.Contains(c.Text, "board v1") && strings.Contains(c.Text, "Hook policy state: Bash x2") {
				found++
			}
		}
	}
	if found != 1 {
		t.Fatalf("expected exactly 1 combined note, got %d", found)
	}
}

func TestAddPostCompactNoteProviderEmptyPartsSkipped(t *testing.T) {
	cm := NewManager(10000)
	buildThreeMessageContext(cm)
	cm.SetPostCompactNoteProvider(func() string { return "" })
	cm.AddPostCompactNoteProvider(func() string { return "" })
	cm.AddPostCompactNoteProvider(func() string { return "only note" })

	if err := cm.Summarize(context.Background(), &mockProvider{}); err != nil {
		t.Fatalf("Summarize failed: %v", err)
	}
	if n := countSystemMsg("only note", cm.Messages()); n != 1 {
		t.Fatalf("expected 1 note, got %d", n)
	}
	if n := countSystemMsg(postCompactNoteMarker, cm.Messages()); n != 1 {
		t.Fatalf("expected exactly 1 marker-bearing note, got %d", n)
	}
}

func TestSetPostCompactNoteProviderReplacesAddStack(t *testing.T) {
	cm := NewManager(10000)
	buildThreeMessageContext(cm)
	cm.SetPostCompactNoteProvider(func() string { return "old" })
	cm.AddPostCompactNoteProvider(func() string { return "extra" })
	// Set semantics preserved: replaces the whole stack.
	cm.SetPostCompactNoteProvider(func() string { return "fresh" })

	if err := cm.Summarize(context.Background(), &mockProvider{}); err != nil {
		t.Fatalf("Summarize failed: %v", err)
	}
	if n := countSystemMsg("fresh", cm.Messages()); n != 1 {
		t.Fatalf("expected 1 'fresh' note, got %d", n)
	}
	for _, forbidden := range []string{"old", "extra"} {
		if n := countSystemMsg(forbidden, cm.Messages()); n != 0 {
			t.Fatalf("Set should have cleared %q note, found %d", forbidden, n)
		}
	}
}
