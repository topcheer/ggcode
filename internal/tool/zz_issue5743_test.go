package tool

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/topcheer/ggcode/internal/cron"
)

// #5743: cron_create one_shot scheduled for "later today" whose target
// time had already passed silently fired +24h (next calendar day): the
// far=true guard only covered >90d horizons, and the create result never
// echoed the computed next fire, so the day-long deferral was invisible at
// build time (techwriter R4-5: two evening builds at 22:40/22:52 intended
// for the same evening, each silently deferred to the next day, withdrawn
// and rebuilt twice).
//
// Fix contract:
//  1. every one-shot create result echoes "next fire: <local time>
//     (<delta> from now)" so schedule mismatches surface at build time;
//  2. a same-day overshoot (time-only expression, today's candidates all
//     passed, next match = tomorrow) is rejected unless "tomorrow": true,
//     mirroring the far= acknowledgement pattern; the accepted create
//     carries a loud WARNING, and far=true does not acknowledge it.
//
// "0 0 * * *" is the deterministic wall-clock-safe overshoot fixture:
// today 00:00 has already passed at any run time, so its next fire is
// always tomorrow 00:00 (the current minute is excluded, matching
// cron.NextTime's search start).

func TestIssue5743SameDayOvershootRejectedWithoutAck(t *testing.T) {
	s := cron.NewScheduler(func(string, bool) {}, "")
	defer s.Shutdown()
	tool := CronCreateTool{Scheduler: s}

	result, err := tool.Execute(context.Background(), json.RawMessage(
		`{"cron": "0 0 * * *", "prompt": "tonight follow-up", "one_shot": true}`))
	if err != nil {
		t.Fatal(err)
	}
	if !result.IsError {
		t.Fatalf("same-day overshoot one-shot must be rejected without tomorrow=true, got success: %s", result.Content)
	}
	for _, want := range []string{
		"already passed today",
		"roll to tomorrow",
		`"tomorrow": true`, // ack guidance
		"far=true does not acknowledge",
	} {
		if !strings.Contains(result.Content, want) {
			t.Errorf("rejection should mention %q, got: %s", want, result.Content)
		}
	}
	if jobs := s.List(); len(jobs) != 0 {
		t.Errorf("rejected rollover one-shot must not be created, found %d job(s)", len(jobs))
	}
}

func TestIssue5743RejectionSurfacesNextFireDelta(t *testing.T) {
	s := cron.NewScheduler(func(string, bool) {}, "")
	defer s.Shutdown()
	tool := CronCreateTool{Scheduler: s}

	// Ask 1 applies at reject time too: the rejection states tomorrow's
	// concrete fire time and the delta from now, so the caller sees the
	// exact deferral being declined.
	result, err := tool.Execute(context.Background(), json.RawMessage(
		`{"cron": "0 0 * * *", "prompt": "tonight follow-up", "one_shot": true}`))
	if err != nil {
		t.Fatal(err)
	}
	if !result.IsError {
		t.Fatalf("expected rejection, got success: %s", result.Content)
	}
	tomorrow := time.Now().AddDate(0, 0, 1).Format("2006-01-02")
	if !strings.Contains(result.Content, tomorrow) {
		t.Errorf("rejection should state tomorrow's fire date %s, got: %s", tomorrow, result.Content)
	}
	if !strings.Contains(result.Content, "from now") {
		t.Errorf("rejection should state the delta from now, got: %s", result.Content)
	}
}

func TestIssue5743TomorrowAckCreatesWithLoudWarning(t *testing.T) {
	s := cron.NewScheduler(func(string, bool) {}, "")
	defer s.Shutdown()
	tool := CronCreateTool{Scheduler: s}

	result, err := tool.Execute(context.Background(), json.RawMessage(
		`{"cron": "0 0 * * *", "prompt": "next-midnight follow-up", "one_shot": true, "tomorrow": true}`))
	if err != nil {
		t.Fatal(err)
	}
	if result.IsError {
		t.Fatalf("tomorrow-acknowledged rollover must create, got rejection: %s", result.Content)
	}
	for _, want := range []string{
		"WARNING: same-day overshoot accepted via tomorrow=true",
		"already passed today",
	} {
		if !strings.Contains(result.Content, want) {
			t.Errorf("acknowledged create should carry %q, got: %s", want, result.Content)
		}
	}
	// Ask 1: the next-fire echo must state tomorrow's date at the target
	// time so the accepted deferral stays visible.
	tomorrow := time.Now().AddDate(0, 0, 1).Format("2006-01-02")
	if !strings.Contains(result.Content, "next fire: "+tomorrow+" 00:00") {
		t.Errorf("result should echo 'next fire: %s 00:00', got: %s", tomorrow, result.Content)
	}
	if !strings.Contains(result.Content, "from now") {
		t.Errorf("next-fire echo should include the delta from now, got: %s", result.Content)
	}
	// The suffix lines must stay behind the JSON job object so
	// first-line unmarshalling keeps working (#5673 contract).
	var job struct {
		Recurring bool `json:"recurring"`
	}
	if err := json.Unmarshal([]byte(strings.SplitN(result.Content, "\n", 2)[0]), &job); err != nil {
		t.Fatalf("job JSON parse (suffix must not break the JSON prefix): %v", err)
	}
	if job.Recurring {
		t.Error("one_shot=true must create a non-recurring job")
	}
	if jobs := s.List(); len(jobs) != 1 {
		t.Fatalf("expected 1 created job, got %d", len(jobs))
	}
}

func TestIssue5743FarDoesNotAcknowledgeRollover(t *testing.T) {
	s := cron.NewScheduler(func(string, bool) {}, "")
	defer s.Shutdown()
	tool := CronCreateTool{Scheduler: s}

	// A +24h rollover is never a far-horizon question; far=true must not
	// double as its acknowledgement (same precedence rule as #5594).
	result, err := tool.Execute(context.Background(), json.RawMessage(
		`{"cron": "0 0 * * *", "prompt": "tonight follow-up", "one_shot": true, "far": true}`))
	if err != nil {
		t.Fatal(err)
	}
	if !result.IsError {
		t.Fatalf("far=true must not acknowledge a same-day overshoot, got success: %s", result.Content)
	}
	if !strings.Contains(result.Content, "far=true does not acknowledge a same-day overshoot") {
		t.Errorf("rejection should close the far bypass explicitly, got: %s", result.Content)
	}
	if jobs := s.List(); len(jobs) != 0 {
		t.Errorf("rejected rollover must not be created, found %d job(s)", len(jobs))
	}
}

func TestIssue5743DayRolloverDetector(t *testing.T) {
	// The R4-5 incident shape: built 22:52 for 22:40 "same evening".
	now := time.Date(2026, 11, 17, 22, 52, 10, 0, time.Local)
	evening := time.Date(2026, 11, 17, 21, 5, 0, 0, time.Local)
	cases := []struct {
		name   string
		expr   string
		now    time.Time
		rolled bool
		target string // today's latest passed slot (15:04), when rolled
		next   string // computed next fire (2006-01-02 15:04), when rolled
	}{
		{"same-evening target just passed", "40 22 * * *", now, true, "22:40", "2026-11-18 22:40"},
		{"midnight target always passed", "0 0 * * *", now, true, "00:00", "2026-11-18 00:00"},
		{"current-minute target counts as passed", "52 22 * * *", now, true, "22:52", "2026-11-18 22:52"},
		{"later-today slot still reachable", "53 22 * * *", now, false, "", ""},
		{"tonight slot still reachable", "30 23 * * *", now, false, "", ""},
		{"stepped minute always has a near match", "*/5 * * * *", now, false, "", ""},
		{"wildcard hour always has a near match", "5 * * * *", now, false, "", ""},
		{"weekday-restricted out of scope", "40 22 * * 1", now, false, "", ""},
		{"dated form is #5594 scope", "40 22 17 11 *", now, false, "", ""},
		{"stepped day-of-month out of scope", "40 22 */2 * *", now, false, "", ""},
		{"range exhaustion rolls to first slot", "0,30 8-20 * * *", evening, true, "20:30", "2026-11-18 08:00"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			target, next, rolled := oneShotDayRollover(tc.expr, tc.now)
			if rolled != tc.rolled {
				t.Fatalf("rolled=%v, want %v", rolled, tc.rolled)
			}
			if !rolled {
				return
			}
			if got := target.Format("15:04"); got != tc.target {
				t.Errorf("target = %s, want %s", got, tc.target)
			}
			if got := next.Format("2006-01-02 15:04"); got != tc.next {
				t.Errorf("next = %s, want %s", got, tc.next)
			}
		})
	}
}

func TestIssue5743DetectorAgreesWithNextTime(t *testing.T) {
	// Boundary equivalence: for explicit-minute+hour time-only forms the
	// detector's computed next fire must equal cron.NextTime's answer, and
	// a not-rolled verdict must mean NextTime still resolves to today.
	now := time.Date(2026, 11, 17, 22, 52, 10, 0, time.Local)
	for _, expr := range []string{"40 22 * * *", "0 0 * * *", "53 22 * * *", "30 23 * * *", "0,30 8-20 * * *"} {
		_, next, rolled := oneShotDayRollover(expr, now)
		nt, err := cron.NextTime(expr, now)
		if err != nil {
			t.Fatalf("%s: NextTime: %v", expr, err)
		}
		if rolled && !nt.Equal(next) {
			t.Errorf("%s: detector next %s != NextTime %s", expr, next, nt)
		}
		if !rolled && nt.Day() != now.Day() {
			t.Errorf("%s: detector not rolled but NextTime day=%d (now day=%d)", expr, nt.Day(), now.Day())
		}
		if rolled != (nt.Day() != now.Day()) {
			t.Errorf("%s: rolled=%v but NextTime day=%d (now day=%d)", expr, rolled, nt.Day(), now.Day())
		}
	}
}

func TestIssue5743OneShotResultEchoesNextFire(t *testing.T) {
	s := cron.NewScheduler(func(string, bool) {}, "")
	defer s.Shutdown()
	tool := CronCreateTool{Scheduler: s}

	// Quiet path: a near-term one-shot (stepped minute, always a match
	// within minutes) must echo the computed next fire without any
	// rollover warning.
	result, err := tool.Execute(context.Background(), json.RawMessage(
		`{"cron": "*/5 * * * *", "prompt": "near follow-up", "one_shot": true}`))
	if err != nil {
		t.Fatal(err)
	}
	if result.IsError {
		t.Fatalf("near-term one-shot must not be rejected, got: %s", result.Content)
	}
	if !strings.Contains(result.Content, "next fire: ") || !strings.Contains(result.Content, " from now)") {
		t.Errorf("one-shot create must echo the computed next fire with delta, got: %s", result.Content)
	}
	if strings.Contains(result.Content, "WARNING") {
		t.Errorf("near-term one-shot must not warn, got: %s", result.Content)
	}
}

func TestIssue5743LaterTodayTimeOnlyStillQuiet(t *testing.T) {
	s := cron.NewScheduler(func(string, bool) {}, "")
	defer s.Shutdown()
	tool := CronCreateTool{Scheduler: s}

	// A time-only HH:MM that is still ahead today must create without the
	// ack (the guard must not over-block legitimate same-evening builds -
	// the exact workflow the R4-5 seats were attempting).
	target := time.Now().Add(2 * time.Minute).Truncate(time.Minute)
	if target.Day() != time.Now().Day() || target.Sub(time.Now().Truncate(time.Minute)) < time.Minute {
		t.Skip("wall clock within 2 minutes of midnight; time-only later-today fixture unavailable")
	}
	expr := fmt.Sprintf("%d %d * * *", target.Minute(), target.Hour())
	result, err := tool.Execute(context.Background(), json.RawMessage(fmt.Sprintf(
		`{"cron": %q, "prompt": "later today", "one_shot": true}`, expr)))
	if err != nil {
		t.Fatal(err)
	}
	if result.IsError {
		t.Fatalf("later-today time-only one-shot must create without ack, got: %s", result.Content)
	}
	if strings.Contains(result.Content, "WARNING") {
		t.Errorf("later-today one-shot must not warn, got: %s", result.Content)
	}
	if !strings.Contains(result.Content, "next fire: ") {
		t.Errorf("later-today one-shot must echo next fire, got: %s", result.Content)
	}
}

func TestIssue5743UpdateRescheduleRolloverGuarded(t *testing.T) {
	s := cron.NewScheduler(func(string, bool) {}, "")
	defer s.Shutdown()
	create := CronCreateTool{Scheduler: s}
	update := CronUpdateTool{Scheduler: s}

	// The #5353 far guard already covered reschedules; the #5743 same-day
	// overshoot guard must too - re-pointing an existing one-shot at a
	// passed time-only expression is the same silent day-long deferral.
	created, err := create.Execute(context.Background(), json.RawMessage(
		`{"cron": "*/5 * * * *", "prompt": "reschedule me", "one_shot": true}`))
	if err != nil {
		t.Fatal(err)
	}
	if created.IsError {
		t.Fatalf("seed create failed: %s", created.Content)
	}
	jobID := firstJobID(t, s)

	rej, err := update.Execute(context.Background(), json.RawMessage(fmt.Sprintf(
		`{"jobId": %q, "cron": "0 0 * * *"}`, jobID)))
	if err != nil {
		t.Fatal(err)
	}
	if !rej.IsError {
		t.Fatalf("rollover reschedule must be rejected without tomorrow=true, got success: %s", rej.Content)
	}
	if !strings.Contains(rej.Content, "already passed today") || !strings.Contains(rej.Content, `"tomorrow": true`) {
		t.Errorf("rejection should explain the rollover and the ack, got: %s", rej.Content)
	}

	ack, err := update.Execute(context.Background(), json.RawMessage(fmt.Sprintf(
		`{"jobId": %q, "cron": "0 0 * * *", "tomorrow": true}`, jobID)))
	if err != nil {
		t.Fatal(err)
	}
	if ack.IsError {
		t.Fatalf("acknowledged rollover reschedule must succeed, got: %s", ack.Content)
	}
	if !strings.Contains(ack.Content, "WARNING: same-day overshoot accepted via tomorrow=true") {
		t.Errorf("acknowledged reschedule must carry the rollover warning, got: %s", ack.Content)
	}
	tomorrow := time.Now().AddDate(0, 0, 1).Format("2006-01-02")
	if !strings.Contains(ack.Content, "Next fire: "+tomorrow+" 00:00") {
		t.Errorf("reschedule result should echo tomorrow's fire, got: %s", ack.Content)
	}
}
