package tool

import (
	"context"
	"encoding/json"
	"strings"
	"testing"
	"time"

	"github.com/topcheer/ggcode/internal/cron"
)

// #5353: one-shot cron with far-future NextFire must warn loudly (year+1 trap),
// recurring jobs are exempt, far=true acknowledges.

func new5353Scheduler(t *testing.T) *cron.Scheduler {
	t.Helper()
	return cron.NewScheduler(func(string, bool) {}, "")
}

func run5353(t *testing.T, tool CronCreateTool, raw string) string {
	t.Helper()
	res, err := tool.Execute(context.Background(), json.RawMessage(raw))
	if err != nil {
		t.Fatalf("execute: %v", err)
	}
	return res.Content
}

// T1: the trap shape — one-shot whose next match is >90d out warns.
// Leap-day one-shot is deterministically >90d out except in a narrow window
// inside a leap February, so the assertion is conditioned on actual distance.
func Test5353_OneShotFarFutureWarns(t *testing.T) {
	tool := CronCreateTool{Scheduler: new5353Scheduler(t)}
	out := run5353(t, tool, `{"cron":"0 0 29 2 *","prompt":"p","one_shot":true}`)
	days := mustNextFireDays(t, out)
	if days > 90 && !strings.Contains(out, "WARNING") {
		t.Fatalf("one-shot with NextFire %.0f days out must warn, got: %s", days, out)
	}
	if days > 90 && !strings.Contains(out, "far=true") {
		t.Fatalf("warning must point at far=true acknowledgment, got: %s", out)
	}
}

// T2: recurring jobs are exempt even when >90d out (annual/leap-day schedules).
func Test5353_RecurringExempt(t *testing.T) {
	tool := CronCreateTool{Scheduler: new5353Scheduler(t)}
	out := run5353(t, tool, `{"cron":"0 0 29 2 *","prompt":"p"}`)
	if strings.Contains(out, "WARNING") {
		t.Fatalf("recurring job must not warn even if NextFire >90d, got: %s", out)
	}
}

// T3: far=true suppresses the warning for the same trap shape.
func Test5353_FarAcknowledgeSilent(t *testing.T) {
	tool := CronCreateTool{Scheduler: new5353Scheduler(t)}
	out := run5353(t, tool, `{"cron":"0 0 29 2 *","prompt":"p","one_shot":true,"far":true}`)
	if strings.Contains(out, "WARNING") {
		t.Fatalf("far=true must suppress the far-future warning, got: %s", out)
	}
}

// T4: boundary — a one-shot inside the 90d window must stay silent.
func Test5353_WithinWindowSilent(t *testing.T) {
	tool := CronCreateTool{Scheduler: new5353Scheduler(t)}
	out := run5353(t, tool, `{"cron":"*/5 * * * *","prompt":"p","one_shot":true}`)
	if strings.Contains(out, "WARNING") {
		t.Fatalf("one-shot within 90d window must not warn, got: %s", out)
	}
	if !strings.Contains(out, "one-shot: fires once") {
		t.Fatalf("one-shot suffix must remain, got: %s", out)
	}
}

func mustNextFireDays(t *testing.T, out string) float64 {
	t.Helper()
	var job struct {
		NextFire time.Time `json:"NextFire"`
	}
	// strip trailing suffix lines before decoding
	line := out
	if i := strings.Index(line, "\n"); i >= 0 {
		line = line[:i]
	}
	if err := json.Unmarshal([]byte(line), &job); err != nil {
		t.Fatalf("decode job: %v (%s)", err, out)
	}
	return time.Until(job.NextFire).Hours() / 24
}
