package diff

import (
	"strings"
	"testing"
)

// TestSplitLinesEmptyReturnsNil covers #537 Bug A: splitLines("") must yield
// zero lines, not [""] — otherwise every diff/stat against an empty side
// reports a phantom deletion of one empty line.
func TestSplitLinesEmptyReturnsNil(t *testing.T) {
	got := splitLines("")
	if got == nil {
		return // nil is the intended result
	}
	if len(got) != 0 {
		t.Fatalf("splitLines(\"\") = %q (len %d), want nil/empty", got, len(got))
	}
}

// TestCountChangesEmptyToContent covers #537 Bug A at the public API level:
// creating a file (old="") must be pure additions, no phantom deletion.
func TestCountChangesEmptyToContent(t *testing.T) {
	additions, deletions := CountChanges("", "a")
	if additions != 1 || deletions != 0 {
		t.Fatalf("CountChanges(\"\", \"a\") = +%d -%d, want +1 -0 (no phantom deletion)", additions, deletions)
	}

	additions, deletions = CountChanges("", "line1\nline2\n")
	if additions != 2 || deletions != 0 {
		t.Fatalf("CountChanges(\"\", \"line1\\nline2\\n\") = +%d -%d, want +2 -0", additions, deletions)
	}

	// Deleting all content must be pure deletions, no phantom addition.
	additions, deletions = CountChanges("a", "")
	if additions != 0 || deletions != 1 {
		t.Fatalf("CountChanges(\"a\", \"\") = +%d -%d, want +0 -1", additions, deletions)
	}
}

// TestUnifiedDiffEmptyToContentNoPhantomDeletion covers #537 Bug A in the
// rendered diff: creating a new file must show only "+" lines.
func TestUnifiedDiffEmptyToContentNoPhantomDeletion(t *testing.T) {
	result := UnifiedDiff("", "hello\nworld\n", 3)
	if strings.Contains(result, "- ") {
		t.Fatalf("expected no deletion lines for file creation, got:\n%s", result)
	}
	if !strings.Contains(result, "+ hello") || !strings.Contains(result, "+ world") {
		t.Fatalf("expected addition lines, got:\n%s", result)
	}
}

// TestUnifiedDiffHunkHeadersHaveLineNumbers covers #537 Bug B: hunk headers
// must be standard "@@ -l,s +l,s @@", not bare "@@".
func TestUnifiedDiffHunkHeadersHaveLineNumbers(t *testing.T) {
	old := "line1\nline2\nline3\nline4\nline5\n"
	new := "line1\nmodified\nline3\nline4\nchanged\n"
	result := UnifiedDiff(old, new, 1)

	if !strings.Contains(result, "@@ -") || !strings.Contains(result, " @@") {
		t.Fatalf("expected standard hunk headers \"@@ -l,s +l,s @@\", got:\n%s", result)
	}
	for _, ln := range strings.Split(result, "\n") {
		if ln == "" {
			continue
		}
		if ln == "@@" {
			t.Fatalf("bare \"@@\" marker leaked (invalid unified diff):\n%s", result)
		}
	}

	// Single hunk starting at line 1 with old size 3, new size 3
	// (context 1: " line1", "-line2", "+modified", " line3" then a second
	// hunk for the tail change). At minimum, the first header must anchor
	// old start line 1 and new start line 1.
	if !strings.Contains(result, "@@ -1,") || !strings.Contains(result, " +1,") {
		t.Fatalf("expected first hunk header anchored at line 1, got:\n%s", result)
	}
}

// TestUnifiedDiffMultipleHunksEachHeadered covers #537 Bug B: a diff with
// two separated change regions produces two hunks, each with its own
// line-anchored header.
func TestUnifiedDiffMultipleHunksEachHeadered(t *testing.T) {
	old := "a\nb\nc\nd\ne\nf\ng\nh\n"
	new := "a\nB\nc\nd\ne\nf\ng\nH\n"
	result := UnifiedDiff(old, new, 0)

	headers := 0
	for _, ln := range strings.Split(result, "\n") {
		if strings.HasPrefix(ln, "@@ -") {
			headers++
		}
	}
	if headers != 2 {
		t.Fatalf("expected 2 hunk headers for two separated changes, got %d:\n%s", headers, result)
	}
	if !strings.Contains(result, "@@ -2,1 +2,1 @@") {
		t.Fatalf("expected header \"@@ -2,1 +2,1 @@\" for line-2 change, got:\n%s", result)
	}
}

// TestUnifiedDiffCreationHunkHeader covers #537 Bug B at the idx-0 edge: a
// hunk beginning at the very first line (file creation) still gets a header.
func TestUnifiedDiffCreationHunkHeader(t *testing.T) {
	result := UnifiedDiff("", "brand new\ncontent\n", 1)
	if !strings.Contains(result, "@@ -0,0 +1,2 @@") {
		t.Fatalf("expected creation header \"@@ -0,0 +1,2 @@\", got:\n%s", result)
	}
}
