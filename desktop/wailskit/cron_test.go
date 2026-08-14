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

// TestUpdateCronJob_PromptOnlyPreservesCronAndQueueIfBusy verifies #288:
// a frontend edit that only changes the prompt (empty cron expr, queueIfBusy
// false not represented) must NOT overwrite the stored cron expression or
// queue_if_busy=true.
func TestUpdateCronJob_PromptOnlyPreservesCronAndQueueIfBusy(t *testing.T) {
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
	if !updated.QueueIfBusy {
		t.Errorf("queueIfBusy silently disabled by prompt-only update (#283-style partial-update bug)")
	}
	if updated.Prompt != "new prompt only" {
		t.Errorf("prompt not updated: got %q", updated.Prompt)
	}
}

// TestUpdateCronJob_TrueEnablesQueueIfBusy verifies that submitting
// queueIfBusy=true does enable it (true-updates semantics).
func TestUpdateCronJob_TrueEnablesQueueIfBusy(t *testing.T) {
	b := newTestCronBridge(t)

	created, err := b.CreateCronJob("0 4 * * *", "prompt", true, false)
	if err != nil {
		t.Fatalf("CreateCronJob: %v", err)
	}

	updated, err := b.UpdateCronJob(created.ID, "", "", true)
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

// TestUpdateCronJob_CronOnlyPreservesPrompt verifies empty prompt is treated
// as "unchanged".
func TestUpdateCronJob_CronOnlyPreservesPrompt(t *testing.T) {
	b := newTestCronBridge(t)

	created, err := b.CreateCronJob("0 5 * * *", "keep me", true, false)
	if err != nil {
		t.Fatalf("CreateCronJob: %v", err)
	}

	updated, err := b.UpdateCronJob(created.ID, "0 6 * * *", "", false)
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
