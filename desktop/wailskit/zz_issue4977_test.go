package wailskit

import (
	"strings"
	"testing"
)

// #4977: the desktop cron editor is a full-state save channel. An explicit
// queueIfBusy=false MUST reach the scheduler (turning true back off), instead
// of being folded into nil ("keep original") which made unchecking silently
// fail while the UI showed success.

func newTestBridgeWithScheduler(t *testing.T) (*ChatBridge, string) {
	t.Helper()
	b := newTestCronBridge(t)
	job, err := b.CreateCronJob("5 4 * * *", "do the thing", true, true)
	if err != nil {
		t.Fatalf("seed job: %v", err)
	}
	return b, job.ID
}

func TestUpdateCronJobExplicitFalseTurnsQueueIfBusyOff(t *testing.T) {
	b, id := newTestBridgeWithScheduler(t)

	info, err := b.UpdateCronJob(id, "7 4 * * *", "do the thing v2", false)
	if err != nil {
		t.Fatalf("UpdateCronJob: %v", err)
	}
	if info.QueueIfBusy {
		t.Fatal("explicit queueIfBusy=false must turn QueueIfBusy off (was silently kept true before #4977 fix)")
	}

	got, err := b.GetCronJob(id)
	if err != nil {
		t.Fatalf("GetCronJob: %v", err)
	}
	if got.QueueIfBusy {
		t.Fatal("persisted job still has QueueIfBusy=true: the false update did not stick")
	}
	if got.CronExpr != "7 4 * * *" {
		t.Fatalf("cronExpr update lost: %q", got.CronExpr)
	}
}

func TestUpdateCronJobExplicitTrueStaysTrue(t *testing.T) {
	b, id := newTestBridgeWithScheduler(t)

	info, err := b.UpdateCronJob(id, "", "again", true)
	if err != nil {
		t.Fatalf("UpdateCronJob: %v", err)
	}
	if !info.QueueIfBusy {
		t.Fatal("explicit queueIfBusy=true must keep QueueIfBusy on")
	}
}

func TestUpdateCronJobEmptyPromptStillRejected(t *testing.T) {
	b, id := newTestBridgeWithScheduler(t)
	_, err := b.UpdateCronJob(id, "", "  ", true)
	if err == nil || !strings.Contains(err.Error(), "#617") {
		t.Fatalf("empty-prompt guard must still reject (#617), got: %v", err)
	}
}

func TestUpdateCronJobNextFirePreservedOnExprChange(t *testing.T) {
	b, id := newTestBridgeWithScheduler(t)
	info, err := b.UpdateCronJob(id, "0 5 * * *", "v3", true)
	if err != nil {
		t.Fatalf("UpdateCronJob: %v", err)
	}
	if info.NextFire == "" {
		t.Fatal("NextFire should be rendered after expression change (non-zero schedule)")
	}
}
