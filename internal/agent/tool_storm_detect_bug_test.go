package agent

import (
	"testing"
)

// TestToolStormBug_DiversityCeiling tests that the diversity threshold uses
// math.Ceil() instead of int() truncation.
//
// Fixed: Line 263 now uses int(math.Ceil(float64(len(s.window)) * 0.6)) which
// gives 3 for a 4-iteration window, matching the comment's intent.
//
// Impact: A pattern like read_file, grep, read_file, grep (2 distinct tools)
// should NOT trigger a storm warning — it's legitimate exploration, not a storm.
func TestToolStormBug_DiversityCeiling(t *testing.T) {
	s := newToolStormState()

	// 4 iterations with thin reasoning, but only 2 distinct tools
	// Pattern: read_file, grep, read_file, grep
	tools := []string{"read_file", "grep", "read_file", "grep"}
	for i, tool := range tools {
		s.recordReasoning("ok") // 2 chars, very thin
		s.recordToolCall(tool, i+1)
	}

	msg := s.maybeWarn()

	// Fixed behavior: minDistinct = ceil(4 * 0.6) = ceil(2.4) = 3
	// With 2 distinct tools, the check len(toolSet) >= 3 should fail
	// and we should NOT get a storm warning.
	if msg != "" {
		t.Fatalf("expected no storm warning for 2 distinct tools (ceil(4*0.6)=3), got: %s", msg)
	}

	t.Logf("PASS: No storm warning for 2-tool pattern (correct ceil behavior)")
}
