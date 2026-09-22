package permission

import (
	"encoding/json"
	"reflect"
	"testing"
)

func TestLearnedRulesNilAndEmpty(t *testing.T) {
	var nilMem *ApprovalMemory
	if got := nilMem.LearnedRules(); got != nil {
		t.Errorf("nil memory: expected nil, got %v", got)
	}
	am := NewApprovalMemory()
	if got := am.LearnedRules(); len(got) != 0 {
		t.Errorf("fresh memory: expected 0 rules, got %d", len(got))
	}
}

func TestLearnedRulesSnapshotAndOrdering(t *testing.T) {
	am := NewApprovalMemory()
	edit := json.RawMessage(`{"file_path":"src/foo.go"}`)
	cmd := json.RawMessage(`{"command":"go test ./..."}`)
	for i := 0; i < autoApproveThreshold; i++ {
		am.RecordApproval("edit_file", edit)
	}
	am.RecordApproval("run_command", cmd) // streak 1, not yet auto-approved

	rules := am.LearnedRules()
	if len(rules) != 2 {
		t.Fatalf("expected 2 rules, got %d", len(rules))
	}
	if !rules[0].AutoApproved {
		t.Errorf("auto-approved key should sort first, got %+v", rules[0])
	}
	want := LearnedRule{Key: "edit_file:src/*.go", Consecutive: autoApproveThreshold, AutoApproved: true}
	if !reflect.DeepEqual(rules[0], want) {
		t.Errorf("snapshot drift: got %+v, want %+v", rules[0], want)
	}
	if rules[1].AutoApproved || rules[1].Consecutive != 1 {
		t.Errorf("streak rule wrong: %+v", rules[1])
	}

	// A deny resets only the denied key's streak.
	am.RecordDeny("run_command", cmd)
	rules = am.LearnedRules()
	if rules[1].Consecutive != 0 {
		t.Errorf("deny should reset streak, got %+v", rules[1])
	}
	if !rules[0].AutoApproved {
		t.Errorf("deny must not clear the other key's learned approval")
	}
}
