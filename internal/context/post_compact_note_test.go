package context

import (
	"context"
	"strings"
	"testing"

	"github.com/topcheer/ggcode/internal/provider"
)

// buildFiveMessageContext assembles a minimal 5-message conversation
// (same shape as the pinned-context tests) on cm.
func buildFiveMessageContext(cm *Manager) {
	sys := provider.Message{Role: "system"}
	sys.Content = []provider.ContentBlock{{Type: "text", Text: "System prompt."}}
	firstU := provider.Message{Role: "user"}
	firstU.Content = []provider.ContentBlock{{Type: "text", Text: "First message."}}
	firstA := provider.Message{Role: "assistant"}
	firstA.Content = []provider.ContentBlock{{Type: "text", Text: "First response."}}
	secondU := provider.Message{Role: "user"}
	secondU.Content = []provider.ContentBlock{{Type: "text", Text: "Second message."}}
	secondA := provider.Message{Role: "assistant"}
	secondA.Content = []provider.ContentBlock{{Type: "text", Text: "Second response."}}
	cm.Add(sys)
	cm.Add(firstU)
	cm.Add(firstA)
	cm.Add(secondU)
	cm.Add(secondA)
}

func findSystemMsg(text string, msgs []provider.Message) int {
	for i, msg := range msgs {
		if msg.Role == "system" && len(msg.Content) > 0 && strings.Contains(msg.Content[0].Text, text) {
			return i
		}
	}
	return -1
}

func countSystemMsg(text string, msgs []provider.Message) int {
	n := 0
	for _, msg := range msgs {
		if msg.Role == "system" && len(msg.Content) > 0 && strings.Contains(msg.Content[0].Text, text) {
			n++
		}
	}
	return n
}

// TestPostCompactNoteSurvivesDirectSummarize verifies that the registered
// post-compaction note (task-board rehydration) is re-materialized right
// after the summary on the direct Summarize path (PTL recovery, /compact).
func TestPostCompactNoteSurvivesDirectSummarize(t *testing.T) {
	cm := NewManager(10000)
	buildFiveMessageContext(cm)
	cm.SetPostCompactNoteProvider(func() string {
		return "Task board: 1 pending, 1 in_progress, 0 completed.\n- task-2 [in_progress] Fix bug"
	})

	if err := cm.Summarize(context.Background(), &mockProvider{}); err != nil {
		t.Fatalf("Summarize failed: %v", err)
	}

	msgs := cm.Messages()
	summaryIdx := findSystemMsg("[Previous conversation summary]", msgs)
	noteIdx := findSystemMsg("[Session State Note", msgs)
	if summaryIdx < 0 {
		t.Fatal("expected summary message after Summarize")
	}
	if noteIdx != summaryIdx+1 {
		t.Fatalf("expected note right after summary, got summary=%d note=%d", summaryIdx, noteIdx)
	}
	if !strings.Contains(msgs[noteIdx].Content[0].Text, "task-2 [in_progress] Fix bug") {
		t.Fatalf("expected board content in note, got %q", msgs[noteIdx].Content[0].Text)
	}
}

// TestPostCompactNoteAppliedViaApplyCompactResult verifies the note is also
// re-materialized on the ApplyCompactResult path (the precompact consumer),
// not just the direct Summarize path.
func TestPostCompactNoteAppliedViaApplyCompactResult(t *testing.T) {
	cm := NewManager(10000)
	buildFiveMessageContext(cm)
	cm.SetPostCompactNoteProvider(func() string {
		return "Task board: 2 pending, 0 in_progress, 0 completed."
	})

	live := cm.Messages()
	snapshot := CompactSnapshot{
		Messages:      live,
		OrigLen:       len(live),
		LastMsgID:     live[len(live)-1].ID,
		ContextWindow: 10000,
	}

	summaryMsg := provider.Message{Role: "system"}
	summaryMsg.Content = []provider.ContentBlock{{Type: "text", Text: "[Previous conversation summary]\nSummary of earlier work."}}
	result := CompactResult{
		Messages:   []provider.Message{summaryMsg},
		TokenCount: 100,
		Changed:    true,
	}

	applied, _ := cm.ApplyCompactResult(snapshot, result)
	if !applied {
		t.Fatal("ApplyCompactResult rejected a valid result")
	}

	msgs := cm.Messages()
	summaryIdx := findSystemMsg("[Previous conversation summary]", msgs)
	noteIdx := findSystemMsg("[Session State Note", msgs)
	if summaryIdx < 0 {
		t.Fatal("expected summary message after ApplyCompactResult")
	}
	if noteIdx != summaryIdx+1 {
		t.Fatalf("expected note right after summary on ApplyCompactResult path, got summary=%d note=%d", summaryIdx, noteIdx)
	}
	if !strings.Contains(msgs[noteIdx].Content[0].Text, "2 pending") {
		t.Fatalf("expected board content in note, got %q", msgs[noteIdx].Content[0].Text)
	}
}

// TestPostCompactNoteStaleReplaced verifies a second compaction replaces
// the stale note instead of accumulating copies.
func TestPostCompactNoteStaleReplaced(t *testing.T) {
	cm := NewManager(10000)
	buildFiveMessageContext(cm)
	board := "board v1"
	cm.SetPostCompactNoteProvider(func() string { return board })

	if err := cm.Summarize(context.Background(), &mockProvider{}); err != nil {
		t.Fatalf("first Summarize failed: %v", err)
	}
	board = "board v2"
	if err := cm.Summarize(context.Background(), &mockProvider{}); err != nil {
		t.Fatalf("second Summarize failed: %v", err)
	}

	msgs := cm.Messages()
	if n := countSystemMsg("[Session State Note", msgs); n != 1 {
		t.Fatalf("expected exactly 1 note after two compactions, got %d", n)
	}
	idx := findSystemMsg("board v2", msgs)
	if idx < 0 {
		t.Fatal("expected refreshed note content")
	}
}

// TestPostCompactNoteEmptyProviderOmits verifies a nil provider or an
// empty return injects nothing (and does not leave stale copies).
func TestPostCompactNoteEmptyProviderOmits(t *testing.T) {
	cm := NewManager(10000)
	buildFiveMessageContext(cm)
	// No provider set at all.
	if err := cm.Summarize(context.Background(), &mockProvider{}); err != nil {
		t.Fatalf("Summarize failed: %v", err)
	}
	if n := countSystemMsg("[Session State Note", cm.Messages()); n != 0 {
		t.Fatalf("expected no note without provider, got %d", n)
	}

	// Empty-returning provider after a note existed: stale copy removed.
	board := "temporary board"
	cm.SetPostCompactNoteProvider(func() string { return board })
	if err := cm.Summarize(context.Background(), &mockProvider{}); err != nil {
		t.Fatalf("Summarize failed: %v", err)
	}
	if n := countSystemMsg("temporary board", cm.Messages()); n != 1 {
		t.Fatalf("expected 1 note, got %d", n)
	}
	board = ""
	if err := cm.Summarize(context.Background(), &mockProvider{}); err != nil {
		t.Fatalf("Summarize failed: %v", err)
	}
	if n := countSystemMsg("[Session State Note", cm.Messages()); n != 0 {
		t.Fatalf("expected stale note removed on empty return, got %d", n)
	}
}
