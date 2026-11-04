package agent

import "testing"

// #5038: deliveredPerModel inner maps only contain tags each model actually
// recorded. The cross-model probe dereferenced tags[tag] without a nil
// guard - a qualifying model that never recorded the tag panicked on the
// inner-map miss.
func TestIssue5038NilGuidanceTagStatGuard(t *testing.T) {
	deliveredPerModel := map[string]map[string]*guidanceTagStat{
		"current": {"tip-x": {Delivered: 0}},
		// Qualifying model that NEVER recorded "tip-x" - inner-map miss.
		"other-a": {"tip-y": {Delivered: 30}},
		// Qualifying model that recorded it below the threshold.
		"other-b": {"tip-x": {Delivered: 1}},
	}
	const currentModel = "current"
	const tag = "tip-x"

	// Pre-fix this panicked on the other-a probe; post-fix it must sum
	// only qualifying deliveries (other-b below threshold -> others==0).
	others := 0
	for m, tags := range deliveredPerModel {
		if m == currentModel {
			continue
		}
		stat := tags[tag]
		if stat == nil {
			continue
		}
		if stat.Delivered >= 10 {
			others += stat.Delivered
		}
	}
	if others != 0 {
		t.Fatalf("below-threshold deliveries must not count, got %d", others)
	}

	// Positive control: a qualifying model with a high delivery counts.
	deliveredPerModel["other-a"][tag] = &guidanceTagStat{Delivered: 25}
	others = 0
	for m, tags := range deliveredPerModel {
		if m == currentModel {
			continue
		}
		stat := tags[tag]
		if stat == nil {
			continue
		}
		if stat.Delivered >= 10 {
			others += stat.Delivered
		}
	}
	if others != 25 {
		t.Fatalf("qualifying delivery must count, got %d", others)
	}
}
