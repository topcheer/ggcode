package tool

import (
	"strings"
	"testing"
)

// #5580 regression: the domain-filter-empty advisory must exist and steer
// toward widening the filter, not toward abandoning web_search. The hint
// was specified by the issue and by the v1.3.282 release notes but was
// dropped from the #5581 rework; these pins keep it from rotting again.
func TestIssue5580SearchFilterEmptyMessage(t *testing.T) {
	msg := searchFilterEmptyMessage(7)
	for _, want := range []string{"[search-filter-empty]", "7", "allowed_domains/blocked_domains", "widen the filter"} {
		if !strings.Contains(msg, want) {
			t.Errorf("filter-empty message missing %q: %q", want, msg)
		}
	}
	// The hint must NOT claim backend degradation - that is the exact
	// misleading advisory class the issue filed.
	if strings.Contains(msg, "[search-degraded]") {
		t.Errorf("filter-empty message must not carry the backend-degraded advisory: %q", msg)
	}
}

// Behavioral pin on the observable composition in web_search.go: a healthy
// backend whose results were all excluded by the domain filter renders the
// filter-empty hint, while a true backend empty keeps the plain message.
// Loop-shape checks are not enough (see #5039); this drives the real branch.
func TestIssue5580EmptyResultBranchComposition(t *testing.T) {
	// Healthy backend, all results excluded by the filter: both conditions
	// of the production guard hold (backendTotal > 0, domain filters set).
	if got := composeEmptySearchMessage(3, true, ""); !strings.Contains(got, "[search-filter-empty]") {
		t.Errorf("filtered-empty with healthy backend must carry the widen-filter hint, got: %q", got)
	} else if !strings.HasPrefix(got, "No results found.") {
		t.Errorf("filtered-empty message must keep the standard prefix, got: %q", got)
	}
	// Backend itself returned nothing: plain message, no filter hint even
	// if domain filters were set.
	if got := composeEmptySearchMessage(0, true, ""); strings.Contains(got, "[search-filter-empty]") {
		t.Errorf("true backend empty must not claim the filter excluded results, got: %q", got)
	}
	// No domain filters configured: never attribute an empty set to filtering.
	if got := composeEmptySearchMessage(0, false, ""); strings.Contains(got, "[search-filter-empty]") {
		t.Errorf("empty without domain filters must not mention the filter, got: %q", got)
	}
	// Degrade advisory (if any) still appends after the filter hint.
	if got := composeEmptySearchMessage(3, true, "\n\n[search-degraded] stub"); !strings.Contains(got, "[search-filter-empty]") || !strings.Contains(got, "[search-degraded]") {
		t.Errorf("advisory must append after filter hint, got: %q", got)
	}
}

// Counter-semantics re-pin through the single recordSearchOutcome entry:
// filtered-empty calls are backend hits and reset the streak.
func TestIssue5580FilteredEmptyDoesNotAdvanceStreak(t *testing.T) {
	resetSearchHealth()
	recordSearchOutcome(0)
	recordSearchOutcome(0)
	if adv := recordSearchOutcome(5); adv != "" {
		t.Errorf("filtered-empty backend hit below threshold must be silent, got: %q", adv)
	}
	if adv := recordSearchOutcome(0); adv != "" {
		t.Errorf("streak must have been reset by the filtered-empty hit, got: %q", adv)
	}
	if adv := recordSearchOutcome(0); adv != "" {
		t.Errorf("second post-hit empty must stay below threshold, got: %q", adv)
	}
	if adv := recordSearchOutcome(0); !strings.Contains(adv, "[search-degraded]") {
		t.Errorf("third consecutive backend empty must advise, got: %q", adv)
	}
}
