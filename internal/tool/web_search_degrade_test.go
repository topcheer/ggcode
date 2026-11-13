package tool

import (
	"strings"
	"testing"
)

func TestSearchDegrade_SilentBeforeThreshold(t *testing.T) {
	resetSearchHealth()
	if adv := recordSearchEmpty(); adv != "" {
		t.Errorf("expected no advisory below threshold, got: %s", adv)
	}
	if adv := recordSearchEmpty(); adv != "" {
		t.Errorf("expected no advisory below threshold on 2nd empty, got: %s", adv)
	}
}

func TestSearchDegrade_AdvisoryAfterThreshold(t *testing.T) {
	resetSearchHealth()
	recordSearchEmpty()
	recordSearchEmpty()
	adv := recordSearchEmpty()
	if !strings.Contains(adv, "[search-degraded]") {
		t.Errorf("expected degradation advisory on 3rd consecutive empty, got: %q", adv)
	}
	if !strings.Contains(adv, "web_fetch") {
		t.Errorf("expected advisory to suggest web_fetch fallback, got: %q", adv)
	}
	// stable advisory while streak continues
	adv2 := recordSearchEmpty()
	if !strings.Contains(adv2, "[search-degraded]") {
		t.Errorf("expected continuing advisory, got: %q", adv2)
	}
}

func TestSearchDegrade_RecoveryOnce(t *testing.T) {
	resetSearchHealth()
	recordSearchEmpty()
	recordSearchEmpty()
	recordSearchEmpty()
	rec := recordSearchHit()
	if !strings.Contains(rec, "recovered") {
		t.Errorf("expected one-time recovery note, got: %q", rec)
	}
	if rec2 := recordSearchHit(); rec2 != "" {
		t.Errorf("expected recovery note exactly once, got again: %q", rec2)
	}
}

func TestSearchDegrade_HitResetsCounter(t *testing.T) {
	resetSearchHealth()
	recordSearchEmpty()
	recordSearchEmpty()
	if rec := recordSearchHit(); rec != "" {
		t.Errorf("hit below threshold should not carry recovery note, got: %q", rec)
	}
	// counter must be reset: two more empties stay below threshold
	if adv := recordSearchEmpty(); adv != "" {
		t.Errorf("counter should reset after hit, got early advisory: %q", adv)
	}
	if adv := recordSearchEmpty(); adv != "" {
		t.Errorf("counter should reset after hit, got early advisory: %q", adv)
	}
}

// #5580: a domain filter that empties a non-empty backend result set is a
// backend HIT. recordSearchOutcome(backendTotal>0) must take the hit path
// (reset the streak), not advance the empty counter.
func TestSearchDegrade_DomainFilterEmptyIsBackendHit(t *testing.T) {
	resetSearchHealth()
	// prime the streak with two real backend empties
	recordSearchOutcome(0)
	recordSearchOutcome(0)
	// backend returned 3 results; allowed_domains filter dropped them all
	// -> filtered set is empty, but this is a backend hit
	if adv := recordSearchOutcome(3); adv != "" {
		t.Errorf("filtered-empty hit below threshold must be silent, got: %q", adv)
	}
	// streak was reset: two fresh backend empties must stay below threshold
	if adv := recordSearchOutcome(0); adv != "" {
		t.Errorf("streak must have been reset by the filtered-empty hit, got early advisory: %q", adv)
	}
	if adv := recordSearchOutcome(0); adv != "" {
		t.Errorf("streak must have been reset by the filtered-empty hit, got early advisory: %q", adv)
	}
	// third backend empty crosses the threshold
	if adv := recordSearchOutcome(0); !strings.Contains(adv, "[search-degraded]") {
		t.Errorf("third consecutive backend empty must advise, got: %q", adv)
	}
}

// #5580 regression shape: while advising, a filtered-empty call (backend
// hit) emits the recovery note exactly once - the backend demonstrably works.
func TestSearchDegrade_FilteredEmptyRecoversStreak(t *testing.T) {
	resetSearchHealth()
	recordSearchOutcome(0)
	recordSearchOutcome(0)
	recordSearchOutcome(0)        // advising now
	rec := recordSearchOutcome(2) // backend results, all filtered away
	if !strings.Contains(rec, "recovered") {
		t.Errorf("filtered-empty backend hit must emit one-time recovery note, got: %q", rec)
	}
	if rec2 := recordSearchOutcome(2); rec2 != "" {
		t.Errorf("recovery note must fire exactly once, got again: %q", rec2)
	}
}
