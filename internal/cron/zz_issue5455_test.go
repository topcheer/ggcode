package cron

import (
	"testing"
	"time"
)

// TestNextTimeExplicitDate pins #5455: explicit day/month one-shots must
// compute the same-day match when the window is still ahead, and the
// next-year match (not a bogus near date) once the day has passed.
func TestNextTimeExplicitDate(t *testing.T) {
	cst := time.FixedZone("CST", 8*3600)

	// Window still ahead (same day, before 18:00) -> today 18:00.
	from := time.Date(2026, 11, 11, 10, 0, 0, 0, cst)
	got, err := NextTime("0 18 11 11 *", from)
	if err != nil {
		t.Fatalf("err: %v", err)
	}
	want := time.Date(2026, 11, 11, 18, 0, 0, 0, cst)
	if !got.Equal(want) {
		t.Fatalf("ahead-window: want %s, got %s", want, got)
	}

	// Window passed (next day 01:09, the seat's drifted clock) -> exactly
	// one year out; the far-guard rejecting this is CORRECT behavior.
	from2 := time.Date(2026, 11, 12, 1, 9, 0, 0, cst)
	got2, err := NextTime("0 18 11 11 *", from2)
	if err != nil {
		t.Fatalf("err: %v", err)
	}
	want2 := time.Date(2027, 11, 11, 18, 0, 0, 0, cst)
	if !got2.Equal(want2) {
		t.Fatalf("passed-window: want %s, got %s", want2, got2)
	}

	// Minute-precision boundary: at exactly 18:00 the next fire is next
	// year (from is exclusive of the current minute).
	from3 := time.Date(2026, 11, 11, 18, 0, 0, 0, cst)
	got3, err := NextTime("0 18 11 11 *", from3)
	if err != nil {
		t.Fatalf("err: %v", err)
	}
	if !got3.Equal(want2) {
		t.Fatalf("boundary: want %s, got %s", want2, got3)
	}
}
