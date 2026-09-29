package agent

// Regression tests for #2879: prefix phrase patterns ("sort the", "order by",
// "rename the", "improve the", "fix the bug") fired deterministically on
// sentences that were already disambiguated, injecting contradictory
// clarification guidance as a user message AND burning the fired-once budget
// so later genuine ambiguity went undetected.

import (
	"strings"
	"testing"
)

func mk2879Agent() *Agent { return &Agent{ambiguityPoint: newAmbiguityPointState()} }

// The issue's evidence table: each sentence fully answers the question the
// suggestion would ask, so no guidance may fire.
func Test2879DisambiguatedPromptsNotFlagged(t *testing.T) {
	for _, prompt := range []string{
		"improve the latency of the hot path by adding a cache",
		"sort the users by last_login descending",
		"order by created_at desc, id asc",
		"rename the file to config.yaml",
		"fix the bug where the parser panics on malformed input",
		"sort the articles newest first",
		"simplify the handler by inlining the validation helper",
		"improve the readability of this function",
	} {
		if w := mk2879Agent().checkAmbiguityPoints(prompt); w != "" {
			t.Errorf("already-disambiguated prompt %q must not fire, got: %s", prompt, w)
		}
	}
}

// Positive controls: the same prefixes WITHOUT a disambiguation signal must
// keep firing (the patterns stay live; only answered questions are muted).
func Test2879GenuineAmbiguityStillFires(t *testing.T) {
	for _, prompt := range []string{
		"sort the list",
		"order by name",
		"rename the function to something more descriptive", // placeholder target
		"improve it",
		"fix the bug",
		"simplify the validation logic",
		"improve the error handling in this module",
		"sort the results by name", // key given, direction still open (pinned)
	} {
		if w := mk2879Agent().checkAmbiguityPoints(prompt); w == "" {
			t.Errorf("genuinely ambiguous prompt %q must still fire", prompt)
		}
	}
}

// The fired-once budget must not be consumed by a suppressed signal: after a
// disambiguated prompt produced no guidance on the SAME agent, a later
// genuinely ambiguous prompt in the same run still gets detected.
func Test2879SuppressedSignalDoesNotBurnFiredOnceBudget(t *testing.T) {
	a := mk2879Agent()
	if w := a.checkAmbiguityPoints("sort the users by last_login descending"); w != "" {
		t.Fatalf("disambiguated prompt must not fire, got: %s", w)
	}
	if w := a.checkAmbiguityPoints("sort the list"); w == "" {
		t.Fatal("suppressed signal must not consume the fired-once budget")
	}
}

// "fix the bug where the parser panics on empty input": the direction signal
// ("fix the bug") must be gone; the "empty input" edge-case signal is a
// legitimate separate ambiguity (error vs empty result vs zero value) and may
// stay.
func Test2879FixBugClauseSuppressesDirectionOnly(t *testing.T) {
	w := mk2879Agent().checkAmbiguityPoints("fix the bug where the parser panics on empty input")
	if w != "" && strings.Contains(w, `"fix the bug"`) {
		t.Errorf("fix-the-bug direction signal must be suppressed by the where-clause, got: %s", w)
	}
}

// CJK parity: explicit direction/target in the sentence suppresses; bare
// vague forms keep firing (pins stay green alongside checks_1521_test.go).
func Test2879CJKDisambiguation(t *testing.T) {
	for _, prompt := range []string{
		"帮我排序，按时间降序",
		"帮我排序，从新到旧",
		"把这个函数重命名一下，改名叫 handleReq",
		"优化一下这个函数的延迟",
	} {
		if w := mk2879Agent().checkAmbiguityPoints(prompt); w != "" {
			t.Errorf("CJK disambiguated prompt %q must not fire, got: %s", prompt, w)
		}
	}
	for _, prompt := range []string{
		"帮我排序",
		"优化一下这个函数的性能", // broad metric 性能 stays detected (pinned upstream)
	} {
		if w := mk2879Agent().checkAmbiguityPoints(prompt); w == "" {
			t.Errorf("CJK vague prompt %q must still fire", prompt)
		}
	}
}
