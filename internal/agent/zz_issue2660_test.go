package agent

// Issue #2660 regression tests: commentCodeIndicators keyword path lacked the
// prose guard that the call-pattern heuristic (#526) already had. Godoc prose
// containing keyword substrings ("if ", "case ", "new ", "return ") was
// flagged as commented-out code.

import (
	"strings"
	"testing"
)

// TestIssue2660KeywordPathProseGuard pins the direct unit behavior: prose
// containing a code-keyword substring must NOT look like code; genuine bare
// code statements still must.
func TestIssue2660KeywordPathProseGuard(t *testing.T) {
	prose := []string{
		// Each of these contains a keyword indicator as a substring but reads
		// as natural language (has prose signal words).
		"Print the value if it is set",
		"Returns a new connection for the pool",
		"Switch on the mode that was requested",
		"In this case we do nothing",
		"Defer the cleanup until the caller is done",
		"Delete any rows that are no longer needed",
		"Import the package before use",
		"This is a very long line of documentation prose mentioning class names",
	}
	for _, p := range prose {
		if looksLikeCode(p) {
			t.Errorf("looksLikeCode(%q) = true, want false (prose with keyword substring)", p)
		}
	}

	code := []string{
		// Genuine commented-out code: keyword hit without prose words.
		"return nil",
		"if err != nil",
		"for i := 0; i",
		"new(sync.Mutex)",
		"case 42:",
		"import \"fmt\"",
		"defer mu.Unlock()",
		"go worker(ctx)",
	}
	for _, c := range code {
		if !looksLikeCode(c) {
			t.Errorf("looksLikeCode(%q) = false, want true (real code statement)", c)
		}
	}
}

// TestIssue2660GodocProseBlockNotFlagged pins the end-to-end behavior from the
// issue: a 3+ line godoc-style prose comment containing keyword substrings on
// every line must NOT be reported as a commented-out code block.
func TestIssue2660GodocProseBlockNotFlagged(t *testing.T) {
	newContent := strings.Join([]string{
		"package foo",
		"",
		"// Run executes the task.",
		"// If the task is not found, it returns an error.",
		"// In that case the caller should retry with a new name.",
		"// Defer the decision to the caller when in doubt.",
		"func Run() {}",
	}, "\n")

	warnings := checkCommentedCodeBlocks("task.go", "", newContent)
	if len(warnings) != 0 {
		t.Errorf("checkCommentedCodeBlocks flagged prose doc block: %v", warnings)
	}
}

// TestIssue2660RealCommentedCodeStillFlagged pins the true-positive side: a
// 3+ line block of genuine commented-out code (keywords, no prose) is still
// reported - the guard suppresses prose only, not code.
func TestIssue2660RealCommentedCodeStillFlagged(t *testing.T) {
	newContent := strings.Join([]string{
		"package foo",
		"",
		"// result := compute(ctx)",
		"// if err != nil {",
		"// return err",
		"// }",
		"func F() {}",
	}, "\n")

	warnings := checkCommentedCodeBlocks("task.go", "", newContent)
	if len(warnings) == 0 {
		t.Error("checkCommentedCodeBlocks missed a real commented-out code block")
	}
}
