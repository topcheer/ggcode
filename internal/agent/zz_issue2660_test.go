package agent

import (
	"strings"
	"testing"
)

func TestIssue2660KeywordPathProseGuard(t *testing.T) {
	prose := []string{

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
