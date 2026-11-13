package tool

import (
	"fmt"
	"sync"
)

// searchHealth tracks consecutive empty-result web_search calls within this
// process (#5578). After a threshold of consecutive empty results, results
// carry an explicit degradation advisory so downstream agents stop blind
// retries and switch to web_fetch on known canonical URLs instead.
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

// recordSearchEmpty increments the consecutive-empty counter after an
// empty-result call and returns a degradation advisory once the threshold is
// crossed (stable text afterwards while the streak continues).
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
// result-bearing call; if the backend had been flagged degraded, emit a
// recovery note exactly once.
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
