package agent

import (
	"regexp"
	"strings"
	"testing"
	"time"
)

// mkIntentRule builds a rule with hit history for injection tests.
func mkIntentRule(rule, taskType string, hits int, lastSeen time.Time) Rule {
	return Rule{
		Category: "convention", Rule: rule, ToolPattern: "x" + rule,
		HitCount: hits, LastSeen: lastSeen, CreatedAt: lastSeen, TaskType: taskType,
	}
}

// TestTopRulesForPromptFiltered_PrefersSameIntent verifies the core P1
// behavior: when at least 2 rules share the run's task type, injection
// is restricted to them; other-type rules no longer consume slots.
func TestTopRulesForPromptFiltered_PrefersSameIntent(t *testing.T) {
	rs := &RuleStore{path: t.TempDir(), maxRules: defaultMaxRules,
		regexCache: make(map[string]*regexp.Regexp)}
	now := time.Now()
	rs.AddRule(mkIntentRule("always run gofmt after editing files", "bugfix", 5, now))
	rs.AddRule(mkIntentRule("verify the exact error line before rewriting", "bugfix", 3, now))
	rs.AddRule(mkIntentRule("release-lesson", "build", 9, now))
	rs.AddRule(mkIntentRule("test-lesson", "test", 7, now))

	got := rs.TopRulesForPromptFiltered(5, "bugfix")
	if !strings.Contains(got, "always run gofmt") || !strings.Contains(got, "verify the exact error line") {
		t.Fatalf("same-intent rules must inject, got:\n%s", got)
	}
	if strings.Contains(got, "release-lesson") || strings.Contains(got, "test-lesson") {
		t.Fatalf("other-intent rules must not pollute bugfix injection, got:\n%s", got)
	}
}

// TestTopRulesForPromptFiltered_ThinSubsetFallsBack: a single same-intent
// rule must not starve injection - the global ranking takes over.
func TestTopRulesForPromptFiltered_ThinSubsetFallsBack(t *testing.T) {
	rs := &RuleStore{path: t.TempDir(), maxRules: defaultMaxRules,
		regexCache: make(map[string]*regexp.Regexp)}
	now := time.Now()
	rs.AddRule(mkIntentRule("lone-bugfix", "bugfix", 2, now))
	rs.AddRule(mkIntentRule("build-lesson", "build", 4, now))
	rs.AddRule(mkIntentRule("test-lesson", "test", 4, now))

	got := rs.TopRulesForPromptFiltered(5, "bugfix")
	if !strings.Contains(got, "lone-bugfix") || !strings.Contains(got, "build-lesson") {
		t.Fatalf("thin intent subset must fall back to global top-N, got:\n%s", got)
	}
}

// TestTopRulesForPromptFiltered_LegacyUntaggedStayGlobal: rules persisted
// before this change (empty TaskType) remain eligible in every intent.
func TestTopRulesForPromptFiltered_LegacyUntaggedStayGlobal(t *testing.T) {
	rs := &RuleStore{path: t.TempDir(), maxRules: defaultMaxRules,
		regexCache: make(map[string]*regexp.Regexp)}
	now := time.Now()
	rs.AddRule(mkIntentRule("legacy-untagged", "", 6, now))
	rs.AddRule(mkIntentRule("another-legacy", "", 5, now))

	got := rs.TopRulesForPromptFiltered(5, "refactor")
	if !strings.Contains(got, "legacy-untagged") {
		t.Fatalf("legacy untagged rules must stay injectable, got:\n%s", got)
	}
}

// TestTopRulesForPromptFiltered_OtherIntentDisablesFilter: classifyTaskType
// returns "other" for unmatched prompts; filtering must be a no-op there.
func TestTopRulesForPromptFiltered_OtherIntentDisablesFilter(t *testing.T) {
	rs := &RuleStore{path: t.TempDir(), maxRules: defaultMaxRules,
		regexCache: make(map[string]*regexp.Regexp)}
	now := time.Now()
	rs.AddRule(mkIntentRule("bugfix-only", "bugfix", 2, now))

	if got := rs.TopRulesForPromptFiltered(5, "other"); !strings.Contains(got, "bugfix-only") {
		t.Fatalf(`intent "other" must disable filtering, got:\n%s`, got)
	}
}

// TestAddRuleInheritsRunIntent: rules learned mid-run (error ratchet,
// user-edit ratchet) pick up the run's intent tag automatically.
func TestAddRuleInheritsRunIntent(t *testing.T) {
	rs := &RuleStore{path: t.TempDir(), maxRules: defaultMaxRules,
		regexCache: make(map[string]*regexp.Regexp)}
	rs.SetRunIntent("bugfix")
	rs.AddRule(mkIntentRule("fresh-lesson", "", 1, time.Now()))

	rules := rs.Rules()
	if len(rules) != 1 || rules[0].TaskType != "bugfix" {
		t.Fatalf("AddRule must inherit runIntent, got %+v", rules)
	}

	// Explicit TaskType is never overwritten.
	rs.AddRule(mkIntentRule("explicit", "test", 1, time.Now()))
	rules = rs.Rules()
	found := false
	for _, r := range rules {
		if r.Rule == "explicit" {
			found = true
			if r.TaskType != "test" {
				t.Fatalf("explicit TaskType must be preserved, got %q", r.TaskType)
			}
		}
	}
	if !found {
		t.Fatal("explicit rule missing from store")
	}

	// Intent "other" must not tag.
	rs.SetRunIntent("other")
	rs.AddRule(mkIntentRule("unclassified", "", 1, time.Now()))
	rules = rs.Rules()
	for _, r := range rules {
		if r.Rule == "unclassified" && r.TaskType != "" {
			t.Fatalf(`intent "other" must not tag rules, got %q`, r.TaskType)
		}
	}
}
