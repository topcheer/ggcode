package agent

import (
	"encoding/json"
	"testing"
)

func TestToolRedundancy_Reset(t *testing.T) {
	r := newToolRedundancyAnalyzer()
	args, _ := json.Marshal(map[string]string{"pattern": "foo"})
	r.recordCall("grep", args)
	r.recordCall("grep", args)
	if len(r.counts) != 1 {
		t.Fatalf("expected 1 fingerprint, got %d", len(r.counts))
	}
	r.reset()
	if len(r.counts) != 0 {
		t.Fatalf("expected 0 fingerprints after reset, got %d", len(r.counts))
	}
	if r.warnings != 0 {
		t.Fatalf("expected 0 warnings after reset, got %d", r.warnings)
	}
}

func TestToolRedundancy_ScatteredDup(t *testing.T) {
	r := newToolRedundancyAnalyzer()
	args, _ := json.Marshal(map[string]string{"pattern": "foo"})

	// Call 1: no warning
	h1 := r.recordCall("grep", args)
	if h1 != "" {
		t.Fatalf("expected no hint on call 1, got: %s", h1)
	}

	// Different tool call in between (loop_detect would reset, this should not)
	otherArgs, _ := json.Marshal(map[string]string{"path": "main.go"})
	r.recordCall("read_file", otherArgs)

	// Call 2: no warning yet
	h2 := r.recordCall("grep", args)
	if h2 != "" {
		t.Fatalf("expected no hint on call 2, got: %s", h2)
	}

	// Call 3: should warn
	h3 := r.recordCall("grep", args)
	if h3 == "" {
		t.Fatal("expected hint on call 3 (scattered duplicate threshold)")
	}
}

func TestToolRedundancy_DifferentArgs(t *testing.T) {
	r := newToolRedundancyAnalyzer()
	args1, _ := json.Marshal(map[string]string{"pattern": "foo"})
	args2, _ := json.Marshal(map[string]string{"pattern": "bar"})

	// Call same args 3 times -> warning on 3rd
	for i := 0; i < 3; i++ {
		h := r.recordCall("grep", args1)
		if i == 2 && h == "" {
			t.Fatal("expected hint on 3rd identical call")
		}
	}

	// Call with different args twice -> no warning (under threshold)
	for i := 0; i < 2; i++ {
		h2 := r.recordCall("grep", args2)
		if h2 != "" {
			t.Fatalf("expected no hint for different args under threshold, got: %s", h2)
		}
	}
}

func TestToolRedundancy_MaxWarnings(t *testing.T) {
	r := newToolRedundancyAnalyzer()
	args1, _ := json.Marshal(map[string]string{"pattern": "aaa"})
	args2, _ := json.Marshal(map[string]string{"pattern": "bbb"})

	// First pattern: trigger warning at call 3
	for i := 0; i < 3; i++ {
		r.recordCall("grep", args1)
	}

	// Second pattern: trigger warning at call 3
	for i := 0; i < 3; i++ {
		r.recordCall("grep", args2)
	}

	if r.warnings != 2 {
		t.Fatalf("expected 2 warnings, got %d", r.warnings)
	}

	// Third pattern: should be suppressed (max warnings reached)
	args3, _ := json.Marshal(map[string]string{"pattern": "ccc"})
	for i := 0; i < 5; i++ {
		h := r.recordCall("grep", args3)
		if h != "" {
			t.Fatalf("expected no hint after max warnings, got: %s", h)
		}
	}
}

func TestToolRedundancy_Escalation(t *testing.T) {
	r := newToolRedundancyAnalyzer()
	args, _ := json.Marshal(map[string]string{"pattern": "escalation_test"})

	var warnings []string
	for i := 0; i < 6; i++ {
		h := r.recordCall("grep", args)
		if h != "" {
			warnings = append(warnings, h)
		}
	}

	// Should have 2 warnings: at 3 and at 6
	if len(warnings) != 2 {
		t.Fatalf("expected 2 warnings (threshold + escalation), got %d", len(warnings))
	}
}

func TestToolRedundancy_Summary(t *testing.T) {
	r := newToolRedundancyAnalyzer()
	args, _ := json.Marshal(map[string]string{"pattern": "summary_test"})

	// Single call - summary should be empty
	r.recordCall("grep", args)
	if s := r.summary(); s != "" {
		t.Fatalf("expected empty summary for single call, got: %s", s)
	}

	// Multiple calls - summary should show count
	r.recordCall("grep", args)
	r.recordCall("grep", args)
	s := r.summary()
	if s == "" {
		t.Fatal("expected non-empty summary for repeated calls")
	}
}

func TestToolRedundancy_ConsecutiveDup(t *testing.T) {
	r := newToolRedundancyAnalyzer()
	args, _ := json.Marshal(map[string]string{"pattern": "consecutive"})

	// Consecutive calls should still be tracked (complementary to loop_detect)
	for i := 0; i < 3; i++ {
		h := r.recordCall("grep", args)
		if i == 2 && h == "" {
			t.Fatal("expected hint on 3rd consecutive call")
		}
	}
}
