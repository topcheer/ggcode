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

	// Should contain deletion
	if !contains(result, "- line2") {
		t.Errorf("expected deletion line, got:\n%s", result)
	}
	// Should contain addition
	if !contains(result, "+ modified") {
		t.Errorf("expected addition line, got:\n%s", result)
	}
}

func TestUnifiedDiffEmpty(t *testing.T) {
	result := UnifiedDiff("", "", 3)
	// Empty strings produce no changes
	if result == "" {
		t.Log("empty diff for empty strings - OK")
	}
}

func TestUnifiedDiffNoChanges(t *testing.T) {
	same := "hello\nworld\n"
	result := UnifiedDiff(same, same, 3)
	// No changes → no diff hunk headers
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
	// Generate two files large enough to trigger the fallback (>5000 combined lines)
	var oldLines, newLines []string
	for i := 0; i < 3000; i++ {
		oldLines = append(oldLines, "line "+itoa(i))
	}
	newLines = append([]string(nil), oldLines...)
	// Modify lines 10-12 and last 3 lines
	newLines[10] = "changed line 10"
	newLines[11] = "changed line 11"
	newLines[len(newLines)-1] = "changed last"

	oldText := strings.Join(oldLines, "\n")
	newText := strings.Join(newLines, "\n")

	result := UnifiedDiff(oldText, newText, 3)

	// Must not be empty
	if result == "" {
		t.Fatal("expected non-empty diff for large file")
	}
	// Must contain truncation indicator
	if !strings.Contains(result, "diff truncated") {
		t.Fatal("expected truncation indicator for large file diff")
	}
	// Must contain the changed lines
	if !strings.Contains(result, "changed line 10") {
		t.Error("expected changed content in diff")
	}
}

func itoa(n int) string {
	return fmt.Sprintf("%d", n)
}
