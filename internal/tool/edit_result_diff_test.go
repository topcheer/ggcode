package tool

import (
	"strings"
	"testing"
)

func TestCompactDiff_NoChange(t *testing.T) {
	result := compactDiff("hello\nworld\n", "hello\nworld\n")
	if result != "" {
		t.Fatalf("expected empty diff for identical content, got %q", result)
	}
}

func TestCompactDiff_SimpleEdit(t *testing.T) {
	old := "line1\nline2\nline3\n"
	new := "line1\nMODIFIED\nline3\n"
	result := compactDiff(old, new)
	if result == "" {
		t.Fatal("expected non-empty diff for changed content")
	}
	if !strings.Contains(result, "- line2") {
		t.Errorf("diff should contain removed line, got %q", result)
	}
	if !strings.Contains(result, "+ MODIFIED") {
		t.Errorf("diff should contain added line, got %q", result)
	}
	if !strings.Contains(result, "[Changes]") {
		t.Errorf("diff should contain [Changes] header, got %q", result)
	}
}

func TestCompactDiff_Addition(t *testing.T) {
	old := "func main() {\n}\n"
	new := "func main() {\n\tfmt.Println(\"hello\")\n}\n"
	result := compactDiff(old, new)
	if !strings.Contains(result, "+ \tfmt.Println") {
		t.Errorf("diff should contain added line, got %q", result)
	}
}

func TestCompactDiff_Truncation(t *testing.T) {
	// Generate a large change with 100 changed lines
	var oldLines, newLines []string
	for i := 0; i < 100; i++ {
		oldLines = append(oldLines, "old "+string(rune('a'+i%26))+string(rune('a'+i/26)))
		newLines = append(newLines, "new "+string(rune('a'+i%26))+string(rune('a'+i/26)))
	}
	old := strings.Join(oldLines, "\n") + "\n"
	new := strings.Join(newLines, "\n") + "\n"

	result := compactDiff(old, new)
	if result == "" {
		t.Fatal("expected non-empty diff")
	}
	// Should be truncated to maxEditDiffLines + header + truncation marker
	lines := strings.Split(result, "\n")
	if len(lines) > maxEditDiffLines+5 {
		t.Errorf("diff should be truncated to ~%d lines, got %d", maxEditDiffLines+5, len(lines))
	}
	if !strings.Contains(result, "more changed lines") {
		t.Errorf("diff should contain truncation notice, got %q", result)
	}
}

func TestCompactDiff_EmptyOld(t *testing.T) {
	result := compactDiff("", "new content\n")
	if result == "" {
		t.Fatal("expected non-empty diff for new content")
	}
	if !strings.Contains(result, "+ new content") {
		t.Errorf("diff should contain added line, got %q", result)
	}
}
