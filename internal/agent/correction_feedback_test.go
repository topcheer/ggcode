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

	cpMgr := checkpoint.NewManager(50)
	cpMgr.StartRun("run-1")
	cpMgr.Save(fp, "old content", "new content", "edit_file")

	if _, err := cpMgr.Undo("user"); err != nil {
		t.Fatalf("Undo failed: %v", err)
	}

	if len(cpMgr.RecentCorrections()) != 1 {
		t.Fatal("expected 1 correction before injection")
	}

	a := &Agent{
		contextManager: ctxpkg.NewManager(200000),
	}
	a.SetCheckpointManager(cpMgr)

	msgsBefore := a.contextManager.Messages()

	a.maybeInjectCorrectionFeedback()

	msgsAfter := a.contextManager.Messages()
	if len(msgsAfter) != len(msgsBefore)+1 {
		t.Fatalf("expected %d messages after injection, got %d", len(msgsBefore)+1, len(msgsAfter))
	}

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

	if c := cpMgr.RecentCorrections(); c != nil {
		t.Errorf("expected corrections cleared after injection, got %v", c)
	}

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

	a.maybeInjectCorrectionFeedback()
}
