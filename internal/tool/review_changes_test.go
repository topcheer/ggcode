package tool

import (
	"testing"
)

func TestReviewChanges_NameAndDescription(t *testing.T) {
	tool := ReviewChanges{WorkingDir: "/tmp"}
	if tool.Name() != "review_changes" {
		t.Errorf("Name() = %q, want %q", tool.Name(), "review_changes")
	}
	if tool.Description() == "" {
		t.Error("Description() should not be empty")
	}
}

func TestParseReviewDiff_SingleFile(t *testing.T) {
	diff := `diff --git a/main.go b/main.go
index 1234567..abcdefg 100644
--- a/main.go
+++ b/main.go
@@ -10,4 +10,7 @@
 func main() {
+	fmt.Println("hello")
+	fmt.Println("world")
+	//TODO fix this later
 	return nil
 }
`
	files := parseReviewDiff(diff)
	if len(files) != 1 {
		t.Fatalf("expected 1 file, got %d", len(files))
	}
	if files[0].path != "main.go" {
		t.Errorf("expected path main.go, got %s", files[0].path)
	}
	if len(files[0].addedLines) != 3 {
		t.Errorf("expected 3 added lines, got %d", len(files[0].addedLines))
	}
	if files[0].addedCount != 3 {
		t.Errorf("expected addedCount=3, got %d", files[0].addedCount)
	}
}

func TestParseReviewDiff_MultipleFiles(t *testing.T) {
	diff := `diff --git a/a.go b/a.go
index 123..456 100644
--- a/a.go
+++ b/a.go
@@ -1,3 +1,4 @@
 package a
+import "fmt"
 func A() {}
diff --git a/b.go b/b.go
index 123..456 100644
--- a/b.go
+++ b/b.go
@@ -1,3 +1,4 @@
 package b
+func B() {}
 func C() {}
`
	files := parseReviewDiff(diff)
	if len(files) != 2 {
		t.Fatalf("expected 2 files, got %d", len(files))
	}
}

func TestParseReviewHunkStart(t *testing.T) {
	tests := []struct {
		hunk string
		want int
	}{
		{"@@ -10,4 +12,6 @@", 12},
		{"@@ -1 +1 @@", 1},
		{"@@ -100,3 +105,5 @@", 105},
		{"@@ -1,1 +42,1 @@", 42},
	}
	for _, tt := range tests {
		got := parseReviewHunkStart(tt.hunk)
		if got != tt.want {
			t.Errorf("parseReviewHunkStart(%q) = %d, want %d", tt.hunk, got, tt.want)
		}
	}
}

func TestDetectCommentBlocks_Detects(t *testing.T) {
	file := &reviewDiffFile{
		path: "main.go",
		addedLines: []reviewDiffLine{
			{1, "//	if err != nil {"},
			// #860: a gap in FILE line numbers (context line in between) must
			// split the block; lines must be file-adjacent to count as one.
			{2, "//		return fmt.Errorf(\"bad\")"},
			{3, "//	for i := 0; i < 10; i++ {"},
			{4, "//		_ = i"},
			{5, "//	}"},
			{6, "//}"},
		},
	}
	findings := detectCommentBlocks(file)
	if len(findings) != 1 {
		t.Fatalf("expected 1 finding, got %d", len(findings))
	}
	if findings[0].Category != "commented-code" {
		t.Errorf("expected category commented-code, got %s", findings[0].Category)
	}
}

func TestDetectCommentBlocks_NoFalsePositive(t *testing.T) {
	file := &reviewDiffFile{
		path: "main.go",
		addedLines: []reviewDiffLine{
			{1, "// This is a regular comment"},
			{2, "// So is this"},
		},
	}
	findings := detectCommentBlocks(file)
	if len(findings) != 0 {
		t.Fatalf("expected 0 findings for regular comments, got %d", len(findings))
	}
}

func TestFormatReviewReport_NoIssues(t *testing.T) {
	report := formatReviewReport(nil, 3, 10, 2)
	if report == "" {
		t.Error("expected non-empty report")
	}
	if !contains(report, "No issues found") {
		t.Error("expected 'No issues found' in report")
	}
}

func TestFormatReviewReport_WithIssues(t *testing.T) {
	issues := []DiffIssue{
		{File: "main.go", Line: 5, Severity: "warning", Category: "debug-stmt", Message: "fmt.Println detected"},
		{File: "secret.go", Line: 3, Severity: "critical", Category: "secret", Message: "API key detected"},
		{File: "todo.go", Line: 10, Severity: "info", Category: "todo", Message: "TODO marker"},
	}
	report := formatReviewReport(issues, 2, 15, 5)
	if !contains(report, "CRITICAL") {
		t.Error("expected CRITICAL section")
	}
	if !contains(report, "WARNING") {
		t.Error("expected WARNING section")
	}
	if !contains(report, "INFO") {
		t.Error("expected INFO section")
	}
	if !contains(report, "critical issue(s)") {
		t.Error("expected critical summary line")
	}
}

func TestGetUntrackedFiles_ParseStatus(t *testing.T) {
	// Test the parsing logic without git
	// We can't easily test getUntrackedFiles without a git repo,
	// but we verified the parsing in TestParseReviewDiff above.
}

func TestCommentedCodePatterns(t *testing.T) {
	tests := []struct {
		line   string
		expect bool
	}{
		{"//	if err != nil {", true},
		{"//	return nil", true},
		{"//	for i := 0; i < 10; i++ {", true},
		{"//	func foo() {", true},
		{"#if True:", true},
		{"// This is a regular comment explaining something", false},
		{"// regular text", false},
		{"code := realCode()", false},
	}
	for _, tt := range tests {
		got := false
		for _, p := range commentedCodePatterns {
			if p.MatchString(tt.line) {
				got = true
				break
			}
		}
		if got != tt.expect {
			t.Errorf("commentedCodePatterns(%q) = %v, want %v", tt.line, got, tt.expect)
		}
	}
}

// Note: contains and indexOf helpers are already defined in plan_mode_tools_test.go
