package agent

import (
	"strings"
	"sync"
)

// ErrorClassifier provides error-type-specific guidance for tool failures.
//
// Research basis: AgentDebug (arXiv:2509.25370) found that targeted feedback
// generated from failure analysis yields up to 26% relative improvement in
// task success across benchmarks. The key insight is that error-type-specific
// guidance (e.g. "file not found → check path") is far more effective than
// generic "reconsider your strategy" advice, especially when delivered
// immediately on the first error rather than waiting for a streak.
//
// Design:
//   - classifyToolError categorizes errors into known patterns
//   - Each category has specific, actionable guidance
//   - Guidance is injected on the FIRST error of each category (not waiting
//     for a streak of 4), giving the agent immediate direction
//   - Each category fires at most once per run (deduplication)
//   - The existing error-streak system (4/7/10 errors) still fires for
//     cumulative failures, providing escalating guidance for sustained problems
type ErrorClassifier struct {
	mu sync.Mutex

	// fired tracks which categories have already provided guidance.
	// Each category fires at most once per run.
	fired map[string]bool
}

// ErrorCategory represents a classified error type.
type ErrorCategory struct {
	Name     string
	Guidance string
}

func NewErrorClassifier() *ErrorClassifier {
	return &ErrorClassifier{
		fired: make(map[string]bool),
	}
}

func (ec *ErrorClassifier) reset() {
	ec.mu.Lock()
	defer ec.mu.Unlock()
	ec.fired = make(map[string]bool)
}

// classifyToolError examines a tool error result and returns a category with
// targeted guidance. Returns empty Name if the error doesn't match a known
// pattern or if guidance for this category was already fired.
//
// The result content is the full error output from the tool.
func (ec *ErrorClassifier) classifyToolError(toolName, resultContent string) ErrorCategory {
	ec.mu.Lock()
	defer ec.mu.Unlock()

	cat := classifyErrorContent(toolName, resultContent)
	if cat.Name == "" {
		return ErrorCategory{}
	}

	// Each category fires at most once per run
	if ec.fired[cat.Name] {
		return ErrorCategory{}
	}
	ec.fired[cat.Name] = true

	return cat
}

// classifyErrorContent performs the actual pattern matching.
// Exported for testing (package-private).
func classifyErrorContent(toolName, content string) ErrorCategory {
	c := strings.ToLower(content)

	// Order matters: most specific patterns first

	// File not found / does not exist
	if errorContainsAny(c,
		"no such file or directory",
		"file does not exist",
		"file not found",
		"does not exist",
		// "cannot find" is too generic — "cannot find package" should match import_error
		"no such file",
		"no such file",
	) {
		return ErrorCategory{
			Name: "file_not_found",
			Guidance: "The file or path does not exist. " +
				"Check the path spelling, use glob or list_directory to find the correct path, " +
				"or check if you're in the correct working directory.",
		}
	}

	// Permission denied
	if errorContainsAny(c,
		"permission denied",
		"access denied",
		"access is denied",
		"operation not permitted",
		"unauthorized",
	) {
		return ErrorCategory{
			Name: "permission_denied",
			Guidance: "Permission denied. Check file permissions, " +
				"ensure the process has write access, or try a different output path. " +
				"Do not retry the same operation — it will fail again.",
		}
	}

	// Nil pointer / panic
	if errorContainsAny(c,
		"nil pointer",
		"panic:",
		"runtime error: invalid memory address",
		"segmentation fault",
	) {
		return ErrorCategory{
			Name: "nil_pointer",
			Guidance: "A nil pointer or panic occurred. This usually means a variable " +
				"wasn't initialized before use. Check for missing initialization, " +
				"nil checks before method calls, or incorrect load order.",
		}
	}

	// Type mismatch / type errors
	if errorContainsAny(c,
		"cannot use",
		"as type",
		"type mismatch",
		"incompatible types",
		"undefined:",
		"undefined symbol",
		"not a function",
		"not defined",
	) {
		return ErrorCategory{
			Name: "type_error",
			Guidance: "A type or symbol error occurred. This often happens when " +
				"code has been refactored — symbols renamed, functions moved, " +
				"or signatures changed. Re-read the current file and check types. " +
				"In a shared workspace, another agent may have renamed symbols.",
		}
	}

	// Import / dependency errors
	if errorContainsAny(c,
		"cannot find package",
		"no required module provides",
		"import cycle",
		"missing go module",
		"module not found",
		"unresolved import",
		"cannot find module",
	) {
		return ErrorCategory{
			Name: "import_error",
			Guidance: "An import or dependency error occurred. Check if the package " +
				"name is correct, run go mod tidy to sync dependencies, " +
				"or check for build tags (e.g. -tags goolm).",
		}
	}

	// Syntax errors
	if errorContainsAny(c,
		"syntax error",
		"unexpected token",
		"unexpected character",
		"expected ';'",
		"expected ','",
		"unexpected end of file",
		"unterminated",
		"malformed",
	) {
		return ErrorCategory{
			Name: "syntax_error",
			Guidance: "A syntax error was detected. Check for unbalanced brackets, " +
				"missing commas, unterminated strings, or incorrect indentation. " +
				"Read the error location carefully and fix the syntax.",
		}
	}

	// Timeout / deadline exceeded
	if errorContainsAny(c,
		"timeout",
		"timed out",
		"deadline exceeded",
		"context deadline exceeded",
		"connection refused",
		"connection reset",
		"no such host",
		"i/o timeout",
	) {
		return ErrorCategory{
			Name: "timeout_network",
			Guidance: "A timeout or network error occurred. This is often transient — " +
				"the service may be temporarily unavailable. Wait a moment and retry, " +
				"or check if the target service/process is running.",
		}
	}

	// Build/compile errors (specific to Go)
	if errorContainsAny(c,
		"build failed",
		"compilation failed",
		"not enough arguments",
		"too many arguments",
		"declared and not used",
		"assigned but not used",
		"mismatched types",
	) {
		return ErrorCategory{
			Name: "build_error",
			Guidance: "A build or compilation error occurred. Read the full error " +
				"output to identify the specific issue. Common causes: unused variables, " +
				"wrong number of arguments, type mismatches after refactoring.",
		}
	}

	// Edit-specific: anchor text not found
	if toolName == "edit_file" || toolName == "multi_edit_file" {
		if errorContainsAny(c,
			"old_text",
			"not found",
			"no match",
			"anchor",
			"whitespace",
			"indentation",
			"does not match",
		) {
			return ErrorCategory{
				Name: "edit_anchor_mismatch",
				Guidance: "The edit anchor (old_text) was not found in the file. " +
					"The file content may have changed since you last read it. " +
					"Re-read the file to get the current content, then retry the edit " +
					"with the exact text from the file.",
			}
		}
	}

	// Test failures
	if strings.Contains(toolName, "test") || errorContainsAny(c,
		"test failed",
		"assertion failed",
		"expected:",
		"got:",
		"--- fail",
	) {
		return ErrorCategory{
			Name: "test_failure",
			Guidance: "A test failed. Read the full test output to understand which " +
				"assertion failed and why. Compare expected vs actual values carefully. " +
				"If the test itself is wrong (testing old behavior), update the test.",
		}
	}

	// Generic fallback — no specific pattern matched
	return ErrorCategory{Name: ""}
}

// errorContainsAny checks if the string contains any of the substrings.
func errorContainsAny(s string, subs ...string) bool {
	for _, sub := range subs {
		if strings.Contains(s, sub) {
			return true
		}
	}
	return false
}
