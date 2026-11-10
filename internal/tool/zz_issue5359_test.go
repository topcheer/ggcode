package tool

import (
	"context"
	"encoding/json"
	"strings"
	"testing"

	"github.com/topcheer/ggcode/internal/cron"
)

// #5359: far_horizon_ack was documented (schema + docs) but orphaned - the
// guard only read args.Far. Both names must now acknowledge the horizon.
func new5359Tool() CronCreateTool {
	return CronCreateTool{Scheduler: cron.NewScheduler(func(string, bool) {}, "")}
}

func run5359(t *testing.T, raw string) string {
	t.Helper()
	res, err := new5359Tool().Execute(context.Background(), json.RawMessage(raw))
	if err != nil {
		t.Fatalf("execute: %v", err)
	}
	return res.Content
}

func Test5359_FarHorizonAckWired(t *testing.T) {
	// ~150 days out (April 10 next year from a later-Nov creation): beyond
	// the 90-day horizon regardless of today's exact date.
	farFuture := `{"cron":"0 9 10 4 *","prompt":"p","one_shot":true,"description":"t","far_horizon_ack":true}`
	out := run5359(t, farFuture)
	if strings.Contains(out, "rejected") {
		t.Fatalf("far_horizon_ack=true must acknowledge the horizon, got: %s", out)
	}
	if !strings.Contains(out, "one-shot: fires once") {
		t.Fatalf("job must be created, got: %s", out)
	}
}

func Test5359_FarLegacyStillWorks(t *testing.T) {
	out := run5359(t, `{"cron":"0 9 10 4 *","prompt":"p","one_shot":true,"description":"t","far":true}`)
	if strings.Contains(out, "rejected") {
		t.Fatalf("far=true (legacy) must still acknowledge, got: %s", out)
	}
}

func Test5359_NoAckRejected(t *testing.T) {
	out := run5359(t, `{"cron":"0 9 10 4 *","prompt":"p","one_shot":true,"description":"t"}`)
	if !strings.Contains(out, "rejected") {
		t.Fatalf("far-future one-shot without any ack must be rejected, got: %s", out)
	}
	if !strings.Contains(out, `"far": true`) {
		t.Fatalf("rejection must point at the far parameter, got: %s", out)
	}
}
