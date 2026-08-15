package cron

import (
	"testing"
	"time"
)

func TestParseField_OutOfRangeRejected(t *testing.T) {
	// #415: range/step bounds out of range must error (same policy as the
	// single-value path) instead of being silently clamped into different
	// semantics.
	tests := []struct {
		part    string
		min, mx int
	}{
		{"60/5", 0, 23},   // hour step: 60 out of range
		{"13-20", 1, 12},  // month range: 13 out of range
		{"0-70", 0, 59},   // minute range: 70 out of range
		{"24-2/2", 0, 23}, // range-with-step: 24 out of range
	}
	for _, tt := range tests {
		if err := parseFieldPart(tt.part, tt.min, tt.mx, map[int]bool{}); err == nil {
			t.Errorf("parseFieldPart(%q) expected out-of-range error, got nil", tt.part)
		}
	}
}

func TestParseField_Comma(t *testing.T) {
	cf, err := parseField("1,15", 0, 59)
	if err != nil {
		t.Fatalf("parseField error: %v", err)
	}
	if !cf.values[1] || !cf.values[15] {
		t.Error("expected values 1 and 15")
	}
}

func TestParseField_Range(t *testing.T) {
	cf, err := parseField("1-5", 0, 59)
	if err != nil {
		t.Fatalf("parseField error: %v", err)
	}
	for i := 1; i <= 5; i++ {
		if !cf.values[i] {
			t.Errorf("expected value %d", i)
		}
	}
}

func TestParseField_Step(t *testing.T) {
	cf, err := parseField("*/15", 0, 59)
	if err != nil {
		t.Fatalf("parseField error: %v", err)
	}
	if !cf.values[0] || !cf.values[15] || !cf.values[30] || !cf.values[45] {
		t.Error("expected values 0,15,30,45")
	}
}

func TestParseField_RangeStep(t *testing.T) {
	cf, err := parseField("10-20/5", 0, 59)
	if err != nil {
		t.Fatalf("parseField error: %v", err)
	}
	if !cf.values[10] || !cf.values[15] || !cf.values[20] {
		t.Error("expected values 10,15,20")
	}
}

func TestParseField_Star(t *testing.T) {
	cf, err := parseField("*", 0, 59)
	if err != nil {
		t.Fatalf("parseField error: %v", err)
	}
	if len(cf.values) != 60 {
		t.Errorf("expected 60 values, got %d", len(cf.values))
	}
}

func TestParseField_InvalidRange(t *testing.T) {
	_, err := parseField("70", 0, 59)
	if err == nil {
		t.Error("expected error for out of range")
	}
}

func TestParseField_InvalidValue(t *testing.T) {
	_, err := parseField("abc", 0, 59)
	if err == nil {
		t.Error("expected error for invalid value")
	}
}

func TestParseField_Empty(t *testing.T) {
	_, err := parseField("", 0, 59)
	// Empty field may parse to empty values
	_ = err
}

func TestParseFieldPart_ReverseRange(t *testing.T) {
	values := make(map[int]bool)
	err := parseFieldPart("5-3", 0, 59, values)
	// Reversed range: may error or produce empty set
	_ = err
	_ = values
}

func TestNextTime_AllFields(t *testing.T) {
	// Every minute
	now := time.Date(2024, 1, 1, 12, 0, 0, 0, time.UTC)
	next, err := NextTime("* * * * *", now)
	if err != nil {
		t.Fatalf("NextTime error: %v", err)
	}
	if next.Minute() != 1 {
		t.Errorf("expected minute 1, got %d", next.Minute())
	}
}

func TestNextTime_SpecificMinute(t *testing.T) {
	now := time.Date(2024, 1, 1, 12, 30, 0, 0, time.UTC)
	next, err := NextTime("0 * * * *", now)
	if err != nil {
		t.Fatalf("NextTime error: %v", err)
	}
	if next.Minute() != 0 {
		t.Errorf("expected minute 0, got %d", next.Minute())
	}
}

func TestNextTime_InvalidExpr(t *testing.T) {
	_, err := NextTime("invalid", time.Now())
	if err == nil {
		t.Error("expected error for invalid expression")
	}
}

func TestScheduler_SetEnqueue(t *testing.T) {
	noop := func(string, bool) {}
	s := NewScheduler(noop, "")
	called := false
	s.SetEnqueue(func(prompt string, _ bool) {
		called = true
	})
	_ = called
	s.Shutdown()
}

func TestScheduler_SetEnqueue_Nil(t *testing.T) {
	noop := func(string, bool) {}
	s := NewScheduler(noop, "")
	s.SetEnqueue(nil) // should not panic
	s.Shutdown()
}

func TestScheduler_Shutdown(t *testing.T) {
	noop := func(string, bool) {}
	s := NewScheduler(noop, "")
	s.Shutdown()
	// Double shutdown should not panic
	s.Shutdown()
}
