package agent

import (
	"fmt"
	"regexp"
	"strings"

	"github.com/topcheer/ggcode/internal/provider"
)

var expectLineRe = regexp.MustCompile(`(?im)^EXPECT:\s*([a-z_0-9]+)?\s*(exit=0|exit!=0|tests_pass|>=1_match|applied>=1|success|fail)\s*$`)

var expectSideEffectTools = map[string]bool{
	"run_command":     true,
	"start_command":   true,
	"edit_file":       true,
	"multi_edit_file": true,
	"multi_file_edit": true,
	"write_file":      true,
	"batch_replace":   true,
	"grep":            true,
	"search_files":    true,
	"code_search":     true,
}

type expectDecl struct {
	toolName string
	check    string
}

func parseExpectDeclarations(text string) []expectDecl {
	matches := expectLineRe.FindAllStringSubmatch(text, -1)
	if len(matches) == 0 {
		return nil
	}
	decls := make([]expectDecl, 0, len(matches))
	for _, m := range matches {
		decls = append(decls, expectDecl{toolName: strings.ToLower(m[1]), check: m[2]})
	}
	return decls
}

func expectCheckPositive(check string) bool {
	switch check {
	case "exit=0", "tests_pass", ">=1_match", "applied>=1", "success":
		return true
	}
	return false
}

func evaluateExpectCheck(check, resultContent string, isError bool) (passed bool, detail string) {
	structuredFail := foresightResultFailureRe.MatchString(resultContent)
	switch check {
	case "exit=0":
		if isError || structuredFail {
			return false, "declared exit=0 but the command reported failure"
		}
		return true, ""
	case "exit!=0":
		if !isError && !structuredFail {
			return false, "declared exit!=0 but the command succeeded"
		}
		return true, ""
	case "tests_pass":
		if isError || structuredFail || strings.Contains(resultContent, "--- FAIL") {
			return false, "declared tests_pass but the test run reported failures"
		}
		return true, ""
	case ">=1_match":
		if foresightResultEmpty(resultContent) {
			return false, "declared >=1_match but the result is empty or no-match"
		}
		return true, ""
	case "applied>=1":
		if isError {
			return false, "declared applied>=1 but the edit reported an error"
		}
		return true, ""
	case "success":
		if isError || structuredFail {
			return false, "declared success but the result indicates failure"
		}
		return true, ""
	case "fail":
		if !isError && !structuredFail {
			return false, "declared fail but the result succeeded"
		}
		return true, ""
	}
	return true, ""
}

func (s *foresightCalibrateState) expectProtocolHint(toolCalls []provider.ToolCallDelta) string {
	if s == nil || s.protocolTaught {
		return ""
	}
	hasSideEffect := false
	for _, tc := range toolCalls {
		if expectSideEffectTools[tc.Name] {
			hasSideEffect = true
			break
		}
	}
	if !hasSideEffect {
		return ""
	}
	s.protocolTaught = true
	return fmt.Sprintf(
		"[expectation-protocol] Before calling a tool, you MAY declare its expected effect on one line: `EXPECT: <check>` (or `EXPECT: <tool> <check>` when batching several tools). Checks: %s. A declared expectation is verified against the actual result immediately; declaring success on a step that actually failed is flagged on the spot.",
		"exit=0 | exit!=0 | tests_pass | >=1_match | applied>=1 | success | fail",
	)
}
