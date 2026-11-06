package agent

import (
	"strings"
	"testing"
	"time"
)

// Issue #5118: trajectory learnings (traj_intel.go extractInsights) and run
// insights (reflection.go GenerateInsights) persisted the raw user prompt
// with no redaction and no env gate - unlike the training-export pipeline
// fixed in #5111. A secret pasted in the first prompt landed in plaintext in
// .ggcode/trajectory-learnings.jsonl (including the GLOBAL cross-project
// store re-injected by RenderPromptSection) and in TUI run-insights memory.
// This pin freezes the fix: both carriers must pass through redactTrainText.
func TestIssue5118TrajInsightTaskRedacted(t *testing.T) {
	key := "sk-ant-api03-THISISATESTKEY1234567890"
	stats := &RunStats{
		Iterations:  5,
		UserPrompt:  "我的 Anthropic key 是 " + key + "，请写进 .env 并测试",
		Success:     true,
		ToolCalls:   map[string]int{"edit_file": 2},
		FilesEdited: []string{"a.go"},
		Duration:    3 * time.Second,
	}

	s := newTrajIntelState()
	learnings := s.extractInsights(stats)
	if len(learnings) == 0 {
		t.Fatal("expected at least one learning from a successful 5-iteration run")
	}
	for i, l := range learnings {
		if strings.Contains(l.Task, key) {
			t.Errorf("learning[%d].Task must not contain the raw secret: %q", i, l.Task)
		}
		if strings.Contains(l.Task, "sk-ant-api03-THISIS") {
			t.Errorf("learning[%d].Task must not leak even a partial key fragment: %q", i, l.Task)
		}
		if !strings.Contains(l.Task, "[REDACTED:") {
			t.Errorf("learning[%d].Task must carry the redaction marker: %q", i, l.Task)
		}
	}
}

// Boundary order pin: redaction must happen BEFORE the 120-rune truncation.
// A key straddling the boundary would otherwise survive as a truncated,
// unmatchable fragment under a truncate-then-redact implementation.
func TestIssue5118TrajInsightBoundaryNoPartialLeak(t *testing.T) {
	key := "sk-ant-api03-BOUNDARYTESTKEY1234567890abcdef"
	// 110 leading runes so the key starts before the 120-rune cut and
	// extends well past it.
	pad := strings.Repeat("前置说明文字", 22) // 110 runes
	stats := &RunStats{
		Iterations:  5,
		UserPrompt:  pad + " " + key,
		Success:     true,
		ToolCalls:   map[string]int{"edit_file": 2},
		FilesEdited: []string{"a.go"},
		Duration:    3 * time.Second,
	}

	s := newTrajIntelState()
	learnings := s.extractInsights(stats)
	if len(learnings) == 0 {
		t.Fatal("expected at least one learning")
	}
	for i, l := range learnings {
		if strings.Contains(l.Task, "sk-ant-api03-BOUND") {
			t.Errorf("learning[%d].Task leaked a boundary-straddling key fragment: %q", i, l.Task)
		}
	}
}

func TestIssue5118GenerateInsightsTaskRedacted(t *testing.T) {
	key := "sk-proj-INSIGHTSTESTKEY1234567890abcd"
	stats := RunStats{
		Iterations: 5,
		UserPrompt: "use this key " + key + " for the API",
		Success:    true,
		ToolCalls:  map[string]int{"read_file": 3},
		Duration:   2 * time.Second,
	}

	out := GenerateInsights(stats)
	if out == "" {
		t.Fatal("expected non-empty insights")
	}
	if strings.Contains(out, key) {
		t.Errorf("GenerateInsights must not contain the raw secret")
	}
	if !strings.Contains(out, "[REDACTED:") {
		t.Errorf("GenerateInsights Task line must be redacted")
	}
}
