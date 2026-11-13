package tool

import (
	"fmt"
	"sync"
)

// searchHealth tracks consecutive backend-level empty-result web_search
// calls within this process (#5578). "Empty" means the backend returned
// zero results BEFORE domain filtering (#5580): a domain filter that
// empties a non-empty backend result set is a backend hit, never counted
// toward the streak. After a threshold of consecutive backend empties,
// results carry an explicit degradation advisory so downstream agents stop
// blind retries and switch to web_fetch on known canonical URLs instead.
type searchHealth struct {
	mu                 sync.Mutex
	consecutiveEmpties int
	advising           bool
}

const searchEmptyDegradeThreshold = 3

var searchDegrade = &searchHealth{}

func resetSearchHealth() {
	searchDegrade.mu.Lock()
	defer searchDegrade.mu.Unlock()
	searchDegrade.consecutiveEmpties = 0
	searchDegrade.advising = false
}

// recordSearchOutcome feeds the backend-level outcome of one web_search
// call into the degrade counter (#5580). backendTotal is the result count
// BEFORE domain filtering: >0 counts as a hit (even if the domain filter
// then empties the set), 0 counts as a backend empty.
func recordSearchOutcome(backendTotal int) string {
	if backendTotal == 0 {
		return recordSearchEmpty()
	}
	return recordSearchHit()
}

// recordSearchEmpty increments the consecutive-empty counter after a
// backend-level empty-result call (zero results before domain filtering)
// and returns a degradation advisory once the threshold is crossed
// (stable text afterwards while the streak continues).
func recordSearchEmpty() string {
	searchDegrade.mu.Lock()
	defer searchDegrade.mu.Unlock()
	searchDegrade.consecutiveEmpties++
	if searchDegrade.consecutiveEmpties < searchEmptyDegradeThreshold {
		return ""
	}
	searchDegrade.advising = true
	return fmt.Sprintf(
		"\n\n[search-degraded] web_search has returned empty results for %d consecutive queries this session - the DuckDuckGo backend is likely degraded or blocked (rate limit / outage). Stop retrying web_search; use web_fetch on known canonical URLs instead (arXiv API: https://arxiv.org/api/query?search_query=..., official documentation domains, previously fetched authoritative pages).",
		searchDegrade.consecutiveEmpties)
}

// recordSearchHit resets the consecutive-empty counter after a
// result-bearing backend call (backend returned >=1 pre-filter result);
// if the backend had been flagged degraded, emit a recovery note exactly
// once.
func recordSearchHit() string {
	searchDegrade.mu.Lock()
	defer searchDegrade.mu.Unlock()
	if searchDegrade.advising {
		n := searchDegrade.consecutiveEmpties
		searchDegrade.advising = false
		searchDegrade.consecutiveEmpties = 0
		return fmt.Sprintf("\n\n[search-degraded] backend recovered after %d empty queries - web_search usable again.", n)
	}
	searchDegrade.consecutiveEmpties = 0
	return ""
}

// searchFilterEmptyMessage renders the #5580 domain-filter-empty advisory:
// the backend returned backendTotal results but allowed_domains/blocked_domains
// excluded every one of them. The message steers the agent toward widening
// the filter instead of concluding the backend is degraded.
func searchFilterEmptyMessage(backendTotal int) string {
	return fmt.Sprintf(
		"\n\n[search-filter-empty] backend returned %d results but all were excluded by allowed_domains/blocked_domains - the backend is healthy; widen the filter instead of retrying the same query.",
		backendTotal)
}
