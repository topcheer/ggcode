package agent

// #618: exemption branches in retry_quality_check.go used pure name-substring
// matching, so unbounded retry loops were silently exempted by unrelated
// identifiers (attemptLog, countryCode) and any `<-x.C` receive masqueraded
// as a timer backoff.

import (
	"testing"
)

// Defect 1: a for-init counter name alone (no condition, no body comparison)
// must NOT exempt an unbounded retry loop.
func TestIssue618_InitCounterNameAloneNotExempt(t *testing.T) {
	src := `package main
import "net/http"
func fetch(url string) {
	for attempt := 0; ; attempt++ {
		_, err := http.Get(url)
		if err != nil {
			continue
		}
		break
	}
}`
	w := checkRetryQuality("retry618.go", "", src)
	if !hasWarning(w, "no attempt cap") {
		t.Fatalf("expected unbounded-retry warning despite init counter name, got %v", w)
	}
	if !hasWarning(w, "no backoff delay") {
		t.Fatalf("expected missing-backoff warning, got %v", w)
	}
}

// Defect 1/2: identifiers that merely CONTAIN counter keywords (countryCode,
// attemptLog, retriesEnabled) must not be treated as attempt caps.
func TestIssue618_SubstringNamesNotExempt(t *testing.T) {
	src := `package main
import "net/http"
var attemptLog *int
var retriesEnabled bool
func fetch(url string) {
	for {
		_, err := http.Get(url)
		if err != nil {
			if attemptLog != nil || retriesEnabled {
				continue
			}
			continue
		}
		break
	}
}`
	w := checkRetryQuality("retry618.go", "", src)
	if !hasWarning(w, "no attempt cap") {
		t.Fatalf("expected unbounded-retry warning: mere mention of attemptLog is not a cap, got %v", w)
	}
	if !hasWarning(w, "no backoff delay") {
		t.Fatalf("expected missing-backoff warning, got %v", w)
	}
}

// countryCode contains "count" — must not match isAttemptCounterName.
func TestIssue618_CountryCodeNotCounterName(t *testing.T) {
	if isAttemptCounterName("countryCode") {
		t.Fatal("countryCode must not be treated as an attempt counter name")
	}
	if isAttemptCounterName("attemptLog") {
		t.Fatal("attemptLog must not be treated as an attempt counter name")
	}
	if !isAttemptCounterName("attempt") || !isAttemptCounterName("retries") || !isAttemptCounterName("count") {
		t.Fatal("genuine counter names must still match")
	}
	if !isAttemptCounterName("attemptCount") {
		t.Fatal("attemptCount (compound) must still match")
	}
}

// Defect 3: `<-x.C` where x is NOT a time.NewTimer/NewTicker result (e.g. an
// event-subscription channel) is a hot loop, not a backoff.
func TestIssue618_DotCOnNonTimerNotBackoff(t *testing.T) {
	src := `package main
import "net/http"
type subscriber struct{ C chan int }
func worker(sub *subscriber) {
	for {
		ev := <-sub.C
		_ = ev
		_, err := http.Get("http://example.com")
		if err != nil {
			continue
		}
		break
	}
}`
	w := checkRetryQuality("retry618.go", "", src)
	if !hasWarning(w, "no backoff delay") {
		t.Fatalf("expected missing-backoff for non-timer .C receive, got %v", w)
	}
}

// Defect 3 counterpart: a genuine time.NewTimer result received via .C in a
// select IS a backoff (regression guard).
func TestIssue618_DotCOnRealTimerStillBackoff(t *testing.T) {
	src := `package main
import ("net/http"; "time")
func fetch(url string) {
	for {
		_, err := http.Get(url)
		if err != nil {
			timer := time.NewTimer(time.Second)
			<-timer.C
			continue
		}
		break
	}
}`
	w := checkRetryQuality("retry618.go", "", src)
	if hasWarning(w, "no backoff delay") {
		t.Fatalf("unexpected missing-backoff for genuine timer receive, got %v", w)
	}
}

// Defect 2 positive: a real comparison against a max in the body still
// exempts (regression guard, mirrors TestRetryQuality_UnboundedButHasCounterCheckInBody).
func TestIssue618_RealCounterComparisonStillExempts(t *testing.T) {
	src := `package main
import ("net/http"; "time")
var maxRetries = 3
func fetch(url string) {
	attempt := 0
	for {
		_, err := http.Get(url)
		if err != nil {
			if attempt >= maxRetries {
				return
			}
			attempt++
			time.Sleep(time.Second)
			continue
		}
		return
	}
}`
	w := checkRetryQuality("retry618.go", "", src)
	if hasWarning(w, "no attempt cap") {
		t.Fatalf("unexpected unbounded-retry warning with real counter comparison, got %v", w)
	}
}
