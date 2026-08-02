package agent

import (
	"strings"
	"testing"
)

// generateLines produces a string with n identical lines of "line <i>".
func generateLines(n int) string {
	var b strings.Builder
	for i := 0; i < n; i++ {
		b.WriteString("line ")
		b.WriteByte('a' + byte(i%26))
		b.WriteString("\n")
	}
	return b.String()
}

func TestCheckEditBlastRadius_TargetedEditNoWarning(t *testing.T) {
	// A small targeted edit (2 of 25 lines changed) should NOT warn.
	old := generateLines(25)
	// Change only 2 lines (8% change rate).
	newStr := strings.Replace(old, "line a\nline b\n", "line x\nline y\n", 1)
	warn := checkEditBlastRadius("test.go", old, newStr)
	if warn != "" {
		t.Errorf("expected no warning for targeted edit, got: %s", warn)
	}
}

func TestCheckEditBlastRadius_MajorRewriteWarns(t *testing.T) {
	// Replace 80% of lines -> should warn.
	old := generateLines(25)
	var b strings.Builder
	for i := 0; i < 5; i++ {
		b.WriteString("line a\n")
	}
	for i := 0; i < 20; i++ {
		b.WriteString("completely different content\n")
	}
	newStr := b.String()
	warn := checkEditBlastRadius("test.go", old, newStr)
	if warn == "" {
		t.Error("expected warning for 80% change, got empty")
	}
	if !strings.Contains(warn, "blast-radius") {
		t.Errorf("warning should mention blast-radius, got: %s", warn)
	}
}

func TestCheckEditBlastRadius_ShrinkWarns(t *testing.T) {
	// File shrinks from 50 to 5 lines -> 90% changed -> should warn.
	old := generateLines(50)
	newStr := "only a few lines\nremain\nhere\n"
	warn := checkEditBlastRadius("test.go", old, newStr)
	if warn == "" {
		t.Error("expected warning for major shrink, got empty")
	}
	if !strings.Contains(warn, "removed") {
		t.Errorf("warning should mention removed lines, got: %s", warn)
	}
}

func TestCheckEditBlastRadius_SmallFileSkipped(t *testing.T) {
	// File under 20 lines -> should NOT warn even if 100% changed.
	old := "line1\nline2\nline3\n"
	newStr := "different1\ndifferent2\ndifferent3\n"
	warn := checkEditBlastRadius("test.go", old, newStr)
	if warn != "" {
		t.Errorf("expected no warning for small file, got: %s", warn)
	}
}

func TestCheckEditBlastRadius_EmptyContentSkipped(t *testing.T) {
	// Empty old or new content -> should NOT warn.
	warn := checkEditBlastRadius("test.go", "", generateLines(50))
	if warn != "" {
		t.Errorf("expected no warning for empty old content, got: %s", warn)
	}
	warn = checkEditBlastRadius("test.go", generateLines(50), "")
	if warn != "" {
		t.Errorf("expected no warning for empty new content, got: %s", warn)
	}
}

func TestCheckEditBlastRadius_BoundaryCase60Percent(t *testing.T) {
	// Exactly 60% change on a 20-line file should warn (>= 0.60).
	old := generateLines(20)
	var b strings.Builder
	// Keep 8 lines unchanged, change 12 lines.
	for i := 0; i < 8; i++ {
		b.WriteString("line a\n")
	}
	for i := 0; i < 12; i++ {
		b.WriteString("new content line\n")
	}
	newStr := b.String()
	warn := checkEditBlastRadius("test.go", old, newStr)
	if warn == "" {
		t.Error("expected warning at exactly 60% change boundary")
	}
}

func TestCheckEditBlastRadius_JustBelow60NoWarning(t *testing.T) {
	// Change 4 of 20 unique lines -> 8 changed / 20 total = 40% -> should NOT warn.
	var oldB strings.Builder
	for i := 0; i < 20; i++ {
		oldB.WriteString("unique line ")
		oldB.WriteByte('a' + byte(i))
		oldB.WriteString("\n")
	}
	old := oldB.String()
	newStr := old
	for _, c := range []byte("mnop") {
		old := "unique line " + string(c) + "\n"
		newL := "changed line " + string(c) + "\n"
		newStr = strings.Replace(newStr, old, newL, 1)
	}
	warn := checkEditBlastRadius("test.go", old, newStr)
	if warn != "" {
		t.Errorf("expected no warning for 40%% blast radius (4 of 20 lines), got: %s", warn)
	}
}

func TestCheckEditBlastRadius_NoChangeSkipped(t *testing.T) {
	// Identical content -> should NOT warn.
	content := generateLines(25)
	warn := checkEditBlastRadius("test.go", content, content)
	if warn != "" {
		t.Errorf("expected no warning for no change, got: %s", warn)
	}
}

func TestCheckEditBlastRadius_WarningContainsActionableGuidance(t *testing.T) {
	// The warning should contain actionable information for the agent.
	old := generateLines(30)
	newStr := generateLines(5) // major shrink
	warn := checkEditBlastRadius("test.go", old, newStr)
	if warn == "" {
		t.Fatal("expected warning")
	}
	// Should mention the percentage
	if !strings.Contains(warn, "%") {
		t.Errorf("warning should contain percentage, got: %s", warn)
	}
	// Should mention edit_file as suggested alternative
	if !strings.Contains(warn, "edit_file") {
		t.Errorf("warning should suggest edit_file, got: %s", warn)
	}
}
