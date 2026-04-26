package cron

import (
	"testing"
	"time"
)

func TestNextTimeEveryMinute(t *testing.T) {
	from := time.Date(2025, 3, 15, 10, 30, 0, 0, time.UTC)
	next, err := NextTime("*/1 * * * *", from)
	if err != nil {
		t.Fatal(err)
	}
	expected := time.Date(2025, 3, 15, 10, 31, 0, 0, time.UTC)
	if next != expected {
		t.Errorf("expected %v, got %v", expected, next)
	}
}

func TestNextTimeEvery5Minutes(t *testing.T) {
	from := time.Date(2025, 3, 15, 10, 32, 0, 0, time.UTC)
	next, err := NextTime("*/5 * * * *", from)
	if err != nil {
		t.Fatal(err)
	}
	// Next 5-minute mark after 10:32 is 10:35
	expected := time.Date(2025, 3, 15, 10, 35, 0, 0, time.UTC)
	if next != expected {
		t.Errorf("expected %v, got %v", expected, next)
	}
}

func TestNextTimeSpecificHour(t *testing.T) {
	from := time.Date(2025, 3, 15, 10, 30, 0, 0, time.UTC)
	next, err := NextTime("0 14 * * *", from)
	if err != nil {
		t.Fatal(err)
	}
	expected := time.Date(2025, 3, 15, 14, 0, 0, 0, time.UTC)
	if next != expected {
		t.Errorf("expected %v, got %v", expected, next)
	}
}

func TestNextTimeWeekdaysOnly(t *testing.T) {
	// 2025-03-15 is a Saturday
	from := time.Date(2025, 3, 15, 10, 0, 0, 0, time.UTC)
	next, err := NextTime("0 9 * * 1-5", from)
	if err != nil {
		t.Fatal(err)
	}
	// Should be Monday 2025-03-17 at 09:00
	expected := time.Date(2025, 3, 17, 9, 0, 0, 0, time.UTC)
	if next != expected {
		t.Errorf("expected %v (weekday=%d), got %v (weekday=%d)",
			expected, expected.Weekday(), next, next.Weekday())
	}
}

func TestNextTimeSpecificMonth(t *testing.T) {
	from := time.Date(2025, 6, 15, 10, 0, 0, 0, time.UTC)
	next, err := NextTime("0 0 1 12 *", from)
	if err != nil {
		t.Fatal(err)
	}
	expected := time.Date(2025, 12, 1, 0, 0, 0, 0, time.UTC)
	if next != expected {
		t.Errorf("expected %v, got %v", expected, next)
	}
}

func TestNextTimeRangeWithStep(t *testing.T) {
	from := time.Date(2025, 3, 15, 10, 0, 0, 0, time.UTC)
	next, err := NextTime("0-30/10 * * * *", from)
	if err != nil {
		t.Fatal(err)
	}
	expected := time.Date(2025, 3, 15, 10, 10, 0, 0, time.UTC)
	if next != expected {
		t.Errorf("expected %v, got %v", expected, next)
	}
}

func TestNextTimeListValues(t *testing.T) {
	from := time.Date(2025, 3, 15, 10, 0, 0, 0, time.UTC)
	next, err := NextTime("15,45 * * * *", from)
	if err != nil {
		t.Fatal(err)
	}
	expected := time.Date(2025, 3, 15, 10, 15, 0, 0, time.UTC)
	if next != expected {
		t.Errorf("expected %v, got %v", expected, next)
	}
}

func TestNextTimeDaily9AM(t *testing.T) {
	from := time.Date(2025, 3, 15, 9, 0, 0, 0, time.UTC)
	// Already at 9:00, should get next day's 9:00
	next, err := NextTime("0 9 * * *", from)
	if err != nil {
		t.Fatal(err)
	}
	expected := time.Date(2025, 3, 16, 9, 0, 0, 0, time.UTC)
	if next != expected {
		t.Errorf("expected %v, got %v", expected, next)
	}
}

func TestNextTimeHourly(t *testing.T) {
	from := time.Date(2025, 3, 15, 10, 30, 0, 0, time.UTC)
	next, err := NextTime("0 * * * *", from)
	if err != nil {
		t.Fatal(err)
	}
	expected := time.Date(2025, 3, 15, 11, 0, 0, 0, time.UTC)
	if next != expected {
		t.Errorf("expected %v, got %v", expected, next)
	}
}

func TestNextTimeInvalidFields(t *testing.T) {
	_, err := NextTime("invalid", time.Now())
	if err == nil {
		t.Error("expected error for invalid expression")
	}

	_, err = NextTime("60 * * * *", time.Now())
	if err == nil {
		t.Error("expected error for minute out of range")
	}

	_, err = NextTime("* 25 * * *", time.Now())
	if err == nil {
		t.Error("expected error for hour out of range")
	}
}

func TestNextTimeAllWildcard(t *testing.T) {
	from := time.Date(2025, 3, 15, 10, 30, 0, 0, time.UTC)
	next, err := NextTime("* * * * *", from)
	if err != nil {
		t.Fatal(err)
	}
	// Every minute — should be the next minute
	expected := time.Date(2025, 3, 15, 10, 31, 0, 0, time.UTC)
	if next != expected {
		t.Errorf("expected %v, got %v", expected, next)
	}
}
