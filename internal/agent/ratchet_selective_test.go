package agent

import (
	"strings"
	"testing"
	"time"

	"github.com/topcheer/ggcode/internal/provider"
)

// newSelectiveRuleStore returns an in-memory RuleStore (loaded flag preset so
// the pre-seeded rules are not overwritten by disk load) for task-selective
// injection tests.
func newSelectiveRuleStore(rules []Rule) *RuleStore {
	rs := NewRuleStore("/")
	if rs == nil {
		panic("nil rule store")
	}
	rs.rules = rules
	rs.loaded = true
	return rs
}

func ruleLines(out string) []string {
	var lines []string
	for _, l := range strings.Split(out, "\n") {
		if strings.HasPrefix(l, "- ") {
			lines = append(lines, strings.TrimPrefix(l, "- "))
		}
	}
	return lines
}

func TestCleanStaleRemovesAgedLowHitRules(t *testing.T) {
	rs := NewRuleStore(t.TempDir())
	old := time.Now().Add(-45 * 24 * time.Hour)
	fresh := time.Now()
	rs.mu.Lock()
	rs.rules = []Rule{
		{ID: "a", Category: "build", Rule: "stale low hit", HitCount: 1, LastSeen: old, CreatedAt: old},
		{ID: "b", Category: "build", Rule: "stale but proven", HitCount: 5, LastSeen: old, CreatedAt: old},
		{ID: "c", Category: "git", Rule: "fresh low hit", HitCount: 1, LastSeen: fresh, CreatedAt: fresh},
	}
	rs.mu.Unlock()

	if removed := rs.CleanStale(); removed != 1 {
		t.Fatalf("CleanStale removed = %d, want 1", removed)
	}
	for _, r := range rs.Rules() {
		if r.ID == "a" {
			t.Fatal("stale low-hit rule should have been removed")
		}
	}
	if len(rs.Rules()) != 2 {
		t.Fatalf("rule count = %d, want 2", len(rs.Rules()))
	}
}

func TestCleanStalePersistsToDisk(t *testing.T) {
	dir := t.TempDir()
	rs := NewRuleStore(dir)
	old := time.Now().Add(-60 * 24 * time.Hour)
	// Seed directly: AddRule always stamps LastSeen=now, so a rule aged past
	// the staleness threshold can only be written via a direct store+save.
	rs.mu.Lock()
	rs.rules = []Rule{{Category: "git", Rule: "old unused rule", MatchPattern: "boom", HitCount: 1, LastSeen: old, CreatedAt: old}}
	saveErr := rs.save()
	rs.mu.Unlock()
	if saveErr != nil {
		t.Fatalf("seed save: %v", saveErr)
	}

	// A fresh store (fresh disk load) must observe the removal.
	rs2 := NewRuleStore(dir)
	if removed := rs2.CleanStale(); removed != 1 {
		t.Fatalf("removed = %d, want 1", removed)
	}
	for _, r := range NewRuleStore(dir).Rules() {
		if r.Rule == "old unused rule" {
			t.Fatal("stale rule persisted after sweep")
		}
	}
}

func TestCleanStaleZeroTimestampFallsBackToCreatedAt(t *testing.T) {
	rs := NewRuleStore(t.TempDir())
	old := time.Now().Add(-90 * 24 * time.Hour)
	rs.mu.Lock()
	rs.rules = []Rule{
		{ID: "x", Category: "test", Rule: "no last seen", HitCount: 1, CreatedAt: old},
	}
	rs.mu.Unlock()
	if removed := rs.CleanStale(); removed != 1 {
		t.Fatalf("removed = %d, want 1 (zero LastSeen should fall back to CreatedAt)", removed)
	}
}

func TestTopRulesForTaskFiltersIrrelevantCategories(t *testing.T) {
	now := time.Now()
	rs := newSelectiveRuleStore([]Rule{
		{ID: "g", Category: "git", Rule: "rebase before push", HitCount: 2, LastSeen: now},
		{ID: "b1", Category: "build", Rule: "use -tags goolm", FixHint: "add -tags goolm", HitCount: 4, LastSeen: now},
		{ID: "t1", Category: "test", Rule: "limit go test parallelism", HitCount: 3, LastSeen: now},
		{ID: "c1", Category: "convention", Rule: "always gofmt", HitCount: 2, LastSeen: now},
	})

	out := rs.TopRulesForTask(5, "fix the panic in the parser")
	if out == "" {
		t.Fatal("expected non-empty output")
	}
	if !strings.Contains(out, "-tags goolm") || !strings.Contains(out, "limit go test parallelism") {
		t.Fatalf("build/test rules expected in output:\n%s", out)
	}
	if strings.Contains(out, "rebase before push") || strings.Contains(out, "always gofmt") {
		t.Fatalf("git/convention rules should be filtered for a bugfix prompt:\n%s", out)
	}
}

func TestTopRulesForTaskFloorKeepsCriticalRule(t *testing.T) {
	now := time.Now()
	rules := []Rule{
		{ID: "g", Category: "git", Rule: "critical git lesson", HitCount: 50, LastSeen: now},
	}
	for i := 1; i <= 5; i++ {
		rules = append(rules, Rule{
			ID: "c", Category: "convention", Rule: "convention lesson", HitCount: i, LastSeen: now,
		})
	}
	rs := newSelectiveRuleStore(rules)

	// maxRules=3: floor=2 (critical git + best convention) + 1 relevant fill.
	out := rs.TopRulesForTask(3, "refactor the config module")
	lines := ruleLines(out)
	if len(lines) != 3 {
		t.Fatalf("expected 3 injected rules, got %d:\n%s", len(lines), out)
	}
	if !strings.Contains(out, "critical git lesson") {
		t.Fatalf("global floor should keep the critical high-hit rule:\n%s", out)
	}
}

func TestTopRulesForTaskUnfilteredFallbackMatchesTopRulesForPrompt(t *testing.T) {
	now := time.Now()
	rules := []Rule{
		{ID: "g", Category: "git", Rule: "rebase before push", HitCount: 2, LastSeen: now},
		{ID: "b1", Category: "build", Rule: "use -tags goolm", HitCount: 4, LastSeen: now},
	}
	rs := newSelectiveRuleStore(rules)

	for _, prompt := range []string{"", "help me understand the codebase"} {
		got := rs.TopRulesForTask(5, prompt)
		want := rs.TopRulesForPrompt(5)
		if got != want {
			t.Errorf("TopRulesForTask(%q) = %q, want identical to TopRulesForPrompt %q", prompt, got, want)
		}
	}
}

func TestTaskRuleCategories(t *testing.T) {
	cases := []struct {
		prompt string
		want   []string
	}{
		{"fix the login crash", []string{"build", "test"}},
		{"add a unit test for the parser", []string{"test", "build"}},
		{"refactor the config module", []string{"convention", "build"}},
		{"review this PR for issues", []string{"convention", "security"}},
		{"修复登录时的 panic", []string{"build", "test"}},
		{"重构这个模块", []string{"convention", "build"}},
		{"提交代码并推送", []string{"git"}},
		{"帮我理解这个代码库", nil},
		{"", nil},
	}
	for _, tc := range cases {
		got := taskRuleCategories(tc.prompt)
		gotSet := make(map[string]bool, len(got))
		for _, c := range got {
			gotSet[c] = true
		}
		if len(got) != len(tc.want) {
			t.Errorf("taskRuleCategories(%q) = %v, want %v", tc.prompt, got, tc.want)
			continue
		}
		for _, w := range tc.want {
			if !gotSet[w] {
				t.Errorf("taskRuleCategories(%q) = %v, want %v", tc.prompt, got, tc.want)
				break
			}
		}
	}
}

func TestLastUserPromptText(t *testing.T) {
	msgs := []provider.Message{
		{Role: "system", Content: []provider.ContentBlock{{Type: "text", Text: "sys"}}},
		{Role: "user", Content: []provider.ContentBlock{{Type: "text", Text: "fix the bug"}}},
		{Role: "assistant", Content: []provider.ContentBlock{{Type: "tool_use", ToolName: "run_command"}}},
		{Role: "user", Content: []provider.ContentBlock{{Type: "tool_result", Output: "some error output"}}},
	}
	if got := lastUserPromptText(msgs); got != "fix the bug" {
		t.Fatalf("lastUserPromptText = %q, want %q", got, "fix the bug")
	}
	if got := lastUserPromptText(nil); got != "" {
		t.Fatalf("lastUserPromptText(nil) = %q, want empty", got)
	}
	// Long prompts are truncated rune-safely.
	long := strings.Repeat("汉", 3000)
	got := lastUserPromptText([]provider.Message{
		{Role: "user", Content: []provider.ContentBlock{{Type: "text", Text: long}}},
	})
	if got == "" || len([]rune(got)) != maxClassifyPromptRunes {
		t.Fatalf("expected truncation to %d runes, got %d", maxClassifyPromptRunes, len([]rune(got)))
	}
}
