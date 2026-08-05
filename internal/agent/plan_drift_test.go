package agent

import (
	"encoding/json"
	"testing"
)

func TestCapturePlan_BulletItems(t *testing.T) {
	s := newPlanDriftState()
	plan := `## Implementation Plan

- Add ` + "`plan_drift.go`" + ` to internal/agent/
- Wire planDrift into agent.go struct
- Write tests for plan drift detection
- Update docs/guide/ with plan drift info`

	s.capturePlan(plan)

	if !s.captured {
		t.Fatal("expected captured=true")
	}
	if len(s.items) != 5 {
		t.Fatalf("expected 5 items (1 heading + 4 bullets), got %d", len(s.items))
	}
	// First item should reference plan_drift.go
	found := false
	for _, item := range s.items {
		for _, kw := range item.Keywords {
			if kw == "plan_drift.go" {
				found = true
			}
		}
	}
	if !found {
		t.Error("expected to find 'plan_drift.go' keyword")
	}
}

func TestCapturePlan_NumberedItems(t *testing.T) {
	s := newPlanDriftState()
	plan := `1. Create ParseSpec in specparser.go
2. Add ValidateSpec function
3. Wire SpecToTasks converter`

	s.capturePlan(plan)

	if len(s.items) != 3 {
		t.Fatalf("expected 3 items, got %d", len(s.items))
	}
}

func TestCapturePlan_HeadingItems(t *testing.T) {
	s := newPlanDriftState()
	plan := `## Phase 1: Core Implementation
Content...
## Phase 2: Testing
Content...
## Phase 3: Documentation`

	s.capturePlan(plan)

	if len(s.items) != 3 {
		t.Fatalf("expected 3 heading items, got %d", len(s.items))
	}
}

func TestCapturePlan_SkipsNonActionable(t *testing.T) {
	s := newPlanDriftState()
	plan := `- Note: this is just context
- Summary: overview of work
- Estimated time: 2 hours`

	s.capturePlan(plan)

	if len(s.items) != 0 {
		t.Fatalf("expected 0 actionable items, got %d", len(s.items))
	}
}

func TestCapturePlan_Empty(t *testing.T) {
	s := newPlanDriftState()
	s.capturePlan("")
	if s.captured {
		t.Error("empty plan should not set captured=true")
	}
}

func TestCheckPlanDrift_NoPlan(t *testing.T) {
	s := newPlanDriftState()
	stats := newRunStats("test")
	msg := s.checkPlanDrift(stats, "")
	if msg != "" {
		t.Error("expected empty message when no plan captured")
	}
}

func TestCheckPlanDrift_AlreadyFired(t *testing.T) {
	s := newPlanDriftState()
	s.captured = true
	s.fired = true
	s.items = []planItem{{Text: "test", Keywords: []string{"foo"}}}
	stats := newRunStats("test")
	msg := s.checkPlanDrift(stats, "")
	if msg != "" {
		t.Error("expected empty message when already fired")
	}
}

func TestCheckPlanDrift_AllAddressed(t *testing.T) {
	s := newPlanDriftState()
	s.captured = true
	s.items = []planItem{
		{Text: "Edit agent.go", Keywords: []string{"agent.go"}},
		{Text: "Create plan_drift.go", Keywords: []string{"plan_drift.go"}},
	}
	stats := newRunStats("test")
	stats.FilesEdited = []string{"internal/agent/agent.go", "internal/agent/plan_drift.go"}
	msg := s.checkPlanDrift(stats, "")
	if msg != "" {
		t.Error("expected empty message when all items addressed")
	}
}

func TestCheckPlanDrift_PartialDrift(t *testing.T) {
	s := newPlanDriftState()
	s.captured = true
	s.items = []planItem{
		{Text: "Edit agent.go", Keywords: []string{"agent.go"}},
		{Text: "Create new_file.go", Keywords: []string{"new_file.go"}},
		{Text: "Update config.yaml", Keywords: []string{"config.yaml"}},
	}
	stats := newRunStats("test")
	stats.FilesEdited = []string{"internal/agent/agent.go"}
	msg := s.checkPlanDrift(stats, "I edited agent.go")
	if msg == "" {
		t.Fatal("expected drift message for 2 unaddressed items")
	}
	if !containsPD(msg, "2 of 3") && !containsPD(msg, "3 of 3") {
		t.Errorf("expected count in message, got: %s", msg)
	}
}

func TestCheckPlanDrift_SingleItemNoFire(t *testing.T) {
	s := newPlanDriftState()
	s.captured = true
	s.items = []planItem{
		{Text: "Item 1 done", Keywords: []string{"done"}},
		{Text: "Item 2 missing", Keywords: []string{"missing"}},
	}
	stats := newRunStats("test")
	stats.FilesEdited = []string{"done.go"}
	msg := s.checkPlanDrift(stats, "done")
	// Only 1 of 2 unaddressed, ratio 0.5 - should fire since >= 2 items check is len<2 && ratio<0.5
	if msg == "" {
		// 1/2 = 0.5, and len(unaddressed)=1 < 2 && ratio=0.5 which is NOT < 0.5, so should fire
		t.Error("expected drift to fire for 1 of 2 (50% threshold met)")
	}
}

func TestExtractPlanFromArgs(t *testing.T) {
	args := json.RawMessage(`{"plan": "## Plan\n- Step 1\n- Step 2", "description": "test"}`)
	plan := extractPlanFromArgs(args)
	if plan == "" {
		t.Fatal("expected non-empty plan")
	}
	if !containsPD(plan, "Step 1") {
		t.Error("expected plan to contain 'Step 1'")
	}
}

func TestExtractPlanFromArgs_Invalid(t *testing.T) {
	args := json.RawMessage(`{invalid json}`)
	plan := extractPlanFromArgs(args)
	if plan != "" {
		t.Error("expected empty plan for invalid JSON")
	}
}

func TestPlanDriftReset(t *testing.T) {
	s := newPlanDriftState()
	s.captured = true
	s.fired = true
	s.items = []planItem{{Text: "test"}}
	s.reset()
	if s.captured || s.fired || len(s.items) != 0 {
		t.Error("reset did not clear state")
	}
}

func TestExtractKeywords(t *testing.T) {
	kw := extractKeywords("Edit `configLoader.go` and update Settings struct")
	found := false
	for _, k := range kw {
		if k == "configloader.go" {
			found = true
		}
	}
	if !found {
		t.Errorf("expected to find 'configloader.go' in keywords: %v", kw)
	}
}

func TestStripNumberedPrefix(t *testing.T) {
	tests := []struct {
		input string
		want  string
	}{
		{"1. Hello", "Hello"},
		{"2) World", "World"},
		{"10. Test item", "Test item"},
		{"No number", ""},
		{"1.", ""},
	}
	for _, tt := range tests {
		got := stripNumberedPrefix(tt.input)
		if got != tt.want {
			t.Errorf("stripNumberedPrefix(%q) = %q, want %q", tt.input, got, tt.want)
		}
	}
}

func containsPD(s, substr string) bool {
	return len(s) >= len(substr) && (s == substr || len(s) > 0 && containsStrPD(s, substr))
}

func containsStrPD(s, substr string) bool {
	for i := 0; i <= len(s)-len(substr); i++ {
		if s[i:i+len(substr)] == substr {
			return true
		}
	}
	return false
}
