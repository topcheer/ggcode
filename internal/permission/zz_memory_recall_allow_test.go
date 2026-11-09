package permission

import (
	"encoding/json"
	"testing"
)

// TestMemoryRecallFastAllow pins the read-only memory_recall tool to the
// benign fast-Allow case in every mode (mirrors the save_memory pin in
// zz_issue4532_4534_test.go; memory_recall is read-only so it must never
// regress to default gating).
func TestMemoryRecallFastAllow(t *testing.T) {
	p := &ConfigPolicy{mode: SupervisedMode}
	if d, _ := p.Check("memory_recall", json.RawMessage(`{"query":"build","scope":"all"}`)); d != Allow {
		t.Fatalf("memory_recall must fast-Allow in supervised mode, got %v", d)
	}
	pBypass := &ConfigPolicy{mode: BypassMode}
	if d, _ := pBypass.Check("memory_recall", json.RawMessage(`{}`)); d != Allow {
		t.Fatalf("memory_recall must fast-Allow in bypass mode, got %v", d)
	}
	// An explicit user ask-pin still outranks the fast-Allow case.
	pAsk := &ConfigPolicy{mode: SupervisedMode, rules: map[string]Decision{"memory_recall": Ask}}
	if d, _ := pAsk.Check("memory_recall", json.RawMessage(`{}`)); d != Ask {
		t.Fatalf("explicit ask pin on memory_recall must be honored, got %v", d)
	}
}
