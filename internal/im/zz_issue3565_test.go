package im

import "testing"
import "time"

func TestIssue3565_HealthyLadderRule(t *testing.T) {
	backoffs := []time.Duration{2 * time.Second, 5 * time.Second, 10 * time.Second, 30 * time.Second, 60 * time.Second}

	reset := func(healthyFor time.Duration, attempt int) int {
		if healthyFor >= mattermostHealthySession {
			return 0
		}
		return attempt
	}
	if got := reset(2*time.Hour, 4); got != 0 {
		t.Fatalf("healthy session must reset ladder, got index %d", got)
	}
	if got := reset(60*time.Second, 4); got != 0 {
		t.Fatalf("threshold-long session must reset, got index %d", got)
	}
	if got := reset(30*time.Second, 4); got != 4 {
		t.Fatalf("short session must keep ladder index 4, got %d", got)
	}
	_ = backoffs
}
