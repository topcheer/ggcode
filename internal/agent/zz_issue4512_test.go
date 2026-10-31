package agent

import (
	"strings"
	"testing"
)

// #4512: the intent view must not let two weak same-intent rules evict every
// high-hit global rule from the prompt - it is a quota mix now.
// (AddRule stamps LastSeen=now and keeps given HitCount.)

func mk4512(rule string, taskType string, hits int) Rule {
	return Rule{Rule: rule, TaskType: taskType, HitCount: hits, Category: "tool"}
}

// Thin intent pool (2 single-hit rules) + rich global pool: the prompt must
// keep global high-hit rules visible (mix), not the 2 intent rules alone.
func TestIssue4512_ThinIntentMixesWithGlobal(t *testing.T) {
	rs := NewRuleStore(t.TempDir())
	rs.AddRule(mk4512("intent-weak-one", "debug", 1))
	rs.AddRule(mk4512("intent-weak-two", "debug", 1))
	rs.AddRule(mk4512("global-strong", "", 50)) // UNTAGGED = universal rule
	out := rs.TopRulesForPromptFiltered(5, "debug")
	if !strings.Contains(out, "intent-weak-one") {
		t.Fatalf("intent rules must lead the mix:\n%s", out)
	}
	if !strings.Contains(out, "global-strong") {
		t.Fatalf("#4512 regression: high-hit global rule evicted by two weak intent rules:\n%s", out)
	}
}

// Rich intent pool: intent rules lead, the global best complements.
func TestIssue4512_RichIntentLeadsGlobalComplements(t *testing.T) {
	rs := NewRuleStore(t.TempDir())
	for i := 0; i < 4; i++ {
		rs.AddRule(mk4512("intent-"+string(rune('a'+i)), "debug", 3))
	}
	rs.AddRule(mk4512("global-top", "", 99)) // universal
	out := rs.TopRulesForPromptFiltered(5, "debug")
	for i := 0; i < 4; i++ {
		if !strings.Contains(out, "intent-"+string(rune('a'+i))) {
			t.Fatalf("intent rule intent-%c missing:\n%s", rune('a'+i), out)
		}
	}
	if !strings.Contains(out, "global-top") {
		t.Fatalf("complement slot must carry the global best:\n%s", out)
	}
}

// Regression: single intent rule falls back to the global ranking; the
// no-intent path is untouched.
func TestIssue4512_NoIntentAndSingleRuleUnchanged(t *testing.T) {
	rs := NewRuleStore(t.TempDir())
	rs.AddRule(mk4512("only-one-intent", "debug", 1))
	rs.AddRule(mk4512("global-strong", "", 50))
	if out := rs.TopRulesForPromptFiltered(5, "debug"); !strings.Contains(out, "global-strong") {
		t.Fatalf("single intent rule must fall back to global ranking:\n%s", out)
	}
	if out := rs.TopRulesForPrompt(5); !strings.Contains(out, "global-strong") {
		t.Fatalf("no-intent path unchanged:\n%s", out)
	}
}
