package diff

import (
	"fmt"
	"strings"
	"testing"
)

func TestUnifiedDiff(t *testing.T) {
	old := "line1\nline2\nline3\nline4\nline5\n"
	new := "line1\nmodified\nline3\nline4\nline5\n"

	result := UnifiedDiff(old, new, 1)
	if result == "" {
		t.Fatal("expected non-empty diff")
	}

	if !contains(result, "- line2") {
		t.Errorf("expected deletion line, got:\n%s", result)
	}

	if !contains(result, "+ modified") {
		t.Errorf("expected addition line, got:\n%s", result)
	}
}

func TestUnifiedDiffEmpty(t *testing.T) {
	result := UnifiedDiff("", "", 3)

	if result == "" {
		t.Log("empty diff for empty strings - OK")
	}
}

func TestUnifiedDiffNoChanges(t *testing.T) {
	same := "hello\nworld\n"
	result := UnifiedDiff(same, same, 3)

	if contains(result, "@@") {
		t.Errorf("expected no hunk headers for identical content, got:\n%s", result)
	}
}

func TestHasChanges(t *testing.T) {
	if !HasChanges("a", "b") {
		t.Error("expected true for different strings")
	}
	if HasChanges("same", "same") {
		t.Error("expected false for identical strings")
	}
}

func contains(s, substr string) bool {
	for i := 0; i <= len(s)-len(substr); i++ {
		if s[i:i+len(substr)] == substr {
			return true
		}
	}
	return false
}

func TestUnifiedDiffLargeFileFallback(t *testing.T) {

	var oldLines, newLines []string
	for i := 0; i < 3000; i++ {
		oldLines = append(oldLines, "line "+itoa(i))
	}
	newLines = append([]string(nil), oldLines...)

	newLines[10] = "changed line 10"
	newLines[11] = "changed line 11"
	newLines[len(newLines)-1] = "changed last"

	oldText := strings.Join(oldLines, "\n")
	newText := strings.Join(newLines, "\n")

	result := UnifiedDiff(oldText, newText, 3)

	if result == "" {
		t.Fatal("expected non-empty diff for large file")
	}

	if !strings.Contains(result, "diff truncated") {
		t.Fatal("expected truncation indicator for large file diff")
	}

	if !strings.Contains(result, "changed line 10") {
		t.Error("expected changed content in diff")
	}
}

func TestStats(t *testing.T) {

	s := Stats("hello\nworld", "hello\nworld")
	if !strings.Contains(s, "+0") || !strings.Contains(s, "-0") {
		t.Fatalf("expected 0 changes, got: %s", s)
	}

	s = Stats("hello\nworld\n", "hello\nworld\nfoo\n")
	if !strings.Contains(s, "+1") || !strings.Contains(s, "-0") {
		t.Fatalf("expected +1 -0, got: %s", s)
	}

	s = Stats("hello\nworld\nfoo\n", "hello\nworld\n")
	if !strings.Contains(s, "+0") || !strings.Contains(s, "-1") {
		t.Fatalf("expected +0 -1, got: %s", s)
	}

	s = Stats("a\nb\nc", "a\nx\nc")
	if !strings.Contains(s, "+1") || !strings.Contains(s, "-1") {
		t.Fatalf("expected +1 -1, got: %s", s)
	}
}

func itoa(n int) string {
	return fmt.Sprintf("%d", n)
}
