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

// #5594: a one-shot cron_create whose explicitly specified date/time is in
// the past (stale prompt date - e.g. the system prompt embedded yesterday's
// date) must be rejected outright. cron.NextTime rolls the schedule ~1 year
// forward (364 days out in the OBS evidence), and far=true must NOT bypass
// the rejection (DoD 2: rejection takes precedence over the far exemption).
// Wildcard-day far-future one-shots (no single specified date) remain
// creatable with far=true (DoD 3), and the rejection copy guides toward
// recomputation or a recurring expression (DoD 4).

// explicitDateExpr mints a 5-field cron with an explicit single day+month
// (the #5594 scope) at 09:05 local, offset by daysAgo days from now.
func explicitDateExpr(daysAgo int) string {
	target := time.Now().AddDate(0, 0, -daysAgo)
	return fmt.Sprintf("5 9 %d %d *", target.Day(), int(target.Month()))
}

func TestIssue5594PastDateOneShotRejected(t *testing.T) {
	s := cron.NewScheduler(func(string, bool) {}, "")
	defer s.Shutdown()
	tool := CronCreateTool{Scheduler: s}

	result, err := tool.Execute(context.Background(), json.RawMessage(fmt.Sprintf(
		`{"cron": %q, "prompt": "stale follow-up", "one_shot": true}`, explicitDateExpr(3))))
	if err != nil {
		t.Fatal(err)
	}
	if !result.IsError {
		t.Fatalf("past-date one-shot must be rejected, got success: %s", result.Content)
	}
	for _, want := range []string{
		"in the past",
		"silently roll the fire ~1 year forward",
		"recurring expression", // DoD 4 guidance
	} {
		if !strings.Contains(result.Content, want) {
			t.Errorf("rejection should mention %q, got: %s", want, result.Content)
		}
	}
	if jobs := s.List(); len(jobs) != 0 {
		t.Errorf("rejected one-shot must not be created, found %d job(s)", len(jobs))
	}
}

func TestIssue5594FarFlagDoesNotBypassPastDate(t *testing.T) {
	s := cron.NewScheduler(func(string, bool) {}, "")
	defer s.Shutdown()
	tool := CronCreateTool{Scheduler: s}

	result, err := tool.Execute(context.Background(), json.RawMessage(fmt.Sprintf(
		`{"cron": %q, "prompt": "stale follow-up", "one_shot": true, "far": true}`, explicitDateExpr(3))))
	if err != nil {
		t.Fatal(err)
	}
	if !result.IsError {
		t.Fatalf("far=true must NOT bypass a past-date rejection, got success: %s", result.Content)
	}
	if !strings.Contains(result.Content, "far=true does not bypass a past-date rejection") {
		t.Errorf("rejection should state the far bypass is closed, got: %s", result.Content)
	}
	if jobs := s.List(); len(jobs) != 0 {
		t.Errorf("rejected one-shot must not be created, found %d job(s)", len(jobs))
	}
}

func TestIssue5594EarlierTodayRejected(t *testing.T) {
	s := cron.NewScheduler(func(string, bool) {}, "")
	defer s.Shutdown()
	tool := CronCreateTool{Scheduler: s}

	// Today at 00:00 is always in the past (the current instant is at or
	// after midnight), pinning the "including today's already-passed
	// time-of-day" clause of DoD 1 regardless of wall-clock run time.
	now := time.Now()
	expr := fmt.Sprintf("0 0 %d %d *", now.Day(), int(now.Month()))
	result, err := tool.Execute(context.Background(), json.RawMessage(fmt.Sprintf(
		`{"cron": %q, "prompt": "earlier today", "one_shot": true}`, expr)))
	if err != nil {
		t.Fatal(err)
	}
	if !result.IsError {
		t.Fatalf("today-at-midnight one-shot must be rejected as past, got success: %s", result.Content)
	}
	if !strings.Contains(result.Content, "in the past") {
		t.Errorf("rejection should mention the past date, got: %s", result.Content)
	}
}

func TestIssue5594TodayLaterTimeCreatable(t *testing.T) {
	s := cron.NewScheduler(func(string, bool) {}, "")
	defer s.Shutdown()
	tool := CronCreateTool{Scheduler: s}

	// A specified moment still ahead (now+1h, same day when it does not
	// cross midnight; tomorrow otherwise) is legitimate and must create.
	target := time.Now().Add(1 * time.Hour)
	expr := fmt.Sprintf("%d %d %d %d *", target.Minute(), target.Hour(), target.Day(), int(target.Month()))
	result, err := tool.Execute(context.Background(), json.RawMessage(fmt.Sprintf(
		`{"cron": %q, "prompt": "soon follow-up", "one_shot": true}`, expr)))
	if err != nil {
		t.Fatal(err)
	}
	if result.IsError {
		t.Fatalf("future-specified one-shot must be creatable, got rejection: %s", result.Content)
	}
	if jobs := s.List(); len(jobs) != 1 {
		t.Fatalf("expected 1 created job, got %d", len(jobs))
	}
}

func TestIssue5594FarFutureWildcardDayStillLegal(t *testing.T) {
	s := cron.NewScheduler(func(string, bool) {}, "")
	defer s.Shutdown()
	tool := CronCreateTool{Scheduler: s}

	// DoD 3 pin: a wildcard-day far-future one-shot (no single specified
	// date to go stale) keeps the #5353 far=true acknowledgement path.
	result, err := tool.Execute(context.Background(), json.RawMessage(fmt.Sprintf(
		`{"cron": %q, "prompt": "far follow-up", "one_shot": true, "far": true}`, farMonthExpr())))
	if err != nil {
		t.Fatal(err)
	}
	if result.IsError {
		t.Fatalf("far=true wildcard-day one-shot must stay legal, got rejection: %s", result.Content)
	}
	if !strings.Contains(result.Content, "WARNING: far-future one-shot accepted via far=true") {
		t.Errorf("far=true success should carry the loud warning, got: %s", result.Content)
	}
	if jobs := s.List(); len(jobs) != 1 {
		t.Fatalf("expected 1 job with far=true, got %d", len(jobs))
	}
}

func TestIssue5594DayOnlyExplicitNotPastChecked(t *testing.T) {
	s := cron.NewScheduler(func(string, bool) {}, "")
	defer s.Shutdown()
	tool := CronCreateTool{Scheduler: s}

	// Scoping pin: day-of-month explicit with a wildcard month is outside
	// the #5594 rejection scope (a stale monthly day rolls at most ~31
	// days, which the regular horizon guard scopes) and must create.
	result, err := tool.Execute(context.Background(), json.RawMessage(
		`{"cron": "5 9 13 * *", "prompt": "monthly 13th", "one_shot": true}`))
	if err != nil {
		t.Fatal(err)
	}
	if result.IsError {
		t.Fatalf("day-only explicit one-shot must not trip the past-date gate, got: %s", result.Content)
	}
	if jobs := s.List(); len(jobs) != 1 {
		t.Fatalf("expected 1 created job, got %d", len(jobs))
	}
}
