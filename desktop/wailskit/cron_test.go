//go:build goolm

package wailskit

import (
	"path/filepath"
	"testing"

	"github.com/topcheer/ggcode/internal/cron"
)

func newTestCronBridge(t *testing.T) *ChatBridge {
	t.Helper()
	b := &ChatBridge{}
	b.cronScheduler = cron.NewScheduler(func(prompt string, queueIfBusy bool) {}, filepath.Join(t.TempDir(), "jobs.json"))
	return b
}

// #4977: the desktop editor is a full-state save channel (frontend always
// sends the checkbox bool). An explicit queueIfBusy=false now DISABLES the
// flag instead of being silently folded into "keep original" - the old
// assertion preserved the #4977 bug. Prompt-only/cron-only updates still
// preserve the untouched field via the scheduler's nil semantics.
func TestUpdateCronJob_FullStateSaveAppliesExplicitFalse(t *testing.T) {
	b := newTestCronBridge(t)

	created, err := b.CreateCronJob("0 3 * * *", "original prompt", true, true)
	if err != nil {
		t.Fatalf("CreateCronJob: %v", err)
	}

	updated, err := b.UpdateCronJob(created.ID, "", "new prompt only", false)
	if err != nil {
		t.Fatalf("UpdateCronJob: %v", err)
	}

	if updated.CronExpr != "0 3 * * *" {
		t.Errorf("cron expr overwritten: got %q, want %q", updated.CronExpr, "0 3 * * *")
	}
	if updated.QueueIfBusy {
		t.Errorf("explicit queueIfBusy=false must disable the flag (#4977): still true")
	}
	if updated.Prompt != "new prompt only" {
		t.Errorf("prompt not updated: got %q", updated.Prompt)
	}
}

func TestUpdateCronJob_TrueEnablesQueueIfBusy(t *testing.T) {
	b := newTestCronBridge(t)

	created, err := b.CreateCronJob("0 4 * * *", "prompt", true, false)
	if err != nil {
		t.Fatalf("CreateCronJob: %v", err)
	}

	updated, err := b.UpdateCronJob(created.ID, "", "prompt", true)
	if err != nil {
		t.Fatalf("UpdateCronJob: %v", err)
	}
	if !updated.QueueIfBusy {
		t.Errorf("expected queueIfBusy=true after explicit true update")
	}
	if updated.Prompt != "prompt" {
		t.Errorf("prompt overwritten: got %q, want %q", updated.Prompt, "prompt")
	}
}

func TestUpdateCronJob_CronOnlyPreservesPrompt(t *testing.T) {
	b := newTestCronBridge(t)

	created, err := b.CreateCronJob("0 5 * * *", "keep me", true, false)
	if err != nil {
		t.Fatalf("CreateCronJob: %v", err)
	}

	updated, err := b.UpdateCronJob(created.ID, "0 6 * * *", "keep me", false)
	if err != nil {
		t.Fatalf("UpdateCronJob: %v", err)
	}
	if updated.CronExpr != "0 6 * * *" {
		t.Errorf("cron not updated: got %q", updated.CronExpr)
	}
	if updated.Prompt != "keep me" {
		t.Errorf("prompt overwritten: got %q, want %q", updated.Prompt, "keep me")
	}
}

func TestCreateCronJobRejectsEmptyPrompt(t *testing.T) {
	b := newTestCronBridge(t)
	for _, prompt := range []string{"", "   "} {
		if _, err := b.CreateCronJob("0 3 * * *", prompt, true, true); err == nil {
			t.Fatalf("empty prompt %q accepted", prompt)
		}
	}
}
