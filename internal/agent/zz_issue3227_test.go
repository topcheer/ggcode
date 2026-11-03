package agent

import (
	"path/filepath"
	"regexp"
	"sync"
	"testing"
)

func TestIssue3227_SingletonIdentity(t *testing.T) {
	a := &Agent{}
	a.SetWorkingDir(t.TempDir())

	rs1 := a.getRuleStore()
	rs2 := a.getRuleStore()
	if rs1 == nil || rs1 != rs2 {
		t.Fatalf("getRuleStore must return the SAME non-nil instance: %p vs %p", rs1, rs2)
	}
	var wg sync.WaitGroup
	pointers := make(chan *RuleStore, 8)
	for i := 0; i < 8; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			pointers <- a.getRuleStore()
		}()
	}
	wg.Wait()
	close(pointers)
	for rs := range pointers {
		if rs != rs1 {
			t.Fatal("concurrent getRuleStore returned a different instance")
		}
	}
}

func TestIssue3227_RetireDoesNotEraseLearnedRules(t *testing.T) {
	dir := t.TempDir()
	a := &Agent{}
	a.SetWorkingDir(dir)
	rs := a.getRuleStore()

	rs.AddRule(Rule{ID: "err-1", Category: "build", Rule: "run make lint", MatchPattern: "lint failed"})
	rs.AddRule(Rule{ID: "ue-1", Category: "convention", Rule: "reread before edit", MatchPattern: "reread-before-edit", Source: ruleSourceUserEdit, ToolPattern: regexp.QuoteMeta("edit_file")})
	if err := rs.save(); err != nil {
		t.Fatal(err)
	}

	if n := rs.RemoveUserEditRules("edit_file"); n != 1 {
		t.Fatalf("expected 1 retired user_edit rule, got %d", n)
	}

	fresh := NewRuleStore(dir)
	patterns := map[string]bool{}
	for _, r := range fresh.Rules() {
		patterns[r.MatchPattern] = true
	}
	if !patterns["lint failed"] {
		t.Fatal("error-driven rule was erased by the user_edit retire - lost-update regression (#3227)")
	}
	if patterns["reread-before-edit"] {
		t.Fatal("user_edit rule should have been retired")
	}
}

func TestIssue3227_FrozenInstanceLostUpdateClass(t *testing.T) {
	dir := t.TempDir()
	frozen := NewRuleStore(dir)
	frozen.Rules()

	writer := NewRuleStore(dir)
	writer.AddRule(Rule{ID: "w-1", Category: "build", Rule: "learned elsewhere"})
	if err := writer.save(); err != nil {
		t.Fatal(err)
	}

	frozen.AddRule(Rule{ID: "f-1", Category: "build", Rule: "frozen instance rule"})
	if err := frozen.save(); err != nil {
		t.Fatal(err)
	}
	disk := NewRuleStore(dir)
	ids := map[string]bool{}
	for _, r := range disk.Rules() {
		ids[r.ID] = true
	}
	if ids["w-1"] {
		t.Fatal("class documentation probe: expected the frozen-instance overwrite to erase w-1; if this fails the storage layer changed - re-check the #3227 fix assumptions")
	}
	_ = filepath.Separator
}
