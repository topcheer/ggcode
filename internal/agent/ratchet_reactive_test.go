package agent

import (
	"os"
	"path/filepath"
	"testing"
)

func TestMatchingRulesForResult_MatchesKnownError(t *testing.T) {
	dir := t.TempDir()
	rs := NewRuleStore(dir)

	// Add a rule that matches "cannot find package" errors
	rs.AddRule(Rule{
		Category:     "build",
		Rule:         "Go commands must use -tags goolm",
		MatchPattern: `cannot find package.*olm`,
		ToolPattern:  `go build|go test`,
		FixHint:      "Add -tags goolm to the go command",
	})

	result := "Error: cannot find package \"github.com/tul/olm\" in any of /usr/local/go/src"
	matched := rs.MatchingRulesForResult(result)

	if len(matched) != 1 {
		t.Fatalf("expected 1 reactive match, got %d", len(matched))
	}
	if matched[0].FixHint != "Add -tags goolm to the go command" {
		t.Errorf("unexpected FixHint: %s", matched[0].FixHint)
	}
}

func TestMatchingRulesForResult_NoMatchForSuccess(t *testing.T) {
	dir := t.TempDir()
	rs := NewRuleStore(dir)

	rs.AddRule(Rule{
		Category:     "build",
		Rule:         "Go commands must use -tags goolm",
		MatchPattern: `cannot find package.*olm`,
		FixHint:      "Add -tags goolm",
	})

	// Successful output — should not match
	result := "Build successful. All tests passed."
	matched := rs.MatchingRulesForResult(result)

	if len(matched) != 0 {
		t.Fatalf("expected 0 matches for success output, got %d", len(matched))
	}
}

func TestMatchingRulesForResult_SkipsWithoutErrorMarkers(t *testing.T) {
	dir := t.TempDir()
	rs := NewRuleStore(dir)

	rs.AddRule(Rule{
		Category:     "build",
		Rule:         "Check for missing imports",
		MatchPattern: `import`,
		FixHint:      "Run goimports",
	})

	// Contains "import" but no error markers — should skip
	result := "This file import is fine."
	matched := rs.MatchingRulesForResult(result)

	if len(matched) != 0 {
		t.Fatalf("expected 0 matches without error markers, got %d", len(matched))
	}
}

func TestMatchingRulesForResult_MatchesMultipleRules(t *testing.T) {
	dir := t.TempDir()
	rs := NewRuleStore(dir)

	rs.AddRule(Rule{
		Category:     "build",
		Rule:         "Go commands must use -tags goolm",
		MatchPattern: `cannot find.*olm`,
		FixHint:      "Add -tags goolm",
	})
	rs.AddRule(Rule{
		Category:     "convention",
		Rule:         "Never copy structs with sync.Mutex",
		MatchPattern: `copy lock|Mutex.*by value`,
		FixHint:      "Use a pointer instead",
	})

	result := "Error: cannot find olm header. Also: copy lock detected"
	matched := rs.MatchingRulesForResult(result)

	if len(matched) != 2 {
		t.Fatalf("expected 2 matches, got %d", len(matched))
	}
}

func TestMergeRuleSets_Deduplicates(t *testing.T) {
	r1 := Rule{ID: "r-aaa", Category: "build", Rule: "Use -tags goolm"}
	r2 := Rule{ID: "r-bbb", Category: "test", Rule: "Run with -p 1"}
	r3 := Rule{ID: "r-aaa", Category: "build", Rule: "Use -tags goolm"} // dup of r1

	merged := mergeRuleSets([]Rule{r1, r2}, []Rule{r3})
	if len(merged) != 2 {
		t.Fatalf("expected 2 after dedup, got %d", len(merged))
	}
}

func TestMergeRuleSets_EmptyInputs(t *testing.T) {
	merged := mergeRuleSets(nil, nil)
	if len(merged) != 0 {
		t.Fatalf("expected 0, got %d", len(merged))
	}
}

func TestMergeRuleSets_PreventiveFirst(t *testing.T) {
	preventive := Rule{ID: "r-prevent", Category: "build", Rule: "Preventive rule"}
	reactive := Rule{ID: "r-react", Category: "build", Rule: "Reactive rule"}

	merged := mergeRuleSets([]Rule{preventive}, []Rule{reactive})
	if len(merged) != 2 {
		t.Fatalf("expected 2, got %d", len(merged))
	}
	if merged[0].ID != "r-prevent" {
		t.Errorf("expected preventive first, got %s", merged[0].ID)
	}
	if merged[1].ID != "r-react" {
		t.Errorf("expected reactive second, got %s", merged[1].ID)
	}
}

func TestMatchingRulesForResult_DetectsVariousErrorMarkers(t *testing.T) {
	dir := t.TempDir()
	rs := NewRuleStore(dir)

	// Add rules with different patterns
	rs.AddRule(Rule{ID: "r-1", Category: "build", Rule: "Build error", MatchPattern: `FAIL`})
	rs.AddRule(Rule{ID: "r-2", Category: "security", Rule: "Permission error", MatchPattern: `denied`})

	tests := []struct {
		name   string
		result string
		want   int
	}{
		{"fail marker", "test FAIL to build", 1},
		{"denied marker", "Error: permission denied", 1},
		{"both markers", "FAIL: permission denied", 2},
		{"no markers", "everything looks good", 0},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			matched := rs.MatchingRulesForResult(tt.result)
			if len(matched) != tt.want {
				t.Errorf("MatchingRulesForResult(%q) = %d matches, want %d", tt.result, len(matched), tt.want)
			}
		})
	}
}

// TestInjectRulesIntoResult_ReactiveMatching tests the full integration:
// when a tool result contains an error matching a known rule, the FixHint
// should be injected into the tool result.
func TestInjectRulesIntoResult_ReactiveMatching(t *testing.T) {
	dir := t.TempDir()
	rulesPath := filepath.Join(dir, ".ggcode", "agent-rules.json")
	if err := os.MkdirAll(filepath.Dir(rulesPath), 0755); err != nil {
		t.Fatal(err)
	}

	// Write rules file with a pattern matching "olm" errors
	ruleJSON := `{"version":1,"rules":[{"id":"r-test1","category":"build","rule":"Use -tags goolm","match_pattern":"cannot find.*olm","tool_pattern":"","fix_hint":"Add -tags goolm","hit_count":3,"created_at":"2025-01-01T00:00:00Z","last_seen":"2025-01-01T00:00:00Z"}]}`
	if err := os.WriteFile(rulesPath, []byte(ruleJSON), 0644); err != nil {
		t.Fatal(err)
	}

	a := &Agent{}
	a.SetWorkingDir(dir)

	result := a.injectRulesIntoResult("run_command", []byte(`{"command":"go build ./..."}`),
		"Error: cannot find package olm")

	if result == "Error: cannot find package olm" {
		t.Fatal("expected rule injection in result, got unchanged result")
	}
}
