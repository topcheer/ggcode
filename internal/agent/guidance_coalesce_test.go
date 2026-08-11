package agent

import (
	"strings"
	"testing"
)

func TestExtractHintTag(t *testing.T) {
	tests := []struct {
		hint string
		want string
	}{
		{"[CRITICAL] Something bad happened", "CRITICAL"},
		{"[strategy-stagnation] web_search failed", "strategy-stagnation"},
		{"[Tool Call Storm] 4 consecutive calls", "Tool Call Storm"},
		{"No tag here", ""},
		{"[multi word tag with spaces] text", "multi word tag with spaces"},
		{"   [indented] tag", "indented"},
		{"", ""},
	}
	for _, tt := range tests {
		got := extractHintTag(tt.hint)
		if got != tt.want {
			t.Errorf("extractHintTag(%q) = %q, want %q", tt.hint, got, tt.want)
		}
	}
}

func TestIsCriticalTag(t *testing.T) {
	critical := []string{
		"CRITICAL", "PERMISSION", "IRREVERSIBLE", "DESTRUCTIVE",
		"SAFETY", "SECURITY", "BLOCKED", "FATAL",
		"pre-commit-build-gate", "hardcoded-secret",
		"path-traversal", "git-destructive",
	}
	for _, tag := range critical {
		if !isCriticalTag(tag) {
			t.Errorf("isCriticalTag(%q) = false, want true", tag)
		}
	}
	nonCritical := []string{
		"", "tool-call-storm", "analysis-paralysis", "verbosity-drift",
		"context-pressure", "tool-diversity", "scope-narrowness",
	}
	for _, tag := range nonCritical {
		if isCriticalTag(tag) {
			t.Errorf("isCriticalTag(%q) = true, want false", tag)
		}
	}
}

func TestCoalesceGuidance_DedupByTag(t *testing.T) {
	hints := []string{
		"[tool-storm] 4 consecutive calls",
		"[tool-storm] Another storm warning that should be deduped",
		"[verbosity-drift] Context is increasing",
	}
	result := coalesceGuidance(hints)
	if len(result) != 2 {
		t.Fatalf("expected 2 hints after dedup, got %d: %v", len(result), result)
	}
	// First occurrence is kept.
	if !strings.Contains(result[0], "4 consecutive calls") {
		t.Errorf("expected first occurrence to be kept, got: %s", result[0])
	}
}

func TestCoalesceGuidance_CapToMax(t *testing.T) {
	hints := []string{
		"[hint-1] first advisory",
		"[hint-2] second advisory",
		"[hint-3] third advisory",
		"[hint-4] fourth advisory",
		"[hint-5] fifth advisory",
		"[hint-6] sixth advisory",
	}
	result := coalesceGuidance(hints)
	// Should be capped to coalesceMaxHints (3) + 1 suppression summary.
	if len(result) != coalesceMaxHints+1 {
		t.Fatalf("expected %d results (capped + summary), got %d: %v", coalesceMaxHints+1, len(result), result)
	}
	// Last entry should be the suppression summary.
	if !strings.Contains(result[len(result)-1], "[guidance-coalesced]") {
		t.Errorf("expected last entry to be suppression summary, got: %s", result[len(result)-1])
	}
	if !strings.Contains(result[len(result)-1], "5 additional") {
		t.Errorf("expected summary to mention 3 suppressed, got: %s", result[len(result)-1])
	}
}

func TestCoalesceGuidance_PrioritizeCritical(t *testing.T) {
	hints := []string{
		"[hint-1] advisory one",
		"[hint-2] advisory two",
		"[hint-3] advisory three",
		"[CRITICAL] this must not be suppressed",
		"[hint-4] advisory four",
	}
	result := coalesceGuidance(hints)
	// Critical should be retained even when cap is exceeded.
	found := false
	for _, h := range result {
		if strings.Contains(h, "this must not be suppressed") {
			found = true
			break
		}
	}
	if !found {
		t.Errorf("critical hint was suppressed: %v", result)
	}
}

func TestCoalesceGuidance_AllCritical(t *testing.T) {
	hints := []string{
		"[CRITICAL] first critical",
		"[SAFETY] second safety",
		"[DESTRUCTIVE] third destructive",
		"[PERMISSION] fourth permission",
		"[BLOCKED] fifth blocked",
	}
	result := coalesceGuidance(hints)
	// Even with all critical, should be capped at coalesceMaxHints.
	if len(result) > coalesceMaxHints+1 {
		t.Fatalf("expected at most %d results, got %d: %v", coalesceMaxHints+1, len(result), result)
	}
}

func TestCoalesceGuidance_Empty(t *testing.T) {
	result := coalesceGuidance(nil)
	if len(result) != 0 {
		t.Errorf("expected empty result for nil input, got %v", result)
	}
}

func TestCoalesceGuidance_SingleHint(t *testing.T) {
	hints := []string{"[hint] just one"}
	result := coalesceGuidance(hints)
	if len(result) != 1 || result[0] != hints[0] {
		t.Errorf("expected single hint unchanged, got %v", result)
	}
}

func TestCoalesceGuidance_TwoHints(t *testing.T) {
	hints := []string{
		"[hint-a] first",
		"[hint-b] second",
	}
	result := coalesceGuidance(hints)
	if len(result) != 2 {
		t.Fatalf("expected 2 hints, got %d: %v", len(result), result)
	}
}

func TestCoalesceGuidance_SuppressionSummaryFormat(t *testing.T) {
	hints := []string{
		"[hint-1] advisory one",
		"[hint-2] advisory two",
		"[hint-3] advisory three",
		"[hint-4] advisory four",
		"[hint-5] advisory five",
	}
	result := coalesceGuidance(hints)
	summary := result[len(result)-1]
	if !strings.Contains(summary, "[guidance-coalesced]") {
		t.Errorf("summary missing tag: %s", summary)
	}
	if !strings.Contains(summary, "suppressed") {
		t.Errorf("summary missing 'suppressed': %s", summary)
	}
	// Should list the suppressed tag names.
	if !strings.Contains(summary, "[hint-4]") {
		t.Errorf("summary missing suppressed tag [hint-4]: %s", summary)
	}
}

func TestCoalesceGuidance_UnTaggedHints(t *testing.T) {
	hints := []string{
		"untagged first warning",
		"untagged second warning",
		"untagged third warning",
		"untagged fourth warning",
		"untagged fifth warning",
	}
	// Without tags, dedup can't work, but cap should still apply.
	result := coalesceGuidance(hints)
	// No dedup possible on untagged hints, so all 5 enter, then cap kicks in.
	if len(result) > coalesceMaxHints+1 {
		t.Fatalf("expected at most %d, got %d", coalesceMaxHints+1, len(result))
	}
}

func TestCoalesceGuidance_ExactAtCap(t *testing.T) {
	// Exactly coalesceMaxHints should not trigger suppression.
	// With cap=1, a single hint should pass through cleanly.
	hints := []string{
		"[hint-1] one",
	}
	result := coalesceGuidance(hints)
	if len(result) != coalesceMaxHints {
		t.Fatalf("expected %d (no suppression needed), got %d: %v", coalesceMaxHints, len(result), result)
	}
	for _, h := range result {
		if strings.Contains(h, "guidance-coalesced") {
			t.Errorf("should not have suppression summary when at exact cap: %s", h)
		}
	}
}

func TestDedupByTag(t *testing.T) {
	hints := []string{
		"[A] first",
		"[B] second",
		"[A] duplicate of A",
		"[C] third",
		"[B] duplicate of B",
	}
	result := dedupByTag(hints)
	if len(result) != 3 {
		t.Fatalf("expected 3 after dedup, got %d: %v", len(result), result)
	}
	if result[0] != "[A] first" {
		t.Errorf("expected first A, got %s", result[0])
	}
	if result[1] != "[B] second" {
		t.Errorf("expected first B, got %s", result[1])
	}
	if result[2] != "[C] third" {
		t.Errorf("expected first C, got %s", result[2])
	}
}

func TestCoalesceGuidance_MixedCriticalAndAdvisory(t *testing.T) {
	hints := []string{
		"[advisory-1] first advisory",
		"[advisory-2] second advisory",
		"[advisory-3] third advisory",
		"[advisory-4] fourth advisory",
		"[CRITICAL] must keep this",
	}
	result := coalesceGuidance(hints)
	// Critical takes a slot, so 2 advisory + 1 critical + summary = 4
	// But critical is retained, advisory fills remaining, rest suppressed.
	criticalFound := false
	for _, h := range result {
		if strings.Contains(h, "must keep this") {
			criticalFound = true
		}
	}
	if !criticalFound {
		t.Errorf("critical hint missing from result: %v", result)
	}
	// Should have a suppression summary since some were dropped.
	hasSummary := false
	for _, h := range result {
		if strings.Contains(h, "guidance-coalesced") {
			hasSummary = true
		}
	}
	if !hasSummary {
		t.Errorf("expected suppression summary for mixed hints: %v", result)
	}
}
