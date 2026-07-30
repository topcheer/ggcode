package tool

import (
	"strings"
	"testing"
)

// --- Conventional Commit Message Analysis ---

func TestAnalyzeCommitMessage_AlreadyConventional(t *testing.T) {
	msgs := []string{
		"feat: add user authentication",
		"fix(auth): handle nil pointer in session loader",
		"docs: update API documentation",
		"refactor!: change database schema",
		"chore(deps): bump golang to 1.26",
		"test: add unit tests for commit analyzer",
	}
	for _, msg := range msgs {
		if s := AnalyzeCommitMessage(msg); s != "" {
			t.Errorf("expected no suggestion for %q, got: %s", msg, s)
		}
	}
}

func TestAnalyzeCommitMessage_NonConventional(t *testing.T) {
	msg := "added new authentication handler with JWT support"
	s := AnalyzeCommitMessage(msg)
	if s == "" {
		t.Fatal("expected suggestion for non-conventional message")
	}
	if !strings.Contains(s, "feat") {
		t.Errorf("expected inferred type 'feat' in suggestion: %s", s)
	}
}

func TestAnalyzeCommitMessage_MergeRevertSkipped(t *testing.T) {
	msgs := []string{
		"Merge branch 'feature' into main",
		"Revert: removed broken feature",
	}
	for _, msg := range msgs {
		if s := AnalyzeCommitMessage(msg); s != "" {
			t.Errorf("expected no suggestion for merge/revert %q, got: %s", msg, s)
		}
	}
}

func TestAnalyzeCommitMessage_TooShortSkipped(t *testing.T) {
	if s := AnalyzeCommitMessage("fix bug"); s != "" {
		t.Errorf("expected no suggestion for short message, got: %s", s)
	}
}

func TestInferCommitType(t *testing.T) {
	cases := []struct {
		msg  string
		want string
	}{
		{"fix null pointer crash", "fix"},
		{"add new endpoint", "feat"},
		{"update README docs", "docs"},
		{"refactor and clean up code", "refactor"},
		{"add unit test", "test"},
		{"optimize performance and caching", "perf"},
		{"bump version and dependencies", "chore"},
		{"update CI pipeline", "ci"},
		{"something completely random", "feat"},
	}
	for _, tc := range cases {
		got := inferCommitType(tc.msg)
		if got != tc.want {
			t.Errorf("inferCommitType(%q) = %q, want %q", tc.msg, got, tc.want)
		}
	}
}

// --- File Categorization ---

func TestCategorizeFile(t *testing.T) {
	cases := []struct {
		path string
		want FileCategory
	}{
		{"main.go", CatSource},
		{"handler/auth.go", CatSource},
		{"app.test.js", CatTest},
		{"handler_test.go", CatTest},
		{"test_utils.py", CatTest},
		{"README.md", CatDocs},
		{"docs/guide.rst", CatDocs},
		{"config.yaml", CatConfig},
		{"docker-compose.yml", CatConfig},
		{"Dockerfile", CatConfig},
		{"package.json", CatConfig},
		{"image.png", CatOther},
		{"data.csv", CatOther},
	}
	for _, tc := range cases {
		got := categorizeFile(tc.path)
		if got != tc.want {
			t.Errorf("categorizeFile(%q) = %v, want %v", tc.path, got, tc.want)
		}
	}
}

// --- Diff Stats Parsing ---

func TestParseDiffStats(t *testing.T) {
	diff := `diff --git a/main.go b/main.go
--- a/main.go
+++ b/main.go
@@ -1,3 +1,5 @@
 existing
+added line 1
+added line 2
-removed line
diff --git a/config.yaml b/config.yaml
--- a/config.yaml
+++ b/config.yaml
@@ -1,1 +1,2 @@
 existing
+new: value
`
	stats := parseDiffStats(diff)
	if len(stats.files) != 2 {
		t.Fatalf("expected 2 files, got %d", len(stats.files))
	}
	if stats.additions != 3 {
		t.Errorf("expected 3 additions, got %d", stats.additions)
	}
	if stats.deletions != 1 {
		t.Errorf("expected 1 deletion, got %d", stats.deletions)
	}
}

func TestParseDiffStats_Empty(t *testing.T) {
	stats := parseDiffStats("")
	if len(stats.files) != 0 || stats.additions != 0 || stats.deletions != 0 {
		t.Errorf("expected zero stats for empty diff, got %+v", stats)
	}
}

// --- Commit Scope Analysis ---

func TestAnalyzeCommitScope_CleanDiff(t *testing.T) {
	diff := `diff --git a/main.go b/main.go
--- a/main.go
+++ b/main.go
@@ -1,1 +1,2 @@
 existing
+new code
`
	cohesion, size := AnalyzeCommitScope(diff)
	if cohesion != "" {
		t.Errorf("expected no cohesion warning for single-file diff, got: %s", cohesion)
	}
	if size != "" {
		t.Errorf("expected no size warning for small diff, got: %s", size)
	}
}

func TestAnalyzeCommitScope_TooManyFiles(t *testing.T) {
	var diff string
	for i := 0; i < 20; i++ {
		diff += "diff --git a/file" + string(rune('a'+i)) + ".go b/file" + string(rune('a'+i)) + ".go\n"
		diff += "--- a/file" + string(rune('a'+i)) + ".go\n"
		diff += "+++ b/file" + string(rune('a'+i)) + ".go\n"
		diff += "@@ -1,1 +1,2 @@\n old\n+change\n"
	}
	_, size := AnalyzeCommitScope(diff)
	if size == "" {
		t.Error("expected size warning for 20 files")
	}
	if !strings.Contains(size, "20 files") {
		t.Errorf("size warning should mention file count: %s", size)
	}
}

func TestAnalyzeCommitScope_TooManyLines(t *testing.T) {
	var diff string
	diff += "diff --git a/big.go b/big.go\n+++ b/big.go\n@@ -1,1 +1,1 @@\n old\n"
	for i := 0; i < 600; i++ {
		diff += "+new line\n"
	}
	_, size := AnalyzeCommitScope(diff)
	if size == "" {
		t.Error("expected size warning for 600+ line changes")
	}
}

func TestAnalyzeCommitScope_MixedConcerns(t *testing.T) {
	diff := `diff --git a/handler.go b/handler.go
--- a/handler.go
+++ b/handler.go
@@ -1,1 +1,2 @@
 code
+new code
diff --git a/README.md b/README.md
--- a/README.md
+++ b/README.md
@@ -1,1 +1,2 @@
 docs
+new docs
diff --git a/config.yaml b/config.yaml
--- a/config.yaml
+++ b/config.yaml
@@ -1,1 +1,2 @@
 config
+new config
diff --git a/migration.sql b/migration.sql
--- a/migration.sql
+++ b/migration.sql
@@ -1,1 +1,2 @@
 sql
+new sql
`
	cohesion, _ := AnalyzeCommitScope(diff)
	if cohesion == "" {
		t.Error("expected cohesion warning for mixed concerns")
	}
	for _, label := range []string{"source code", "documentation", "configuration"} {
		if !strings.Contains(cohesion, label) {
			t.Errorf("cohesion warning should mention %q: %s", label, cohesion)
		}
	}
}

func TestAnalyzeCommitScope_SourceAndTestOkay(t *testing.T) {
	// Source + test should NOT trigger cohesion warning.
	diff := `diff --git a/handler.go b/handler.go
--- a/handler.go
+++ b/handler.go
@@ -1,1 +1,2 @@
 code
+new code
diff --git a/handler_test.go b/handler_test.go
--- a/handler_test.go
+++ b/handler_test.go
@@ -1,1 +1,2 @@
 code
+new test
`
	cohesion, _ := AnalyzeCommitScope(diff)
	if cohesion != "" {
		t.Errorf("source + test should not trigger cohesion warning: %s", cohesion)
	}
}

func TestCombineScopeWarnings(t *testing.T) {
	if s := combineScopeWarnings("", ""); s != "" {
		t.Errorf("expected empty string for no warnings, got %q", s)
	}
	s := combineScopeWarnings("cohesion issue", "")
	if s != "cohesion issue" {
		t.Errorf("expected 'cohesion issue', got %q", s)
	}
	s = combineScopeWarnings("a", "b")
	if !strings.Contains(s, "a") || !strings.Contains(s, "b") {
		t.Errorf("expected both warnings combined, got %q", s)
	}
}
