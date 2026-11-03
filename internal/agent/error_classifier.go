package agent

import (
	"regexp"
	"strings"
	"sync"
)

var compileMissingFileRe = regexp.MustCompile(`open \S+\.go: no such file or directory`)

type ErrorClassifier struct {
	mu sync.Mutex

	fired map[string]bool
}

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

func (ec *ErrorClassifier) firedCategories() []string {
	ec.mu.Lock()
	defer ec.mu.Unlock()
	out := make([]string, 0, len(ec.fired))
	for name := range ec.fired {
		out = append(out, name)
	}
	return out
}

func (ec *ErrorClassifier) classifyToolError(toolName, resultContent string) ErrorCategory {
	ec.mu.Lock()
	defer ec.mu.Unlock()

	cat := classifyErrorContent(toolName, resultContent)
	if cat.Name == "" {
		return ErrorCategory{}
	}

	if ec.fired[cat.Name] {
		return ErrorCategory{}
	}
	ec.fired[cat.Name] = true

	return cat
}

func classifyErrorContent(toolName, content string) ErrorCategory {
	c := strings.ToLower(content)

	if errorContainsAny(c,
		"unauthorized",
		"http 401",
		"401 unauthorized",
		"authentication failed",
		"authentication required",
		"invalid api key",
		"token expired",
	) {
		return ErrorCategory{
			Name: "auth_error",
			Guidance: "An authentication error occurred (401/unauthorized/expired token). " +
				"The API key or access token is missing, invalid, or expired - check and " +
				"rotate/update the credential (e.g. re-login or update the key in config), " +
				"then retry the operation.",
		}
	}

	if (toolName == "run_command" || toolName == "start_command" ||
		toolName == "wait_command" || toolName == "read_command_output") &&
		compileMissingFileRe.MatchString(c) {
		return ErrorCategory{
			Name: "build_error",
			Guidance: "A build error occurred: the compiler cannot open a .go file - a package " +
				"references a deleted or renamed file. Fix the import/package declaration " +
				"or restore the file; checking your own path spelling will not help.",
		}
	}

	if errorContainsAny(c,
		"no such file or directory",
		"file does not exist",
		"file not found",
		"does not exist",

		"no such file",
	) {
		return ErrorCategory{
			Name: "file_not_found",
			Guidance: "The file or path does not exist. " +
				"Check the path spelling, use glob or list_directory to find the correct path, " +
				"or check if you're in the correct working directory.",
		}
	}

	if errorContainsAny(c,
		"permission denied",
		"access denied",
		"access is denied",
		"operation not permitted",
	) {
		return ErrorCategory{
			Name: "permission_denied",
			Guidance: "Permission denied. Check file permissions, " +
				"ensure the process has write access, or try a different output path. " +
				"Do not retry the same operation — it will fail again.",
		}
	}

	if errorContainsAny(c,
		"panic: test timed out",
		"test timed out after",
	) {
		return ErrorCategory{
			Name: "test_failure",
			Guidance: "A test failed. Read the full test output to understand which " +
				"assertion failed and why. Compare expected vs actual values carefully. " +
				"If the test itself is wrong (testing old behavior), update the test.",
		}
	}

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

	if errorContainsAny(c,
		"index.lock",
		"another git process",
		"unable to create '.git/index.lock'",
		"fatal: unable to write new index file",
	) {
		return ErrorCategory{
			Name: "git_lock_contention",
			Guidance: "Another git process is running or left a stale index.lock. " +
				"This is common in multi-agent workflows. Wait a few seconds for the " +
				"other process to finish, then retry. If it persists, the lock may be " +
				"stale (from a crashed process) - remove .git/index.lock with rm, then retry.",
		}
	}

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

	if strings.Contains(toolName, "test") || errorContainsAny(c,
		"test failed",
		"assertion failed",
		"expected:",
		"got:",
		"--- fail",
		"panic: test timed out",
	) {
		return ErrorCategory{
			Name: "test_failure",
			Guidance: "A test failed. Read the full test output to understand which " +
				"assertion failed and why. Compare expected vs actual values carefully. " +
				"If the test itself is wrong (testing old behavior), update the test.",
		}
	}

	return ErrorCategory{Name: ""}
}

func errorContainsAny(s string, subs ...string) bool {
	for _, sub := range subs {
		if strings.Contains(s, sub) {
			return true
		}
	}
	return false
}
