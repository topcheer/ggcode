package tool

import (
	"strings"
	"testing"
)

func TestScanStagedDiffForIssues_EmptyInput(t *testing.T) {
	issues := ScanStagedDiffForIssues("")
	if issues != nil {
		t.Errorf("expected nil for empty input, got %d issues", len(issues))
	}

	issues = ScanStagedDiffForIssues("   \n  \n")
	if issues != nil {
		t.Errorf("expected nil for whitespace-only input, got %d issues", len(issues))
	}
}

func TestScanStagedDiffForIssues_DebugStmt(t *testing.T) {
	diff := `diff --git a/main.go b/main.go
--- a/main.go
+++ b/main.go
@@ -10,4 +10,6 @@
 func main() {
+	fmt.Println("debug value:", x)
 	doSomething()
+	fmt.Printf("x=%d\n", x)
 }
`
	issues := ScanStagedDiffForIssues(diff)
	if len(issues) != 2 {
		t.Fatalf("expected 2 debug-stmt issues, got %d: %+v", len(issues), issues)
	}

	for _, iss := range issues {
		if iss.Category != "debug-stmt" {
			t.Errorf("expected category 'debug-stmt', got %q", iss.Category)
		}
		if iss.File != "main.go" {
			t.Errorf("expected file 'main.go', got %q", iss.File)
		}
	}
}

func TestScanStagedDiffForIssues_DebugStmtJavaScript(t *testing.T) {
	diff := `diff --git a/app.js b/app.js
--- a/app.js
+++ b/app.js
@@ -5,3 +5,5 @@
 function run() {
+	console.log("starting app")
+	console.debug("debug info")
 }
`
	issues := ScanStagedDiffForIssues(diff)
	if len(issues) != 2 {
		t.Fatalf("expected 2 issues, got %d", len(issues))
	}
}

func TestScanStagedDiffForIssues_TestFileExcluded(t *testing.T) {
	diff := `diff --git a/handler_test.go b/handler_test.go
--- a/handler_test.go
+++ b/handler_test.go
@@ -10,4 +10,6 @@
 func TestHandler(t *testing.T) {
+	fmt.Println("debug")
 	if true {
+		fmt.Printf("val=%v\n", x)
 	}
 }
`
	issues := ScanStagedDiffForIssues(diff)
	// Debug statements in test files should NOT be flagged
	for _, iss := range issues {
		if iss.Category == "debug-stmt" {
			t.Errorf("debug-stmt should not be flagged in test files: %+v", iss)
		}
	}
}

func TestScanStagedDiffForIssues_MergeConflict(t *testing.T) {
	diff := `diff --git a/config.go b/config.go
--- a/config.go
+++ b/config.go
@@ -5,3 +5,7 @@
 func load() {
+<<<<<<< HEAD
+	timeout = 30
+=======
+	timeout = 60
+>>>>>>> feature-branch
 }
`
	issues := ScanStagedDiffForIssues(diff)
	found := false
	for _, iss := range issues {
		if iss.Category == "merge-conflict" {
			found = true
			if iss.Severity != "warning" {
				t.Errorf("expected warning severity for merge conflict, got %q", iss.Severity)
			}
		}
	}
	if !found {
		t.Errorf("expected at least one merge-conflict issue, got %+v", issues)
	}
}

func TestScanStagedDiffForIssues_TODOMarker(t *testing.T) {
	diff := `diff --git a/server.go b/server.go
--- a/server.go
+++ b/server.go
@@ -20,3 +20,4 @@
 func handle() {
+	// TODO: add error handling
 }
`
	issues := ScanStagedDiffForIssues(diff)
	found := false
	for _, iss := range issues {
		if iss.Category == "todo" {
			found = true
		}
	}
	if !found {
		t.Errorf("expected a todo issue, got %+v", issues)
	}
}

func TestScanStagedDiffForIssues_DebuggerBreakpoint(t *testing.T) {
	tests := []struct {
		name string
		line string
	}{
		{"pdb", "+	import pdb; pdb.set_trace()"},
		{"breakpoint", "+	breakpoint()"},
		{"js_debugger", "+	debugger;"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			diff := "diff --git a/code.py b/code.py\n+++ b/code.py\n@@ -1,1 +1,2 @@\n old\n" + tt.line + "\n"
			issues := ScanStagedDiffForIssues(diff)
			found := false
			for _, iss := range issues {
				if iss.Category == "debugger" {
					found = true
				}
			}
			if !found {
				t.Errorf("expected debugger issue for %q, got %+v", tt.name, issues)
			}
		})
	}
}

func TestScanStagedDiffForIssues_ContextLinesIgnored(t *testing.T) {
	// Context lines (starting with space) should not be flagged.
	diff := `diff --git a/main.go b/main.go
+++ b/main.go
@@ -1,3 +1,3 @@
 fmt.Println("existing")
 fmt.Println("also existing")
 doSomething()
`
	issues := ScanStagedDiffForIssues(diff)
	// These are context lines (start with space), not additions
	for _, iss := range issues {
		if iss.Category == "debug-stmt" {
			t.Errorf("context lines should not be flagged: %+v", iss)
		}
	}
}

func TestScanStagedDiffForIssues_RemovedLinesIgnored(t *testing.T) {
	// Removed lines (starting with -) should not be flagged.
	diff := `diff --git a/main.go b/main.go
+++ b/main.go
@@ -1,3 +1,2 @@
-fmt.Println("removed debug")
 doSomething()
 realCode()
`
	issues := ScanStagedDiffForIssues(diff)
	for _, iss := range issues {
		if iss.Category == "debug-stmt" {
			t.Errorf("removed lines should not be flagged: %+v", iss)
		}
	}
}

func TestScanStagedDiffForIssues_MultipleFiles(t *testing.T) {
	diff := `diff --git a/a.go b/a.go
+++ b/a.go
@@ -1,1 +1,2 @@
 old
+	fmt.Println("a")
diff --git a/b.go b/b.go
+++ b/b.go
@@ -1,1 +1,2 @@
 old
+	fmt.Println("b")
`
	issues := ScanStagedDiffForIssues(diff)
	if len(issues) != 2 {
		t.Fatalf("expected 2 issues across files, got %d", len(issues))
	}

	files := map[string]bool{}
	for _, iss := range issues {
		files[iss.File] = true
	}
	if len(files) != 2 {
		t.Errorf("expected issues from 2 files, got %d: %v", len(files), files)
	}
}

func TestScanStagedDiffForIssues_LineNumbers(t *testing.T) {
	// Verify that line numbers are correctly tracked.
	diff := `diff --git a/main.go b/main.go
+++ b/main.go
@@ -10,3 +10,5 @@
 line10
+	fmt.Println("line11")
 line12
+	fmt.Println("line13")
`
	issues := ScanStagedDiffForIssues(diff)
	if len(issues) != 2 {
		t.Fatalf("expected 2 issues, got %d", len(issues))
	}

	if issues[0].Line != 11 {
		t.Errorf("expected first issue at line 11, got %d", issues[0].Line)
	}
	if issues[1].Line != 13 {
		t.Errorf("expected second issue at line 13, got %d", issues[1].Line)
	}
}

func TestScanStagedDiffForIssues_MaxIssuesCap(t *testing.T) {
	// Generate a diff with many debug statements to verify the cap.
	var diff string
	diff += "diff --git a/big.go b/big.go\n+++ b/big.go\n@@ -1,1 +1,1 @@\n old\n"
	for i := 0; i < 50; i++ {
		diff += "+	fmt.Println(\"debug\")\n"
	}
	issues := ScanStagedDiffForIssues(diff)
	if len(issues) > maxDiffScanIssues {
		t.Errorf("expected at most %d issues, got %d", maxDiffScanIssues, len(issues))
	}
}

func TestFormatDiffIssues_NoIssues(t *testing.T) {
	result := FormatDiffIssues(nil)
	if result != "" {
		t.Errorf("expected empty string for no issues, got %q", result)
	}
}

func TestFormatDiffIssues_WithWarnings(t *testing.T) {
	issues := []DiffIssue{
		{File: "main.go", Line: 10, Severity: "warning", Category: "merge-conflict", Message: "Unresolved merge conflict marker."},
		{File: "main.go", Line: 15, Severity: "info", Category: "debug-stmt", Message: "Debug print statement."},
	}
	result := FormatDiffIssues(issues)
	if result == "" {
		t.Fatal("expected non-empty result")
	}
	if !strings.Contains(result, "1 warning") {
		t.Errorf("expected '1 warning' in output: %s", result)
	}
	if !strings.Contains(result, "Pre-commit scan") {
		t.Errorf("expected 'Pre-commit scan' header: %s", result)
	}
	if !strings.Contains(result, "main.go:10") {
		t.Errorf("expected file:line reference 'main.go:10': %s", result)
	}
}

func TestFormatDiffIssues_OnlyInfo(t *testing.T) {
	issues := []DiffIssue{
		{File: "app.js", Line: 5, Severity: "info", Category: "debug-stmt", Message: "Debug print."},
	}
	result := FormatDiffIssues(issues)
	if strings.Contains(result, "review before pushing") {
		t.Errorf("should not show 'review before pushing' for info-only: %s", result)
	}
	if !strings.Contains(result, "1 info") {
		t.Errorf("expected '1 info' in output: %s", result)
	}
}

// TestScanStagedDiffForIssues_RealWorldDiff tests with a realistic diff.
func TestScanStagedDiffForIssues_RealWorldDiff(t *testing.T) {
	diff := `diff --git a/internal/handler/auth.go b/internal/handler/auth.go
index 1a2b3c4..5d6e7f8 100644
--- a/internal/handler/auth.go
+++ b/internal/handler/auth.go
@@ -30,6 +30,8 @@ import (
 )
 
 func authenticate(token string) error {
+	// FIXME: this doesn't handle expired tokens yet
+	fmt.Printf("token=%s\n", token)
 	if token == "" {
 		return ErrEmptyToken
 	}
@@ -45,4 +47,3 @@ func validate(t string) bool {
-	oldValidationLogic(t)
+	newValidationLogic(t)
 }
diff --git a/config.yml b/config.yml
--- a/config.yml
+++ b/config.yml
@@ -5,3 +5,5 @@ server:
+  api_key: "AKIA1234567890ABCDEF"
+  timeout: 30
`
	issues := ScanStagedDiffForIssues(diff)

	categories := map[string]int{}
	for _, iss := range issues {
		categories[iss.Category]++
	}

	// Should detect: debug-stmt, todo (FIXME), and possibly secret
	if categories["debug-stmt"] == 0 {
		t.Error("expected at least one debug-stmt issue")
	}
	if categories["todo"] == 0 {
		t.Error("expected at least one todo issue (FIXME)")
	}
}

// TestScanStagedDiffForIssues_DiffHeaderSkipped tests that "+++" and "---"
// lines are not mistakenly treated as added content.
func TestScanStagedDiffForIssues_DiffHeaderSkipped(t *testing.T) {
	diff := `diff --git a/main_test.go b/main_test.go
--- a/main_test.go
+++ b/main_test.go
@@ -1,1 +1,1 @@
-something
+something else
`
	issues := ScanStagedDiffForIssues(diff)
	// The "+++ b/main_test.go" line should not be treated as content.
	// No issues expected since it's a test file and no debug/merge patterns.
	for _, iss := range issues {
		if iss.Category == "debug-stmt" {
			t.Errorf("test file should not trigger debug-stmt: %+v", iss)
		}
	}
}

// TestScanStagedDiffForIssues_HunkWithoutLineCount tests hunk headers
// that omit the count (e.g. @@ -5 +5 @@).
func TestScanStagedDiffForIssues_HunkWithoutLineCount(t *testing.T) {
	diff := `diff --git a/main.go b/main.go
+++ b/main.go
@@ -5 +5 @@
 code
+	fmt.Println("debug")
`
	issues := ScanStagedDiffForIssues(diff)
	if len(issues) != 1 {
		t.Fatalf("expected 1 issue, got %d", len(issues))
	}
	if issues[0].Category != "debug-stmt" {
		t.Errorf("expected debug-stmt, got %q", issues[0].Category)
	}
}
