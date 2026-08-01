package agent

import (
	"path/filepath"
	"strings"
	"testing"

	"github.com/topcheer/ggcode/internal/checkpoint"
	ctxpkg "github.com/topcheer/ggcode/internal/context"
)

func TestMaybeInjectCorrectionFeedback(t *testing.T) {
	dir := t.TempDir()
	fp := filepath.Join(dir, "test.go")

	// Set up checkpoint manager with one correction.
	cpMgr := checkpoint.NewManager(50)
	cpMgr.StartRun("run-1")
	cpMgr.Save(fp, "old content", "new content", "edit_file")

	// Trigger an undo to create the correction.
	if _, err := cpMgr.Undo(); err != nil {
		t.Fatalf("Undo failed: %v", err)
	}

	// Verify correction exists.
	if len(cpMgr.RecentCorrections()) != 1 {
		t.Fatal("expected 1 correction before injection")
	}

	// Create a minimal agent with the checkpoint manager and a real context manager.
	a := &Agent{
		contextManager: ctxpkg.NewManager(200000),
	}
	a.SetCheckpointManager(cpMgr)

	// Capture messages before injection.
	msgsBefore := a.contextManager.Messages()

	// Call the injection.
	a.maybeInjectCorrectionFeedback()

	// A new user message should have been added.
	msgsAfter := a.contextManager.Messages()
	if len(msgsAfter) != len(msgsBefore)+1 {
		t.Fatalf("expected %d messages after injection, got %d", len(msgsBefore)+1, len(msgsAfter))
	}

	// The injected message should mention the reverted file and guidance.
	injected := msgsAfter[len(msgsAfter)-1]
	if injected.Role != "user" {
		t.Errorf("expected injected message role user, got %s", injected.Role)
	}
	var text string
	for _, b := range injected.Content {
		if b.Type == "text" {
			text += b.Text
		}
	}
	if !strings.Contains(text, "reverted") {
		t.Errorf("expected injected message to mention 'reverted', got: %s", text)
	}
	if !strings.Contains(text, "test.go") {
		t.Errorf("expected injected message to mention the file, got: %s", text)
	}
	if !strings.Contains(text, "Do NOT simply repeat") {
		t.Errorf("expected guidance about not repeating, got: %s", text)
	}

	// Corrections should be cleared (one-shot).
	if c := cpMgr.RecentCorrections(); c != nil {
		t.Errorf("expected corrections cleared after injection, got %v", c)
	}

	// Second call should be a no-op (no corrections left).
	msgsBeforeSecond := a.contextManager.Messages()
	a.maybeInjectCorrectionFeedback()
	msgsAfterSecond := a.contextManager.Messages()
	if len(msgsAfterSecond) != len(msgsBeforeSecond) {
		t.Errorf("expected no new message on second call, got %d new", len(msgsAfterSecond)-len(msgsBeforeSecond))
	}
}

func TestMaybeInjectCorrectionFeedbackNilManager(t *testing.T) {
	a := &Agent{
		contextManager: ctxpkg.NewManager(200000),
	}
	// No checkpoint manager set — should be a safe no-op.
	a.maybeInjectCorrectionFeedback()
}
