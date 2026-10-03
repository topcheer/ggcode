package agent

// Regression probes for #3227: RuleStore instances each load() ONCE and
// save() overwrites the file with that frozen snapshot, so a long-lived
// cached instance (user-edit observer) silently erased everything the
// per-run instances learned - a cross-instance lost-update. The fix makes
// every in-process consumer share ONE store via getRuleStore().

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
	// Concurrent callers (asyncVerify runs off the main loop) must all see
	// one instance - the a.mu-guarded cache makes the pointer handoff safe.
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

	// Error-driven learning (runRatchet/generalize path) writes R1.
	rs.AddRule(Rule{ID: "err-1", Category: "build", Rule: "run make lint", MatchPattern: "lint failed"})
	rs.AddRule(Rule{ID: "ue-1", Category: "convention", Rule: "reread before edit", MatchPattern: "reread-before-edit", Source: ruleSourceUserEdit, ToolPattern: regexp.QuoteMeta("edit_file")})
	if err := rs.save(); err != nil {
		t.Fatal(err)
	}

	// r447 undo retire through the SAME store must drop only the user_edit
	// rule - the error-driven rule survives because there is a single
	// in-memory truth, not a frozen snapshot racing other writers.
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

// TestIssue3227_FrozenInstanceLostUpdateClass documents WHY the singleton
// is mandatory: two RAW NewRuleStore instances on the same file still
// exhibit the lost-update class (frozen load + full-overwrite save). No
// in-process call site may build its own store anymore (#3227).
func TestIssue3227_FrozenInstanceLostUpdateClass(t *testing.T) {
	dir := t.TempDir()
	frozen := NewRuleStore(dir)
	frozen.Rules() // trigger the one-shot load: snapshot is now empty

	writer := NewRuleStore(dir)
	writer.AddRule(Rule{ID: "w-1", Category: "build", Rule: "learned elsewhere"})
	if err := writer.save(); err != nil {
		t.Fatal(err)
	}

	// The frozen instance saves its empty-plus-own snapshot and erases w-1
	// from disk - exactly the #3227 report path.
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
