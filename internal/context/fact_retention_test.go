package context

// applyFactRetention tests (sa-237): dropped constraints and recurring
// paths are re-attached; preserved facts are a no-op; the appended
// section respects its char budget.

import (
	"strings"
	"testing"
)

func TestApplyFactRetention_ReattachesDroppedConstraint(t *testing.T) {
	payload := "User: please add the feature\nUser: don't modify the existing tests\nAssistant: done"
	summary := "## Task\nAdd feature.\n## Done\nAdded."
	out := applyFactRetention(summary, payload)
	if !strings.Contains(out, "## Auto-preserved Facts") {
		t.Fatalf("expected auto-preserved section, got:\n%s", out)
	}
	if !strings.Contains(out, "don't modify the existing tests") {
		t.Fatalf("dropped constraint not re-attached:\n%s", out)
	}
}

func TestApplyFactRetention_CJKConstraint(t *testing.T) {
	payload := "User: 新功能加好了\nUser: 不要改测试文件\nAssistant: ok"
	summary := "## Task\n新功能."
	out := applyFactRetention(summary, payload)
	if !strings.Contains(out, "不要改测试文件") {
		t.Fatalf("CJK constraint not re-attached:\n%s", out)
	}
}

func TestApplyFactRetention_PreservedFactIsNoop(t *testing.T) {
	payload := "User: don't modify the existing tests"
	summary := "## Decisions & Constraints\n- Don't modify the existing tests"
	out := applyFactRetention(summary, payload)
	if strings.Contains(out, "## Auto-preserved Facts") {
		t.Fatalf("summary preserved the fact; must be a no-op, got:\n%s", out)
	}
}

func TestApplyFactRetention_PreservedFactCaseFolding(t *testing.T) {
	// Paraphrased casing/spacing still counts as preserved.
	payload := "User: NEVER push directly to main branch"
	summary := "## Decisions & Constraints\n- never push directly to Main Branch"
	out := applyFactRetention(summary, payload)
	if strings.Contains(out, "## Auto-preserved Facts") {
		t.Fatalf("case-folded paraphrase should count as preserved, got:\n%s", out)
	}
}

func TestApplyFactRetention_ReattachesRecurringPath(t *testing.T) {
	payload := strings.Repeat("edited internal/context/manager.go and ran go build. ", 3)
	summary := "## Task\nWork done.\n## Key Files\nnone noted"
	out := applyFactRetention(summary, payload)
	if !strings.Contains(out, "recurring file: internal/context/manager.go") {
		t.Fatalf("recurring path not re-attached:\n%s", out)
	}
}

func TestApplyFactRetention_RarePathNotReattached(t *testing.T) {
	payload := "read /tmp/once.md once"
	summary := "## Task\nWork."
	out := applyFactRetention(summary, payload)
	if strings.Contains(out, "once.md") {
		t.Fatalf("path below recurrence threshold must not be re-attached:\n%s", out)
	}
}

func TestApplyFactRetention_BudgetCap(t *testing.T) {
	var lines []string
	for i := 0; i < 30; i++ {
		lines = append(lines, "User: never do thing number "+strings.Repeat("x", 60)+string(rune('a'+i%26)))
	}
	payload := strings.Join(lines, "\n")
	summary := "## Task\nx."
	out := applyFactRetention(summary, payload)
	section := out[strings.Index(out, "## Auto-preserved Facts"):]
	if len(section) > maxRetentionChars+200 { // header + bullets overhead
		t.Fatalf("appended section exceeded budget: %d chars", len(section))
	}
}

func TestApplyFactRetention_EmptyInputs(t *testing.T) {
	if got := applyFactRetention("", "payload"); got != "" {
		t.Fatalf("empty summary must stay empty, got %q", got)
	}
	if got := applyFactRetention("summary", ""); got != "summary" {
		t.Fatalf("empty payload must be a no-op, got %q", got)
	}
}
