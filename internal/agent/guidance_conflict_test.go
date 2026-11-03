package agent

import (
	"strings"
	"testing"
)

func TestDetectGuidanceConflict_NoConflict(t *testing.T) {
	tests := []struct {
		name  string
		hints []string
	}{
		{
			name: "single hint",
			hints: []string{
				"[tool-storm] Too many tool calls detected.",
			},
		},
		{
			name: "two non-conflicting hints",
			hints: []string{
				"[tool-storm] Too many tool calls.",
				"[budget-guard] Context budget at 50%.",
			},
		},
		{
			name: "three non-conflicting hints",
			hints: []string{
				"[verify-lint] Run go vet after edits.",
				"[staging-quality] Check staged files.",
				"[budget-guard] Context budget at 50%.",
			},
		},
		{
			name:  "empty slice",
			hints: []string{},
		},
		{
			name:  "nil slice",
			hints: nil,
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			result := detectGuidanceConflict(tc.hints)
			if result != "" {
				t.Fatalf("expected no conflict, got: %s", result)
			}
		})
	}
}

func TestDetectGuidanceConflict_ActVsExplore(t *testing.T) {
	hints := []string{
		"[analysis-paralysis] ACT NOW: make your best-guess edit.",
		"[empty-search-spiral] You should broaden your search scope.",
	}

	result := detectGuidanceConflict(hints)
	if result == "" {
		t.Fatal("expected conflict detection, got empty")
	}
	if !strings.Contains(result, "[guidance-conflict]") {
		t.Fatalf("expected [guidance-conflict] tag, got: %s", result)
	}
	if !strings.Contains(result, "exploration") {
		t.Fatalf("expected explore/exploit priority guidance, got: %s", result)
	}
}

func TestDetectGuidanceConflict_NarrowVsBroaden(t *testing.T) {
	hints := []string{
		"[verify-scope-narrow] NARROW SCOPE: your query is too broad.",
		"[web-search-quality] Broaden your search for better coverage.",
	}

	result := detectGuidanceConflict(hints)
	if result == "" {
		t.Fatal("expected conflict detection, got empty")
	}
	if !strings.Contains(result, "[guidance-conflict]") {
		t.Fatalf("expected [guidance-conflict] tag, got: %s", result)
	}
}

func TestDetectGuidanceConflict_SpeedVsThorough(t *testing.T) {
	hints := []string{
		"[tool-call-economy] Too many calls; reduce iterations.",
		"[verify-regression] Be thorough: run full test suite.",
	}

	result := detectGuidanceConflict(hints)
	if result == "" {
		t.Fatal("expected conflict detection, got empty")
	}
}

func TestDetectGuidanceConflict_CommitVsDontCommit(t *testing.T) {
	hints := []string{
		"[commit-partitioning] Ready to commit, stage and commit now.",
		"[precommit-build-gate] Do not commit until build passes.",
	}

	result := detectGuidanceConflict(hints)
	if result == "" {
		t.Fatal("expected conflict detection, got empty")
	}
	if !strings.Contains(strings.ToLower(result), "conservative") {
		t.Fatalf("expected conservative directive to win, got: %s", result)
	}
}

func TestDetectGuidanceConflict_SameHintNotConflict(t *testing.T) {

	hints := []string{
		"[mixed] ACT NOW but also explore more before acting.",
	}

	result := detectGuidanceConflict(hints)
	if result != "" {
		t.Fatalf("single hint with both keywords should not trigger: %s", result)
	}
}

func TestDetectGuidanceConflict_CaseInsensitive(t *testing.T) {

	hints := []string{
		"[test] make your best-guess edit now.",
		"[test2] Please Explore More Before Acting.",
	}

	result := detectGuidanceConflict(hints)
	if result == "" {
		t.Fatal("expected case-insensitive conflict detection, got empty")
	}
}

func TestDetectGuidanceConflict_SameDirectionNoConflict(t *testing.T) {

	hints := []string{
		"[test] ACT NOW, stop exploring.",
		"[test2] stop exploring further and edit now.",
	}
	if result := detectGuidanceConflict(hints); result != "" {
		t.Fatalf("expected no conflict for same-direction hints, got: %s", result)
	}
}

func TestDetectGuidanceConflict_MaxOneConflict(t *testing.T) {

	hints := []string{
		"[a] ACT NOW and commit.",
		"[b] Please explore more first.",
		"[c] Narrow your scope.",
		"[d] Broaden your search.",
	}

	result := detectGuidanceConflict(hints)
	if result == "" {
		t.Fatal("expected conflict detection, got empty")
	}

	count := strings.Count(result, "[guidance-conflict]")
	if count != 1 {
		t.Fatalf("expected exactly 1 conflict tag, got %d", count)
	}
}

func TestContainsAnyKeyword(t *testing.T) {
	subs := []string{"ACT NOW", "STOP EXPLOR"}

	if !containsAnyKeyword("PLEASE ACT NOW", subs) {
		t.Error("expected match for 'ACT NOW'")
	}
	if !containsAnyKeyword("STOP EXPLORING FURTHER", subs) {
		t.Error("expected match for 'STOP EXPLOR'")
	}
	if containsAnyKeyword("KEEP EXPLORING", subs) {
		t.Error("did not expect match for 'KEEP EXPLORING'")
	}
}
