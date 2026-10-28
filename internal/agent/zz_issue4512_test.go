package agent

import (
	"regexp"
	"strings"
	"testing"
	"time"
)

// #4512: adopting the intent view at len(intentActive)>=2 used to fully
// swap in the intent subset, letting a thin pair of weak (HitCount=1)
// rules displace every high-hit global rule from the prompt. The mixed
// quota keeps the intent preference (at most half the quota reserved for
// intent rules) while the global ranking fills the remaining slots.

func mkQuotaStore(t *testing.T) *RuleStore {
	t.Helper()
	return &RuleStore{path: t.TempDir(), maxRules: defaultMaxRules,
		regexCache: make(map[string]*regexp.Regexp)}
}

// Two weak intent rules must not displace the high-hit global rules.
func TestTopRulesForPromptFiltered_ThinIntentKeepsGlobalFill(t *testing.T) {
	rs := mkQuotaStore(t)
	now := time.Now()
	rs.AddRule(mkIntentRule("repro-script-first when debugging flaky timing", "bugfix", 1, now))
	rs.AddRule(mkIntentRule("pin the exact fixture id before rewriting migrations", "bugfix", 1, now))
	rs.AddRule(mkIntentRule("global-build-strong", "build", 20, now))
	rs.AddRule(mkIntentRule("global-test-strong", "test", 15, now))

	got := rs.TopRulesForPromptFiltered(5, "bugfix")
	if !strings.Contains(got, "repro-script-first") || !strings.Contains(got, "pin the exact fixture") {
		t.Fatalf("intent rules must keep their reserved quota, got:\n%s", got)
	}
	if !strings.Contains(got, "global-build-strong") || !strings.Contains(got, "global-test-strong") {
		t.Fatalf("high-hit global rules must not be displaced by a thin intent subset (#4512), got:\n%s", got)
	}
}

// Intent rules cap at half the quota: 4 intent rules with maxRules=4 keep
// only 2 intent slots, the rest go to the global ranking.
func TestTopRulesForPromptFiltered_IntentQuotaCappedAtHalf(t *testing.T) {
	rs := mkQuotaStore(t)
	now := time.Now()
	rs.AddRule(mkIntentRule("intent-strong-1", "bugfix", 9, now))
	rs.AddRule(mkIntentRule("intent-strong-2", "bugfix", 8, now))
	rs.AddRule(mkIntentRule("intent-weak-3", "bugfix", 2, now))
	rs.AddRule(mkIntentRule("intent-weak-4", "bugfix", 1, now))
	rs.AddRule(mkIntentRule("global-mid", "build", 5, now))

	got := rs.TopRulesForPromptFiltered(4, "bugfix")
	if !strings.Contains(got, "intent-strong-1") || !strings.Contains(got, "intent-strong-2") {
		t.Fatalf("top intent rules must stay, got:\n%s", got)
	}
	if !strings.Contains(got, "global-mid") {
		t.Fatalf("global ranking must fill the non-reserved slots, got:\n%s", got)
	}
	if strings.Contains(got, "intent-weak-4") {
		t.Fatalf("lowest-ranked rules must be truncated at maxRules, got:\n%s", got)
	}
}

// Unbounded maxRules keeps every rule (intent first, then global fill).
func TestTopRulesForPromptFiltered_UnboundedKeepsAll(t *testing.T) {
	rs := mkQuotaStore(t)
	now := time.Now()
	rs.AddRule(mkIntentRule("u-intent-1", "bugfix", 1, now))
	rs.AddRule(mkIntentRule("u-intent-2", "bugfix", 1, now))
	rs.AddRule(mkIntentRule("u-global-1", "build", 3, now))

	got := rs.TopRulesForPromptFiltered(0, "bugfix")
	for _, want := range []string{"u-intent-1", "u-intent-2", "u-global-1"} {
		if !strings.Contains(got, want) {
			t.Fatalf("unbounded view must keep %q, got:\n%s", want, got)
		}
	}
	if strings.Index(got, "u-intent-1") > strings.Index(got, "u-global-1") {
		t.Fatalf("intent rules must lead the mixed view, got:\n%s", got)
	}
}
