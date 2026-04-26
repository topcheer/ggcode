package tool

import (
	"strings"
	"testing"
)

func makeLines(n int) string {
	lines := make([]string, n)
	for i := 0; i < n; i++ {
		lines[i] = "line content here"
	}
	return strings.Join(lines, "\n")
}

func TestReadFileRangeFull(t *testing.T) {
	content := "line1\nline2\nline3"
	result := readFileRange(content, 0, 0, 0)
	if !strings.Contains(result, "     1\tline1") {
		t.Errorf("expected line 1, got:\n%s", result)
	}
	if !strings.Contains(result, "     2\tline2") {
		t.Errorf("expected line 2, got:\n%s", result)
	}
	if !strings.Contains(result, "     3\tline3") {
		t.Errorf("expected line 3, got:\n%s", result)
	}
}

func TestReadFileRangeWithOffset(t *testing.T) {
	content := "line1\nline2\nline3\nline4\nline5"
	result := readFileRange(content, 3, 0, 0)
	if strings.Contains(result, "line1") && strings.Contains(result, "     1\t") {
		t.Error("should not contain line 1 when offset=3")
	}
	if !strings.Contains(result, "     3\tline3") {
		t.Errorf("expected line 3 as first line, got:\n%s", result)
	}
	if !strings.Contains(result, "     5\tline5") {
		t.Errorf("expected line 5, got:\n%s", result)
	}
}

func TestReadFileRangeWithLimit(t *testing.T) {
	content := "line1\nline2\nline3\nline4\nline5"
	result := readFileRange(content, 1, 3, 0)
	if !strings.Contains(result, "     1\tline1") {
		t.Errorf("expected line 1, got:\n%s", result)
	}
	if !strings.Contains(result, "     3\tline3") {
		t.Errorf("expected line 3, got:\n%s", result)
	}
	if strings.Contains(result, "line4") {
		t.Error("should not contain line 4 with limit=3")
	}
}

func TestReadFileRangeTruncation(t *testing.T) {
	// Create content with more than maxOutputLines lines
	content := makeLines(2500)
	result := readFileRange(content, 0, 0, 0)
	if !strings.Contains(result, "File truncated") {
		t.Error("expected truncation notice")
	}
	if strings.Contains(result, "2001") {
		t.Error("should not contain line 2001 when capped at 2000")
	}
}

func TestReadFileRangeOffsetBeyondEnd(t *testing.T) {
	content := "line1\nline2\nline3"
	result := readFileRange(content, 100, 0, 0)
	if !strings.Contains(result, "beyond end") {
		t.Errorf("expected offset beyond end notice, got:\n%s", result)
	}
}

func TestReadFileRangeOffsetLimitCombo(t *testing.T) {
	content := makeLines(100)
	result := readFileRange(content, 10, 5, 0)
	if !strings.Contains(result, "    10\t") {
		t.Errorf("expected line 10, got:\n%s", result)
	}
	if !strings.Contains(result, "    14\t") {
		t.Errorf("expected line 14, got:\n%s", result)
	}
	if strings.Contains(result, "    15\t") {
		t.Error("should not contain line 15 with limit=5 starting at offset=10")
	}
}
