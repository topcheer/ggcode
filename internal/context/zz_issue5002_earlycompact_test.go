package context

import "testing"

// #5002 gap 2: an early compact ratio must move the auto-compact threshold
// earlier (min(window-reserve, ratio*window)) without affecting the disabled
// default, so single long-task sessions (sub-agents) can pre-compact at ~70%
// of the metadata-derived window instead of hitting the reactive overflow path.
func TestIssue5002EarlyCompactRatioLowersThreshold(t *testing.T) {
	m := NewManager(100000)
	base := m.AutoCompactThreshold()
	if base <= 0 || base >= 100000 {
		t.Fatalf("default threshold must be window-reserve in (0, window), got %d", base)
	}

	m.SetEarlyCompactRatio(0.70)
	early := m.AutoCompactThreshold()
	want := 70000 // 0.70 * 100000 < 100000 - reserve (~90%)
	if early != want {
		t.Fatalf("early ratio must pin threshold at ratio*window: got %d want %d", early, want)
	}
	if early >= base {
		t.Fatalf("early threshold (%d) must be lower than default (%d)", early, base)
	}
	if r := m.EarlyCompactRatio(); r != 0.70 {
		t.Fatalf("EarlyCompactRatio must report 0.70, got %v", r)
	}
}

func TestIssue5002EarlyCompactRatioDisabledByDefaultAndInvalid(t *testing.T) {
	m := NewManager(100000)
	if r := m.EarlyCompactRatio(); r != 0 {
		t.Fatalf("default early ratio must be disabled (0), got %v", r)
	}
	base := m.AutoCompactThreshold()

	// Invalid values must not enable an early trigger.
	m.SetEarlyCompactRatio(1.0) // >= 1 → disabled
	if m.AutoCompactThreshold() != base {
		t.Fatalf("ratio >= 1 must keep threshold at default: got %d want %d", m.AutoCompactThreshold(), base)
	}
	if r := m.EarlyCompactRatio(); r != 0 {
		t.Fatalf("ratio >= 1 must clamp to disabled (0), got %v", r)
	}
	m.SetEarlyCompactRatio(-0.5) // negative → disabled
	if m.AutoCompactThreshold() != base {
		t.Fatalf("negative ratio must keep threshold at default: got %d want %d", m.AutoCompactThreshold(), base)
	}
	m.SetEarlyCompactRatio(0) // explicit disable
	if m.AutoCompactThreshold() != base {
		t.Fatalf("ratio 0 must keep threshold at default: got %d want %d", m.AutoCompactThreshold(), base)
	}

	// Re-enabling after disable works (last-set wins).
	m.SetEarlyCompactRatio(0.5)
	if got, want := m.AutoCompactThreshold(), 50000; got != want {
		t.Fatalf("re-enable must set threshold to ratio*window: got %d want %d", got, want)
	}
}

func TestIssue5002EarlyCompactRatioNeverBelowMinReserve(t *testing.T) {
	// Tiny window: ratio*window would fall below minSummaryReserve; the floor
	// must still apply so a trigger remains meaningful.
	m := NewManager(200)
	m.SetEarlyCompactRatio(0.10) // 20 tokens < minSummaryReserve floor
	if got := m.AutoCompactThreshold(); got < minSummaryReserve {
		t.Fatalf("threshold must never fall below minSummaryReserve (%d), got %d", minSummaryReserve, got)
	}
}
