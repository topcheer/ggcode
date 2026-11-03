package tool

import (
	"fmt"
	"regexp"
	"strings"
)

const maxResultSummaryLines = 20

func summarizeCommandOutput(command, output string) string {
	cmdLower := strings.ToLower(strings.TrimSpace(command))

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

		return summarizeGenericTestOutput(output)
	default:

		return summarizeCompilerErrors(output)
	}
}

func isGoTestCommand(cmd string) bool {

	if cmd == "go test" || strings.HasPrefix(cmd, "go test ") {
		return true
	}
	return strings.Contains(cmd, " go test ") ||
		strings.Contains(cmd, " go test\n") ||
		strings.Contains(cmd, " go test\t") ||

		goTestWordRe.MatchString(cmd)
}

var goTestWordRe = regexp.MustCompile(`(?:^|[\s;&|])(?:go\s+test|gotest)(?:\s|$)`)

func isGoBuildCommand(cmd string) bool {

	for _, sub := range []string{"go build", "go vet", "go install", "go generate", "go check"} {
		if cmd == sub || strings.HasPrefix(cmd, sub+" ") ||
			strings.Contains(cmd, " "+sub+" ") ||
			strings.Contains(cmd, " "+sub+"\n") ||
			strings.Contains(cmd, " "+sub+"\t") {
			return true
		}
	}
	return false
}

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

var (
	goTestFailRe    = regexp.MustCompile(`^--- FAIL:\s+(.+?)\s+\(`)
	goTestSkipRe    = regexp.MustCompile(`^--- SKIP:\s+(.+?)\s+\(`)
	goTestPassRe    = regexp.MustCompile(`^--- PASS:\s+(.+?)\s+\(`)
	goTestFailPkgRe = regexp.MustCompile(`^FAIL\s+\S+\s+.*`)
	goTestOkPkgRe   = regexp.MustCompile(`^ok\s+\s?\S+\s+`)
	panicRe         = regexp.MustCompile(`^panic:.*`)
	panicLocRe      = regexp.MustCompile(`/[^:\s]+\.\w+:\d+`)
)

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

	okPkgCount := 0
	for _, line := range lines {
		if goTestOkPkgRe.MatchString(strings.TrimSpace(line)) {
			okPkgCount++
		}
	}

	if failCount == 0 && skipCount == 0 && passCount == 0 && okPkgCount == 0 && len(panicMsgs) == 0 {
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

		sb.WriteString(fmt.Sprintf("PASSED: %d test(s)", passCount))
		if skipCount > 0 {
			sb.WriteString(fmt.Sprintf(", skipped: %d", skipCount))
		}
		if okPkgCount > 0 {
			sb.WriteString(fmt.Sprintf(", ok: %d package(s)", okPkgCount))
		}
		sb.WriteString("\n")
	} else if okPkgCount > 0 {

		sb.WriteString(fmt.Sprintf("PASSED: %d package(s)", okPkgCount))
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

var goCompileErrorRe = regexp.MustCompile(`([^\s:]+\.go):(\d+)(?::\d+)?:\s+(.*)`)

var goCompileErrorKeywords = []string{
	"error",
	"undefined",
	"declared and not used",
	"imported and not used",
	"redeclared in this block",
	"missing return",
	"duplicate declaration",
	"cannot use",
	"mismatched types",

	"printf:",
	"vet:",
}

func isGoCompileErrorLine(lower string) bool {
	for _, kw := range goCompileErrorKeywords {
		if strings.Contains(lower, kw) {
			return true
		}
	}
	return false
}

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

			lower := strings.ToLower(trimmed)
			if !isGoCompileErrorLine(lower) {
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

var (
	pytestFailRe    = regexp.MustCompile(`^FAILED\s+(.+?)\s+-`)
	pytestSummaryRe = regexp.MustCompile(`(\d+)\s+failed.*(\d+)\s+passed`)
	jestFailRe      = regexp.MustCompile(`^FAIL\s+(.+)`)
	jestSummaryRe   = regexp.MustCompile(`Tests:\s+(\d+)\s+failed.*(\d+)\s+passed.*(\d+)\s+total`)
	cargoFailRe     = regexp.MustCompile(`test result:\s*(FAILED|ok)\.\s*(\d+)\s+passed;\s*(\d+)\s+failed`)
)

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

var genericCompileErrorRe = regexp.MustCompile(`([^\s:]+\.(?:go|rs|py|ts|js|tsx|jsx|java|c|cpp|rb)):(\d+)(?::\d+)?:\s*(error|Error|ERROR)[:\s]+(.+)`)

func summarizeCompilerErrors(output string) string {
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
