package agent

import (
	"regexp"
	"testing"
	"time"
)

func userEditRuleFor(base string, turns int) Rule {
	return Rule{
		ID:           "ue_" + base,
		Category:     "convention",
		Rule:         "The user manually adjusts agent output in " + base + " (observed " + time.Now().Format("1504") + " times across " + string(rune('0'+turns)) + " turns): re-read the file before editing it to avoid overwriting their changes",
		MatchPattern: "",
		ToolPattern:  regexp.QuoteMeta(base),
		Source:       ruleSourceUserEdit,
		HitCount:     1,
		LastSeen:     time.Now(),
		CreatedAt:    time.Now(),
	}
}

func TestAddRule_DistinctUserEditFilesCoexist(t *testing.T) {
	rs := NewRuleStore(t.TempDir())
	rs.AddRule(userEditRuleFor("trial_fork.go", 2))
	rs.AddRule(userEditRuleFor("agent.go", 2))

	rules := rs.Rules()
	var tf, ag *Rule
	for i := range rules {
		if rules[i].ToolPattern == "trial_fork\\.go" {
			tf = &rules[i]
		}
		if rules[i].ToolPattern == "agent\\.go" {
			ag = &rules[i]
		}
	}
	if tf == nil || ag == nil {
		t.Fatalf("expected both files' user_edit rules to coexist, got %d rules", len(rules))
	}
	if tf.HitCount != 1 || ag.HitCount != 1 {
		t.Errorf("HitCount must not cross-accumulate: trial_fork=%d agent=%d", tf.HitCount, ag.HitCount)
	}
}

func TestDeduplicateRules_PreservesDistinctUserEditFiles(t *testing.T) {
	rs := NewRuleStore(t.TempDir())
	rs.AddRule(userEditRuleFor("user_edit_ratchet_test.go", 2))
	rs.AddRule(userEditRuleFor("action_annihilate.go", 3))
	if merged := rs.DeduplicateRules(); merged != 0 {
		t.Fatalf("dedup merged %d cross-file user_edit rules; must be 0", merged)
	}
	if got := len(rs.Rules()); got != 2 {
		t.Errorf("after dedup want 2 rules, got %d", got)
	}
}

func TestAddRule_SameFileUserEditStillMerges(t *testing.T) {
	rs := NewRuleStore(t.TempDir())
	r1 := userEditRuleFor("same.go", 2)
	r1.ID = "ue_same_1"
	r2 := userEditRuleFor("same.go", 3)
	r2.ID = "ue_same_2"
	rs.AddRule(r1)
	rs.AddRule(r2)
	if got := len(rs.Rules()); got != 1 {
		t.Errorf("same-file re-add must merge into 1 rule, got %d", got)
	}
}
