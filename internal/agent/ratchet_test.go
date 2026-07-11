package agent

import (
	"strings"
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
		MatchPattern: "libolm.*header",
		ToolPattern:  "go build|go test|go vet",
		FixHint:      "Add -tags goolm",
	})
	rs.AddRule(Rule{
		Category:     "git",
		Rule:         "Check untracked files",
		MatchPattern: "git commit",
	})

	// run_command with "go build" should match the build rule via ToolPattern
	matching := rs.MatchingRulesForTool("run_command", "go build -tags goolm ./...")
	if len(matching) != 1 {
		t.Fatalf("expected 1 matching rule, got %d", len(matching))
	}
	if matching[0].Rule != "Use -tags goolm" {
		t.Errorf("unexpected rule: %s", matching[0].Rule)
	}

	// git_commit should match the git rule (ToolPattern empty, falls back to MatchPattern)
	matching = rs.MatchingRulesForTool("git_commit", "git commit -m test")
	if len(matching) != 1 {
		t.Fatalf("expected 1 git matching rule, got %d", len(matching))
	}

	// write_file should match neither
	matching = rs.MatchingRulesForTool("write_file", "/some/path")
	if len(matching) != 0 {
		t.Errorf("expected 0 matching rules for write_file, got %d", len(matching))
	}

	// ToolPattern matching: "go test" should also match the build rule
	matching = rs.MatchingRulesForTool("run_command", "go test ./...")
	if len(matching) != 1 {
		t.Errorf("expected 1 matching rule for go test via tool_pattern, got %d", len(matching))
	}

	// A command that does NOT match ToolPattern should not match
	matching = rs.MatchingRulesForTool("run_command", "echo hello")
	if len(matching) != 0 {
		t.Errorf("expected 0 matching rules for echo hello, got %d", len(matching))
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

	// Add a build rule with separate error and tool patterns
	rs := NewRuleStore(dir)
	rs.AddRule(Rule{
		Category:     "build",
		Rule:         "Use -tags goolm",
		MatchPattern: "libolm.*header",
		ToolPattern:  "go build|go test|go vet",
		FixHint:      "Add -tags goolm",
	})

	// Inject for matching tool (tool args match ToolPattern)
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

	// Non-matching tool args should NOT inject
	result = a.injectRulesIntoResult("run_command",
		[]byte(`{"command":"echo hello"}`),
		"echo output")
	if result != "echo output" {
		t.Error("expected no injection for non-matching command")
	}

	// Non-matching tool category should not inject
	result = a.injectRulesIntoResult("read_file",
		[]byte(`{"path":"/some/file"}`),
		"File contents")
	if result != "File contents" {
		t.Error("expected no injection for non-matching tool")
	}

	// Backward compat: rule with only MatchPattern (no ToolPattern) should still work for injection
	rs.AddRule(Rule{
		Category:     "git",
		Rule:         "Check untracked files",
		MatchPattern: "git commit",
	})
	result = a.injectRulesIntoResult("git_commit",
		[]byte(`{"message":"git commit -m test"}`),
		"Committed")
	if result == "Committed" {
		t.Error("expected injected content for backward-compat rule")
	}
	if !contains(result, "Check untracked files") {
		t.Error("expected backward-compat rule text in injected result")
	}
}

// --- Staleness & TTL tests ---

func TestCleanStale_RemovesStaleLowValue(t *testing.T) {
	dir := t.TempDir()
	rs := NewRuleStore(dir)

	// Add a stale, low-value rule (should be removed)
	rs.AddRule(Rule{
		Category:     "build",
		Rule:         "stale low-value rule",
		MatchPattern: "stale-pattern",
	})
	// Manually set LastSeen to 40 days ago and low HitCount
	rs.mu.Lock()
	rs.load()
	rs.rules[0].LastSeen = time.Now().Add(-40 * 24 * time.Hour)
	rs.rules[0].HitCount = 1
	rs.mu.Unlock()

	// Add a fresh rule (should be kept)
	rs.AddRule(Rule{
		Category:     "test",
		Rule:         "fresh rule",
		MatchPattern: "fresh-pattern",
	})

	removed := rs.CleanStale()
	if removed != 1 {
		t.Fatalf("expected 1 stale rule removed, got %d", removed)
	}

	rules := rs.Rules()
	if len(rules) != 1 {
		t.Fatalf("expected 1 rule remaining, got %d", len(rules))
	}
	if rules[0].Rule != "fresh rule" {
		t.Errorf("expected fresh rule to survive, got: %s", rules[0].Rule)
	}
}

func TestCleanStale_PreservesHighValueStale(t *testing.T) {
	dir := t.TempDir()
	rs := NewRuleStore(dir)

	// Add a stale but high-value rule (HitCount >= 3)
	rs.AddRule(Rule{
		Category:     "build",
		Rule:         "important stale rule",
		MatchPattern: "important",
	})
	rs.mu.Lock()
	rs.load()
	rs.rules[0].LastSeen = time.Now().Add(-40 * 24 * time.Hour)
	rs.rules[0].HitCount = 5 // high value
	rs.mu.Unlock()

	removed := rs.CleanStale()
	if removed != 0 {
		t.Fatalf("expected 0 removals for high-value stale rule, got %d", removed)
	}

	rules := rs.Rules()
	if len(rules) != 1 {
		t.Fatalf("expected 1 rule preserved, got %d", len(rules))
	}
}

func TestCleanStale_EmptyStore(t *testing.T) {
	dir := t.TempDir()
	rs := NewRuleStore(dir)

	removed := rs.CleanStale()
	if removed != 0 {
		t.Errorf("expected 0 removals on empty store, got %d", removed)
	}
}

func TestRecencyWeightedScore(t *testing.T) {
	now := time.Now()

	// Just seen: weight ~1.0
	score := recencyWeightedScore(5, now, now)
	if score < 4.9 || score > 5.1 {
		t.Errorf("expected score ~5.0 for just-seen rule with 5 hits, got %.2f", score)
	}

	// 7 days ago: weight ~0.5 → score ~2.5
	score = recencyWeightedScore(5, now.Add(-7*24*time.Hour), now)
	if score < 2.0 || score > 3.0 {
		t.Errorf("expected score ~2.5 for 7-day-old rule with 5 hits, got %.2f", score)
	}

	// 30 days ago: weight ~0.19 → score ~0.95
	score = recencyWeightedScore(5, now.Add(-30*24*time.Hour), now)
	if score > 1.5 {
		t.Errorf("expected low score for 30-day-old rule, got %.2f", score)
	}

	// Recent low-hit should score lower than stale high-hit
	recentLow := recencyWeightedScore(1, now, now)
	staleHigh := recencyWeightedScore(10, now.Add(-14*24*time.Hour), now)
	if recentLow >= staleHigh {
		t.Errorf("expected stale high-hit (%.2f) to outrank recent low-hit (%.2f)", staleHigh, recentLow)
	}
}

func TestTopRulesForPrompt_RecencyWeighting(t *testing.T) {
	dir := t.TempDir()
	rs := NewRuleStore(dir)

	// Rule A: high hit count but very stale
	rs.AddRule(Rule{
		Category:     "build",
		Rule:         "stale high-hit rule",
		MatchPattern: "stale",
	})
	rs.mu.Lock()
	rs.load()
	rs.rules[0].HitCount = 20
	rs.rules[0].LastSeen = time.Now().Add(-60 * 24 * time.Hour) // 60 days stale
	rs.mu.Unlock()

	// Rule B: lower hit count but very recent
	rs.AddRule(Rule{
		Category:     "test",
		Rule:         "recent low-hit rule",
		MatchPattern: "recent",
	})
	rs.mu.Lock()
	rs.load()
	rs.rules[1].HitCount = 3
	rs.rules[1].LastSeen = time.Now() // just now
	rs.mu.Unlock()

	prompt := rs.TopRulesForPrompt(5)
	if prompt == "" {
		t.Fatal("expected non-empty prompt")
	}

	// The recent rule should appear before the stale rule due to recency weighting
	idxRecent := strings.Index(prompt, "recent low-hit rule")
	idxStale := strings.Index(prompt, "stale high-hit rule")
	if idxRecent < 0 || idxStale < 0 {
		t.Fatalf("both rules should be present in prompt")
	}
	if idxRecent > idxStale {
		t.Errorf("expected recent rule to rank before stale rule in prompt output")
	}
}
