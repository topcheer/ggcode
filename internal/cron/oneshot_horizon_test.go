package cron

import (
	"fmt"
	"strings"
	"testing"
	"time"
)

// farFutureExpr builds a "0 9 <day> <month> *" expression pointing roughly
// five months out, guaranteed beyond MaxOneShotHorizon from now.
func farFutureExpr() string {
	target := time.Now().AddDate(0, 5, 0)
	return fmt.Sprintf("0 9 %d %d *", target.Day(), int(target.Month()))
}

func TestCheckOneShotHorizonBoundary(t *testing.T) {
	now := time.Date(2026, 1, 15, 10, 0, 0, 0, time.Local)

	// Exactly at the horizon: allowed (limit is exclusive).
	if err := checkOneShotHorizon(false, "0 10 15 4 *", now.Add(MaxOneShotHorizon), now, MaxOneShotHorizon); err != nil {
		t.Fatalf("one-shot exactly at the horizon should be allowed, got: %v", err)
	}
	// One minute past the horizon: rejected.
	err := checkOneShotHorizon(false, "1 10 15 4 *", now.Add(MaxOneShotHorizon+time.Minute), now, MaxOneShotHorizon)
	if err == nil {
		t.Fatal("one-shot past the horizon must be rejected")
	}
	// Well within the horizon: allowed.
	if err := checkOneShotHorizon(false, "0 11 15 1 *", now.Add(time.Hour), now, MaxOneShotHorizon); err != nil {
		t.Fatalf("near one-shot should be allowed, got: %v", err)
	}
	// The horizon is a parameter, not a hardwired constant: a tighter
	// horizon rejects what the default would allow and vice versa.
	if err := checkOneShotHorizon(false, "x", now.Add(2*time.Hour), now, time.Hour); err == nil {
		t.Fatal("custom tighter horizon must be honored")
	}
	if err := checkOneShotHorizon(false, "x", now.Add(2*time.Hour), now, 3*time.Hour); err != nil {
		t.Fatalf("custom looser horizon must be honored, got: %v", err)
	}
	// Recurring jobs are never subject to the guard, however far out.
	if err := checkOneShotHorizon(true, "0 9 15 4 *", now.Add(10*365*24*time.Hour), now, MaxOneShotHorizon); err != nil {
		t.Fatalf("recurring jobs must never be rejected by the one-shot horizon guard, got: %v", err)
	}
	// Zero next-fire time is not a horizon violation.
	if err := checkOneShotHorizon(false, "x", time.Time{}, now, MaxOneShotHorizon); err != nil {
		t.Fatalf("zero next-fire must be ignored, got: %v", err)
	}
}

func TestCreateOneShotNextDayAllowed(t *testing.T) {
	s := NewScheduler(func(string, bool) {}, "")
	tomorrow := time.Now().AddDate(0, 0, 1)
	expr := fmt.Sprintf("0 9 %d %d *", tomorrow.Day(), int(tomorrow.Month()))

	job, err := s.Create(expr, "reminder", false, false)
	if err != nil {
		t.Fatalf("next-day one-shot reminder must be allowed: %v", err)
	}
	if d := time.Until(job.NextFire); d > MaxOneShotHorizon {
		t.Fatalf("next-day job fired %s out, expected well within %s", d, MaxOneShotHorizon)
	}
}

func TestCreateOneShotFarFutureRejected(t *testing.T) {
	s := NewScheduler(func(string, bool) {}, "")
	expr := farFutureExpr()

	expected, err := NextTime(expr, time.Now())
	if err != nil {
		t.Fatalf("building expected trigger time: %v", err)
	}

	_, err = s.Create(expr, "reminder", false, false)
	if err == nil {
		t.Fatal("one-shot job beyond the 90-day horizon must be rejected at creation")
	}
	msg := err.Error()
	if want := expected.Format("2006-01-02 15:04"); !strings.Contains(msg, want) {
		t.Errorf("rejection error must contain the computed trigger time %q, got: %s", want, msg)
	}
	if !strings.Contains(msg, "specify the full date") {
		t.Errorf("rejection error must tell the user to specify the full date explicitly, got: %s", msg)
	}
	if got := s.List(); len(got) != 0 {
		t.Errorf("rejected job must not be stored, found %d job(s)", len(got))
	}
}

func TestCreateRecurringFarFutureAllowed(t *testing.T) {
	s := NewScheduler(func(string, bool) {}, "")
	expr := farFutureExpr()

	if _, err := s.Create(expr, "periodic check", true, false); err != nil {
		t.Fatalf("recurring jobs must not be affected by the one-shot horizon guard: %v", err)
	}
}

// Absorbed from #5354 (credit: teamclaw developer seat): explicit far
// acknowledgement turns the hard reject into an opt-in path.
func TestCreateOneShotFarFutureAcknowledgedAllowed(t *testing.T) {
	s := NewScheduler(nil, t.TempDir())
	// farFutureExpr is the package's tz-independent ~5-months-out form,
	// guaranteed beyond MaxOneShotHorizon (a naive "next year same day"
	// HH:MM form is timezone-dependent and can land only hours away).
	expr := farFutureExpr()
	_, err := s.CreateAcknowledgedFar(expr, "far reminder", false, false, true)
	if err != nil {
		t.Fatalf("far-acknowledged one-shot must be allowed, got: %v", err)
	}
	// default (unacknowledged) still rejects the same expression
	_, err = s.Create(expr, "far reminder", false, false)
	if err == nil {
		t.Fatal("unacknowledged far one-shot must still be rejected")
	}
}
