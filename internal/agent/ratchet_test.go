package agent

import (
	"os"
	"path/filepath"
	"testing"
	"time"
)

func TestRuleStoreAddAndMatch(t *testing.T) {
	dir := t.TempDir()
	rs := NewRuleStore(dir)
	if rs == nil {
		t.Fatal("expected non-nil RuleStore")
	}

	rs.AddRule(Rule{
		Category:     "build",
		Rule:         "Use -tags goolm",
		MatchPattern: "libolm.*header",
		FixHint:      "Add -tags goolm",
	})

	rules := rs.Rules()
	if len(rules) != 1 {
		t.Fatalf("expected 1 rule, got %d", len(rules))
	}
	if rules[0].HitCount != 1 {
		t.Errorf("expected hit_count=1, got %d", rules[0].HitCount)
	}

	// Match an error that fits the pattern
	matched, unmatched := rs.MatchErrors([]string{
		"libolm C headers not found",
		"some other error",
	})
	if len(matched) != 1 || len(unmatched) != 1 {
		t.Fatalf("expected 1 matched, 1 unmatched; got %d matched, %d unmatched", len(matched), len(unmatched))
	}

	// Verify hit_count incremented
	rules = rs.Rules()
	if rules[0].HitCount != 2 {
		t.Errorf("expected hit_count=2 after match, got %d", rules[0].HitCount)
	}
}

func TestRuleStorePersistence(t *testing.T) {
	dir := t.TempDir()
	rs1 := NewRuleStore(dir)
	rs1.AddRule(Rule{
		Category:     "test",
		Rule:         "Run tests after changes",
		MatchPattern: "test failed",
	})

	// Create a new store pointing at the same file
	rs2 := NewRuleStore(dir)
	rules := rs2.Rules()
	if len(rules) != 1 {
		t.Fatalf("expected 1 rule after reload, got %d", len(rules))
	}
	if rules[0].Rule != "Run tests after changes" {
		t.Errorf("unexpected rule: %s", rules[0].Rule)
	}
}

func TestRuleStoreEviction(t *testing.T) {
	dir := t.TempDir()
	rs := NewRuleStore(dir)
	rs.maxRules = 3 // small limit for testing

	// Add 5 rules with different recency
	for i := 0; i < 5; i++ {
		rs.AddRule(Rule{
			Category:     "build",
			Rule:         "rule-" + string(rune('A'+i)),
			MatchPattern: "pattern-" + string(rune('A'+i)),
		})
		// Slight time offset so they have different ages
		time.Sleep(2 * time.Millisecond)
	}

	rules := rs.Rules()
	if len(rules) != 3 {
		t.Errorf("expected 3 rules after eviction, got %d", len(rules))
	}
}

func TestRuleStoreMatchingRulesForTool(t *testing.T) {
	dir := t.TempDir()
	rs := NewRuleStore(dir)
	rs.AddRule(Rule{
		Category:     "build",
		Rule:         "Use -tags goolm",
		MatchPattern: "go build",
		FixHint:      "Add -tags goolm",
	})
	rs.AddRule(Rule{
		Category:     "git",
		Rule:         "Check untracked files",
		MatchPattern: "git commit",
	})

	// run_command with "go build" should match the build rule
	matching := rs.MatchingRulesForTool("run_command", "go build -tags goolm ./...")
	if len(matching) != 1 {
		t.Fatalf("expected 1 matching rule, got %d", len(matching))
	}
	if matching[0].Rule != "Use -tags goolm" {
		t.Errorf("unexpected rule: %s", matching[0].Rule)
	}

	// git_commit should match the git rule
	matching = rs.MatchingRulesForTool("git_commit", "git commit -m test")
	if len(matching) != 1 {
		t.Fatalf("expected 1 git matching rule, got %d", len(matching))
	}

	// write_file should match neither
	matching = rs.MatchingRulesForTool("write_file", "/some/path")
	if len(matching) != 0 {
		t.Errorf("expected 0 matching rules for write_file, got %d", len(matching))
	}
}

func TestCategoryMatchesTool(t *testing.T) {
	tests := []struct {
		category string
		tool     string
		expect   bool
	}{
		{"build", "run_command", true},
		{"build", "write_file", false},
		{"test", "start_command", true},
		{"git", "git_commit", true},
		{"git", "run_command", false},
		{"convention", "edit_file", true},
		{"convention", "run_command", false},
		{"security", "write_file", true},
		{"security", "run_command", true},
		{"unknown", "run_command", false},
	}
	for _, tt := range tests {
		result := categoryMatchesTool(tt.category, tt.tool)
		if result != tt.expect {
			t.Errorf("categoryMatchesTool(%q, %q) = %v, want %v", tt.category, tt.tool, result, tt.expect)
		}
	}
}

func TestInferBuildCommand(t *testing.T) {
	dir := t.TempDir()

	// No project files → empty
	if cmd := inferBuildCommand(dir); cmd != "" {
		t.Errorf("expected empty command for empty dir, got %s", cmd)
	}

	// Go project
	os.WriteFile(filepath.Join(dir, "go.mod"), []byte("module test"), 0644)
	if cmd := inferBuildCommand(dir); cmd != "go build ./..." {
		t.Errorf("expected 'go build ./...', got %s", cmd)
	}
}

func TestExtractErrorLines(t *testing.T) {
	output := `Building...
some output here
Error: undefined variable 'foo'
more output
FAIL: test_something
panic: runtime error
normal line
fatal error: cannot compile`

	errors := extractErrorLines(output)
	if len(errors) != 4 {
		t.Fatalf("expected 4 error lines, got %d: %v", len(errors), errors)
	}
}

func TestInjectRulesIntoResult(t *testing.T) {
	dir := t.TempDir()
	a := &Agent{workingDir: dir}

	// Add a build rule
	rs := NewRuleStore(dir)
	rs.AddRule(Rule{
		Category:     "build",
		Rule:         "Use -tags goolm",
		MatchPattern: "go build",
		FixHint:      "Add -tags goolm",
	})

	// Inject for matching tool
	result := a.injectRulesIntoResult("run_command",
		[]byte(`{"command":"go build ./..."}`),
		"Build succeeded")
	if result == "Build succeeded" {
		t.Error("expected injected content, got original")
	}
	if !contains(result, "Harness Rules") {
		t.Error("expected 'Harness Rules' header in injected result")
	}
	if !contains(result, "Use -tags goolm") {
		t.Error("expected rule text in injected result")
	}

	// Non-matching tool should not inject
	result = a.injectRulesIntoResult("read_file",
		[]byte(`{"path":"/some/file"}`),
		"File contents")
	if result != "File contents" {
		t.Error("expected no injection for non-matching tool")
	}
}
