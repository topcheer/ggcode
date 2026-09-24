package agent

import (
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/topcheer/ggcode/internal/checkpoint"
	ctxpkg "github.com/topcheer/ggcode/internal/context"
	"github.com/topcheer/ggcode/internal/tool"
)

func TestFenceObserveAppendsWarningOnFencedReExecution(t *testing.T) {
	a := &Agent{}
	f := a.rollbackFence()
	f.Record("run_command", "git push origin main")
	f.Activate(time.Now().Add(-time.Hour))

	args := []byte(`{"command":"git push origin main"}`)
	if _, hit := a.fenceMatchFor("run_command", args); !hit {
		t.Fatal("expected fenced match for re-executed command")
	}

	got := a.fenceObserve("run_command", args, tool.Result{Content: "pushed"}, true)
	if !strings.Contains(got.Content, "[rollback-fence]") {
		t.Errorf("expected rollback-fence warning appended, got: %s", got.Content)
	}

	// Non-matching command: no warning.
	other := []byte(`{"command":"git status"}`)
	if _, hit := a.fenceMatchFor("run_command", other); hit {
		t.Error("unfenced command must not match")
	}
	res2 := a.fenceObserve("run_command", other, tool.Result{Content: "ok"}, false)
	if strings.Contains(res2.Content, "[rollback-fence]") {
		t.Errorf("warning must not be appended without a fence match, got: %s", res2.Content)
	}
}

func TestFenceMatchOnlyForCommandTools(t *testing.T) {
	a := &Agent{}
	f := a.rollbackFence()
	f.Record("run_command", "git push origin main")
	f.Activate(time.Now().Add(-time.Hour))

	// Same command text routed through a non-command tool: never fenced.
	if _, hit := a.fenceMatchFor("web_fetch", []byte(`{"command":"git push origin main"}`)); hit {
		t.Error("non-command tool must never match the fence")
	}
	// Malformed arguments: safe no-match.
	if _, hit := a.fenceMatchFor("run_command", []byte(`{oops`)); hit {
		t.Error("malformed arguments must not match")
	}
}

func TestExecutePathRecordsRunCommandAndFencesOnRestore(t *testing.T) {
	a := &Agent{}
	args := []byte(`{"command":"gh pr create --title x"}`)
	a.fenceObserve("run_command", args, tool.Result{Content: "created"}, false)

	acted := a.ActivateRollbackFence(time.Now().Add(-time.Hour))
	if len(acted) != 1 {
		t.Fatalf("expected 1 fenced action, got %d", len(acted))
	}
	if acted[0].Tool != "run_command" || acted[0].Command != "gh pr create --title x" {
		t.Fatalf("unexpected action %+v", acted[0])
	}
	if _, ok := a.rollbackFence().Match("gh pr create --title x"); !ok {
		t.Fatal("expected fence match after restore")
	}

	// Failed executions are NOT recorded: no confirmed external effect.
	a.fenceObserve("run_command", []byte(`{"command":"npm publish"}`), tool.Result{Content: "failed", IsError: true}, false)
	if acted2 := a.ActivateRollbackFence(time.Now().Add(time.Hour)); len(acted2) != 0 {
		t.Fatalf("failed executions must not be recorded, got %v", acted2)
	}
}

func TestCorrectionFeedbackIncludesFenceAdvisory(t *testing.T) {
	dir := t.TempDir()
	fp := filepath.Join(dir, "test.go")
	cpMgr := checkpoint.NewManager(50)
	cpMgr.StartRun("run-1")
	cpMgr.Save(fp, "old content", "new content", "edit_file")
	if _, err := cpMgr.Undo("user"); err != nil {
		t.Fatalf("Undo failed: %v", err)
	}

	a := &Agent{contextManager: ctxpkg.NewManager(200000)}
	a.SetCheckpointManager(cpMgr)
	a.rollbackFence().Record("run_command", "git push origin main")
	a.ActivateRollbackFence(time.Now().Add(-time.Hour))

	before := len(a.contextManager.Messages())
	a.maybeInjectCorrectionFeedback()
	msgs := a.contextManager.Messages()
	if len(msgs) != before+1 {
		t.Fatalf("expected 1 injected message, got %d", len(msgs)-before)
	}
	var text string
	for _, b := range msgs[len(msgs)-1].Content {
		if b.Type == "text" {
			text += b.Text
		}
	}
	if !strings.Contains(text, "Rollback advisory") {
		t.Errorf("expected fence advisory in injected message, got: %s", text)
	}
	if !strings.Contains(text, "git push origin main") {
		t.Errorf("expected fenced command listed, got: %s", text)
	}
	if !strings.Contains(text, "Do NOT simply repeat") {
		t.Errorf("expected standard correction guidance preserved, got: %s", text)
	}
}

func TestFenceOnlyInjectionWithoutCorrections(t *testing.T) {
	a := &Agent{contextManager: ctxpkg.NewManager(200000)}
	a.rollbackFence().Record("run_command", "gh pr create")
	a.ActivateRollbackFence(time.Now().Add(-time.Hour))

	before := len(a.contextManager.Messages())
	a.maybeInjectCorrectionFeedback()
	msgs := a.contextManager.Messages()
	if len(msgs) != before+1 {
		t.Fatalf("expected fence-only injection, got %d new messages", len(msgs)-before)
	}
	var text string
	for _, b := range msgs[len(msgs)-1].Content {
		if b.Type == "text" {
			text += b.Text
		}
	}
	if !strings.Contains(text, "Rollback advisory") || !strings.Contains(text, "gh pr create") {
		t.Errorf("unexpected fence-only message: %s", text)
	}
}

func TestNoInjectionWhenNothingFencedAndNoCorrections(t *testing.T) {
	a := &Agent{contextManager: ctxpkg.NewManager(200000)}
	before := len(a.contextManager.Messages())
	a.maybeInjectCorrectionFeedback()
	if got := len(a.contextManager.Messages()); got != before {
		t.Fatalf("expected no injection, got %d new messages", got-before)
	}
}
