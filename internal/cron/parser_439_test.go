package cron

import (
	"testing"
	"time"
)

// #439: reversed ranges must be rejected everywhere, including inside lists.
func TestParseReversedRange(t *testing.T) {
	base := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	if _, err := NextTime("5,50-10 * * * *", base); err == nil {
		t.Error("reversed range inside list must error")
	}
	if _, err := NextTime("50-10 * * * *", base); err == nil {
		t.Error("standalone reversed range must error")
	}
	if _, err := NextTime("10-50 * * * *", base); err != nil {
		t.Errorf("valid range must parse: %v", err)
	}
}
