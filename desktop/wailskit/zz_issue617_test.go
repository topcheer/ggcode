//go:build goolm

package wailskit

import (
	"strings"
	"testing"
)

func TestIssue617_EmptyPromptRejected(t *testing.T) {
	b := newTestCronBridge(t)

	created, err := b.CreateCronJob("0 3 * * *", "original prompt", true, false)
	if err != nil {
		t.Fatalf("CreateCronJob: %v", err)
	}

	_, err = b.UpdateCronJob(created.ID, "", "", false)
	if err == nil {
		t.Fatal("empty prompt silently treated as unchanged (#617): expected error")
	}
	if !strings.Contains(err.Error(), "prompt") {
		t.Errorf("error should mention prompt: %v", err)
	}

	_, err = b.UpdateCronJob(created.ID, "", "   \t ", false)
	if err == nil {
		t.Fatal("whitespace-only prompt silently accepted (#617): expected error")
	}

	got, err := b.GetCronJob(created.ID)
	if err != nil {
		t.Fatalf("GetCronJob: %v", err)
	}
	if got.Prompt != "original prompt" {
		t.Errorf("stored prompt changed by rejected update: %q", got.Prompt)
	}
}

func TestIssue617_ValidUpdateRoundTrips(t *testing.T) {
	b := newTestCronBridge(t)

	created, err := b.CreateCronJob("0 3 * * *", "old", true, false)
	if err != nil {
		t.Fatalf("CreateCronJob: %v", err)
	}

	updated, err := b.UpdateCronJob(created.ID, "*/30 * * * *", "new prompt", true)
	if err != nil {
		t.Fatalf("UpdateCronJob: %v", err)
	}
	if updated.Prompt != "new prompt" {
		t.Errorf("prompt = %q, want new prompt", updated.Prompt)
	}
	if updated.CronExpr != "*/30 * * * *" {
		t.Errorf("cronExpr = %q, want */30 * * * *", updated.CronExpr)
	}
	if !updated.QueueIfBusy {
		t.Error("queueIfBusy = false, want true")
	}
}
