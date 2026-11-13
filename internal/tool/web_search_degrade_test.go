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
