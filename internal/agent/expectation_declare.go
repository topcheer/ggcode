package agent

import (
	"encoding/json"
	"regexp"
	"strings"
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

// parseAppliedCount extracts the "applied changes" counter from a structured
// edit-tool result (batch_replace / multi_file_edit JSON: files_changed,
// total_matches). ok=false when the content is not parseable structured JSON,
// in which case the caller falls back to the error-bit-only verdict.
func parseAppliedCount(content string) (int, bool) {
	var payload struct {
		FilesChanged  int `json:"files_changed"`
		TotalMatches  int `json:"total_matches"`
		FilesReplaced int `json:"files_replaced"`
	}
	if err := json.Unmarshal([]byte(strings.TrimSpace(content)), &payload); err != nil {
		return 0, false
	}
	return payload.FilesChanged + payload.TotalMatches + payload.FilesReplaced, true
}

func evaluateExpectCheck(check, resultContent string, isError bool) (passed bool, detail string) {
	// Failure signal comes from the structured isError flag plus the bounded
	// status envelope only (#5023): searching file contents for "panic:"-like
	// markers must not make a successful command count as failed.
	structuredFail := foresightResultFailureSignal(resultContent, isError)
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
		// #5765: batch_replace expresses failure via IsError=FilesError>0, so an
		// all-skipped run (zero matches in every target file) returns success
		// with files_changed:0. "Applied at least one" must be checked against
		// the structured counters, not just the error bit.
		if applied, ok := parseAppliedCount(resultContent); ok && applied < 1 {
			return false, "declared applied>=1 but the result reports zero applied changes (all targets skipped or no matches)"
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
