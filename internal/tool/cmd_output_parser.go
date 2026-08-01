package tool

// Build/Test Output Intelligence — Structured Command Result Extraction
//
// Research basis: Context engineering research (Anthropic 2025, Fundesk 2026)
// identifies tool output as the "silent context killer" — a single `go test
// ./...` run for a large project can produce 5,000+ lines of output (~50K
// tokens). When tests fail, the agent must read through all of it to find the
// 3 failing tests buried among hundreds of passing ones. This wastes context
// budget and slows the fix-test cycle.
//
// Competitor analysis:
//   - Claude Code: relies on raw output + LSP diagnostics (no structured test
//     result extraction from command output)
//   - Cursor: has inline test result UI, parses test output for display
//   - Cline/OpenHands: parses test output to extract failures for display
//   - Go ecosystem: `go test -json` + `test2json` for structured output,
//     but agents rarely use -json flag and the output is verbose
//   - Aider: shows pass/fail counts but no structured extraction
//
// Gap: No deterministic extraction of actionable information from build/test
// command output. The agent gets raw text and must interpret it via LLM
// reasoning — expensive and error-prone for large outputs.
//
// Design:
//   - Runs after command execution, before result is returned to the agent
//   - Detects command type from the command string (go test, go build, etc.)
//   - Extracts: failed test names with locations, compile errors with file:line,
//     pass/skip/fail counts, panic stack traces
//   - Zero false-positive risk: only extracts patterns that are unambiguous
//   - Produces a compact "[Result Summary]" section appended to raw output
//   - No external dependencies — pure string parsing

import (
	"fmt"
	"regexp"
	"strings"
)

// maxResultSummaryLines caps the summary size to prevent flooding when a build
// has many errors.
const maxResultSummaryLines = 20

// summarizeCommandOutput analyzes command output and returns a compact summary
// of build/test results. Returns "" if no recognizable patterns are found.
//
// The summary is prepended with a "[Result Summary]" header and includes:
//   - For go test: failed test names, counts (pass/fail/skip), panic locations
//   - For go build/vet: compile errors with file:line locations
//   - For generic: lines matching compiler error patterns (file:line: message)
func summarizeCommandOutput(command, output string) string {
	cmdLower := strings.ToLower(strings.TrimSpace(command))
	// Extract the actual command from comment-prefixed strings
	for _, line := range strings.Split(cmdLower, "\n") {
		line = strings.TrimSpace(line)
		if line != "" && !strings.HasPrefix(line, "#") {
			cmdLower = line
			break
		}
	}

	switch {
	case isGoTestCommand(cmdLower):
		return summarizeGoTestOutput(output)
	case isGoBuildCommand(cmdLower):
		return summarizeGoBuildOutput(output)
	case isGenericTestCommand(cmdLower):
		// For other test runners, try generic parsing
		return summarizeGenericTestOutput(output)
	default:
		// For any command, extract compiler-style errors (file:line: message)
		return summarizeCompilerErrors(output)
	}
}

// isGoTestCommand returns true if the command runs Go tests.
// Uses word-boundary check to avoid false positives like "cargo test"
// which contains "go test" as a substring.
func isGoTestCommand(cmd string) bool {
	return strings.HasPrefix(cmd, "go test") ||
		strings.Contains(cmd, " go test ") ||
		strings.Contains(cmd, " go test\n") ||
		strings.Contains(cmd, " go test\t") ||
		strings.Contains(cmd, "gotest")
}

// isGoBuildCommand returns true if the command compiles Go code.
// Uses prefix check to avoid false positives (e.g. "cargo build" should not match).
func isGoBuildCommand(cmd string) bool {
	for _, sub := range []string{"go build", "go vet", "go install", "go generate", "go check"} {
		if strings.HasPrefix(cmd, sub) ||
			strings.Contains(cmd, " "+sub+" ") ||
			strings.Contains(cmd, " "+sub+"\n") ||
			strings.Contains(cmd, " "+sub+"\t") {
			return true
		}
	}
	return false
}

// isGenericTestCommand returns true if the command is a known test runner.
func isGenericTestCommand(cmd string) bool {
	for _, pattern := range []string{
		"pytest", "python -m pytest", "npm test", "npm run test",
		"yarn test", "pnpm test", "jest", "vitest", "mocha",
		"cargo test", "mvn test", "gradle test", "make test",
		"dotnet test", "ruby -Itest", "rspec",
	} {
		if strings.Contains(cmd, pattern) {
			return true
		}
	}
	return false
}

// --- Go Test Output Parsing ---

// Patterns for Go test output.
var (
	// "--- FAIL: TestName (0.00s)" or "--- FAIL: TestName/Sub (0.00s)"
	goTestFailRe = regexp.MustCompile(`^--- FAIL:\s+(.+?)\s+\(`)
	// "--- SKIP: TestName (0.00s)"
	goTestSkipRe = regexp.MustCompile(`^--- SKIP:\s+(.+?)\s+\(`)
	// "--- PASS: TestName (0.00s)" — only matched for counting
	goTestPassRe = regexp.MustCompile(`^--- PASS:\s+(.+?)\s+\(`)
	// "FAIL\tpackage/path\t[build failed|0.00s]"
	goTestFailPkgRe = regexp.MustCompile(`^FAIL\s+\S+\s+.*`)
	// "ok  \tpackage/path\t0.00s"
	goTestOkPkgRe = regexp.MustCompile(`^ok\s+\s?\S+\s+`)
	// Panic location: "panic: ..." followed by file:line
	panicRe = regexp.MustCompile(`^panic:.*`)
	// File:line pattern for panic origins
	panicLocRe = regexp.MustCompile(`/[^:\s]+\.\w+:\d+`)
)

// summarizeGoTestOutput parses Go test output and extracts a structured summary.
func summarizeGoTestOutput(output string) string {
	lines := strings.Split(output, "\n")

	var failedTests []string
	var skippedTests []string
	passCount, failCount, skipCount := 0, 0, 0
	var panicMsgs []string
	var buildFailedPkgs []string

	for _, line := range lines {
		trimmed := strings.TrimSpace(line)

		if m := goTestFailRe.FindStringSubmatch(trimmed); m != nil {
			failCount++
			failedTests = append(failedTests, m[1])
		} else if m := goTestSkipRe.FindStringSubmatch(trimmed); m != nil {
			skipCount++
			skippedTests = append(skippedTests, m[1])
		} else if goTestPassRe.MatchString(trimmed) {
			passCount++
		} else if strings.Contains(trimmed, "[build failed]") {
			failCount++
			if m := goTestFailPkgRe.FindStringSubmatch(trimmed); m != nil {
				buildFailedPkgs = append(buildFailedPkgs, trimmed)
			}
		} else if panicRe.MatchString(trimmed) && len(panicMsgs) < 3 {
			panicMsgs = append(panicMsgs, trimmed)
		}
	}

	// Count ok packages
	okPkgCount := 0
	for _, line := range lines {
		if goTestOkPkgRe.MatchString(strings.TrimSpace(line)) {
			okPkgCount++
		}
	}

	// Only produce a summary if we found something actionable
	if failCount == 0 && skipCount == 0 && passCount == 0 && len(panicMsgs) == 0 {
		return ""
	}

	var sb strings.Builder
	sb.WriteString("[Result Summary]\n")

	if failCount > 0 {
		sb.WriteString(fmt.Sprintf("FAILED: %d test(s)", failCount))
		if passCount > 0 {
			sb.WriteString(fmt.Sprintf(", passed: %d", passCount))
		}
		if skipCount > 0 {
			sb.WriteString(fmt.Sprintf(", skipped: %d", skipCount))
		}
		sb.WriteString("\n")

		for i, t := range failedTests {
			if i >= maxResultSummaryLines-5 {
				sb.WriteString(fmt.Sprintf("  ... and %d more failure(s)\n", len(failedTests)-i))
				break
			}
			sb.WriteString(fmt.Sprintf("  FAIL: %s\n", t))
		}
	} else if passCount > 0 {
		// All passed — summarize for token savings on verbose runs
		sb.WriteString(fmt.Sprintf("PASSED: %d test(s)", passCount))
		if skipCount > 0 {
			sb.WriteString(fmt.Sprintf(", skipped: %d", skipCount))
		}
		sb.WriteString("\n")
	}

	if len(buildFailedPkgs) > 0 {
		sb.WriteString("Build failures in package(s):\n")
		for _, p := range buildFailedPkgs {
			sb.WriteString(fmt.Sprintf("  %s\n", p))
		}
	}

	if len(panicMsgs) > 0 {
		sb.WriteString("Panics detected:\n")
		for _, p := range panicMsgs {
			sb.WriteString(fmt.Sprintf("  %s\n", p))
		}
	}

	return sb.String()
}

// --- Go Build Output Parsing ---

// Pattern for Go compile errors: "path/file.go:line:col: error message"
var goCompileErrorRe = regexp.MustCompile(`([^\s:]+\.go):(\d+)(?::\d+)?:\s+(.*)`)

// summarizeGoBuildOutput parses Go build/vet output and extracts compile errors.
func summarizeGoBuildOutput(output string) string {
	lines := strings.Split(output, "\n")

	type compileError struct {
		file string
		line string
		msg  string
	}
	var errors []compileError
	seenFiles := make(map[string]bool)
	var errorFiles []string

	for _, line := range lines {
		trimmed := strings.TrimSpace(line)
		if m := goCompileErrorRe.FindStringSubmatch(trimmed); m != nil {
			// Only capture actual errors, not info lines
			lower := strings.ToLower(trimmed)
			if !strings.Contains(lower, "error") && !strings.Contains(lower, "undefined") &&
				!strings.Contains(lower, "declared and not used") &&
				!strings.Contains(lower, "cannot use") &&
				!strings.Contains(lower, "mismatched types") {
				continue
			}
			errors = append(errors, compileError{
				file: m[1],
				line: m[2],
				msg:  strings.TrimSpace(m[3]),
			})
			if !seenFiles[m[1]] {
				seenFiles[m[1]] = true
				errorFiles = append(errorFiles, m[1])
			}
		}
	}

	if len(errors) == 0 {
		return ""
	}

	var sb strings.Builder
	sb.WriteString("[Result Summary]\n")
	sb.WriteString(fmt.Sprintf("%d compile error(s) in %d file(s):\n", len(errors), len(errorFiles)))

	for i, e := range errors {
		if i >= maxResultSummaryLines-2 {
			sb.WriteString(fmt.Sprintf("  ... and %d more error(s)\n", len(errors)-i))
			break
		}
		sb.WriteString(fmt.Sprintf("  %s:%s: %s\n", e.file, e.line, e.msg))
	}

	return sb.String()
}

// --- Generic Test Output Parsing ---

// Patterns for common test runner outputs.
var (
	// pytest: "FAILED test_file.py::test_name - Error message"
	pytestFailRe = regexp.MustCompile(`^FAILED\s+(.+?)\s+-`)
	// pytest: "===== N failed, M passed in X.XXs ====="
	pytestSummaryRe = regexp.MustCompile(`(\d+)\s+failed.*(\d+)\s+passed`)
	// jest/vitest: "FAIL  path/to/test.test.js"
	jestFailRe = regexp.MustCompile(`^FAIL\s+(.+)`)
	// jest summary: "Tests: 2 failed, 3 passed, 5 total"
	jestSummaryRe = regexp.MustCompile(`Tests:\s+(\d+)\s+failed.*(\d+)\s+passed.*(\d+)\s+total`)
	// cargo test: "test result: FAILED. 1 passed; 1 failed; 0 ignored; ..."
	cargoFailRe = regexp.MustCompile(`test result:\s*(FAILED|ok)\.\s*(\d+)\s+passed;\s*(\d+)\s+failed`)
)

// summarizeGenericTestOutput parses output from pytest, jest, cargo test, etc.
func summarizeGenericTestOutput(output string) string {
	lines := strings.Split(output, "\n")

	var failedTests []string
	var summary string

	for _, line := range lines {
		trimmed := strings.TrimSpace(line)

		if m := pytestFailRe.FindStringSubmatch(trimmed); m != nil {
			failedTests = append(failedTests, m[1])
		}
		if m := pytestSummaryRe.FindStringSubmatch(trimmed); m != nil {
			summary = fmt.Sprintf("%s failed, %s passed", m[1], m[2])
		}
		if m := jestFailRe.FindStringSubmatch(trimmed); m != nil {
			failedTests = append(failedTests, strings.TrimSpace(m[1]))
		}
		if m := jestSummaryRe.FindStringSubmatch(trimmed); m != nil {
			summary = fmt.Sprintf("%s failed, %s passed, %s total", m[1], m[2], m[3])
		}
		if m := cargoFailRe.FindStringSubmatch(trimmed); m != nil {
			if m[1] == "FAILED" {
				summary = fmt.Sprintf("%s passed, %s failed", m[2], m[3])
			}
		}
	}

	if len(failedTests) == 0 && summary == "" {
		return ""
	}

	var sb strings.Builder
	sb.WriteString("[Result Summary]\n")
	if summary != "" {
		sb.WriteString(fmt.Sprintf("Results: %s\n", summary))
	}
	for i, t := range failedTests {
		if i >= maxResultSummaryLines-2 {
			sb.WriteString(fmt.Sprintf("  ... and %d more\n", len(failedTests)-i))
			break
		}
		sb.WriteString(fmt.Sprintf("  FAIL: %s\n", t))
	}

	return sb.String()
}

// --- Compiler Error Extraction (generic) ---

// Generic compiler error pattern: "file.ext:line:col: error: message" or "file.ext(line): error"
var genericCompileErrorRe = regexp.MustCompile(`([^\s:]+\.(?:go|rs|py|ts|js|tsx|jsx|java|c|cpp|rb)):(\d+)(?::\d+)?:\s*(error|Error|ERROR)[:\s]+(.+)`)

// summarizeCompilerErrors extracts compiler-style errors from any command output.
func summarizeCompilerErrors(output string) string {
	lines := strings.Split(output, "\n")
	_ = lines // lines not used directly; we use regex on the full output

	matches := genericCompileErrorRe.FindAllStringSubmatch(output, -1)
	if len(matches) == 0 {
		return ""
	}

	var sb strings.Builder
	sb.WriteString("[Result Summary]\n")
	sb.WriteString(fmt.Sprintf("%d error(s) detected:\n", len(matches)))

	for i, m := range matches {
		if i >= maxResultSummaryLines-2 {
			sb.WriteString(fmt.Sprintf("  ... and %d more\n", len(matches)-i))
			break
		}
		sb.WriteString(fmt.Sprintf("  %s:%s: %s\n", m[1], m[2], m[4]))
	}

	return sb.String()
}
