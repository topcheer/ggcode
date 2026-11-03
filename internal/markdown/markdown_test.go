package markdown

import (
	"strings"
	"testing"
)

func TestNormalizePreservesHeadingBlockBoundaryBeforeOrderedList(t *testing.T) {
	input := "## Phase 2 — 短期修复（3-5 天）\n5. S01 + S02\n6. C-2\n"
	normalized := Normalize(input)
	if !strings.Contains(normalized, "Phase 2 — 短期修复（3-5 天）\n\n5. S01 + S02") {
		t.Fatalf("expected blank line between normalized heading and ordered list, got %q", normalized)
	}
}

func TestNormalizePreservesHeadingBlockBoundaryAfterParagraph(t *testing.T) {
	input := "Intro paragraph\n## Phase 3\n10. ARCH-01\n"
	normalized := Normalize(input)

	if !strings.Contains(normalized, "Intro paragraph\n\n## Phase 3\n\n10. ARCH-01") {
		t.Fatalf("expected normalized heading to stay isolated as a block, got %q", normalized)
	}
}

func TestRenderNormalizesControlChars(t *testing.T) {
	out := Render("line one\r\nline two\rX", 40)
	if strings.ContainsRune(out, '\r') {
		t.Errorf("CR survived Render: %q", out)
	}

	code := "```go\nfunc main() {\n\treturn\n}\n```"
	out = Render(code, 40)
	if strings.ContainsRune(out, '\t') {
		t.Errorf("TAB survived Render in fenced code block: %q", out)
	}
	if !strings.Contains(out, "    return") {
		t.Errorf("TAB not expanded to spaces in code block: %q", out)
	}

	out = Render("plain text", 40)
	if !strings.Contains(out, "plain text") {
		t.Errorf("plain text mangled: %q", out)
	}
}
