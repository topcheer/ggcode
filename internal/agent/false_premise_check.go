package agent

import (
	"fmt"
	"regexp"
	"strings"

	"github.com/topcheer/ggcode/internal/debug"
)

const (
	maxFalsePremiseWarnings    = 2
	falsePremiseFreshnessTurns = 2
)

type toolErrorRecord struct {
	toolName     string
	errorSnippet string
	turnsAgo     int
	matched      bool
}

type falsePremiseState struct {
	recentErrors []toolErrorRecord
	warningCount int
}

func newFalsePremiseState() *falsePremiseState {
	return &falsePremiseState{}
}

func (f *falsePremiseState) reset() {
	f.recentErrors = nil
	f.warningCount = 0
}

var buildVerifyToolPrefixes = map[string]bool{
	"run_command":         true,
	"start_command":       true,
	"wait_command":        true,
	"read_command_output": true,
}

var buildVerifyErrorRe = regexp.MustCompile(`(?i)(build\s+fail|compil(e|ation)\s+(error|fail)|test.*(fail|panic)|FAIL\s|panic:|signal:\s+killed|fatal error:|lint.*(error|fail)|exit\s+status\s+[1-9]|exit\s+code\s+[1-9])`)

var buildErrorRe = regexp.MustCompile(`(?i)(build\s+(error|fail)|compil(e|ation)\s+(error|fail))`)

func (f *falsePremiseState) recordToolResult(toolName string, resultContent string, isError bool) {
	if !isError {

		kept := f.recentErrors[:0]
		for _, e := range f.recentErrors {

			sameCategory := fpIsBuildTestTool(toolName) && fpIsBuildTestTool(e.toolName)
			isBuildTestError := buildVerifyToolPrefixes[e.toolName] && buildVerifyErrorRe.MatchString(e.errorSnippet)
			if !sameCategory && e.toolName != toolName {
				kept = append(kept, e)
				continue
			}

			if e.toolName == toolName && !isBuildTestError {
				continue
			}

			if isBuildTestError {

				isBuildOnly := buildErrorRe.MatchString(e.errorSnippet)
				if !matchesBuildSuccessClaim(strings.ToLower(resultContent), isBuildOnly) {
					kept = append(kept, e)
				}
			}
		}
		f.recentErrors = kept
		return
	}
	snippet := resultContent
	if len(snippet) > 200 {
		snippet = snippet[:200]
	}
	f.recentErrors = append(f.recentErrors, toolErrorRecord{
		toolName:     toolName,
		errorSnippet: snippet,
	})
	if len(f.recentErrors) > 5 {
		f.recentErrors = f.recentErrors[len(f.recentErrors)-5:]
	}
}

func (f *falsePremiseState) checkFalsePremise(assistantText string) string {
	if len(f.recentErrors) == 0 {
		return ""
	}
	if f.warningCount >= maxFalsePremiseWarnings {
		f.ageErrors()
		return ""
	}

	lowered := strings.ToLower(assistantText)
	var found []string

	for i := range f.recentErrors {
		err := &f.recentErrors[i]
		if err.matched || err.turnsAgo > falsePremiseFreshnessTurns {
			continue
		}

		isBuildOnly := buildErrorRe.MatchString(err.errorSnippet)
		if fpIsBuildTestTool(err.toolName) && strings.TrimSpace(lowered) != "" &&
			matchesBuildSuccessClaim(lowered, isBuildOnly) && !acknowledgesError(lowered) {
			err.matched = true
			found = append(found, buildContradiction(err, "build/test success",
				"Re-run the build/test command and report the actual result."))
			continue
		}

		if fpIsSearchTool(err.toolName) && indicatesNoResult(err.errorSnippet) && matchesFoundResultClaim(lowered) {
			err.matched = true
			found = append(found, buildContradiction(err, "search results found",
				"Re-examine the search output; it did not return the results you claim."))
			continue
		}

		if fpIsReadTool(err.toolName) && indicatesNotFound(err.errorSnippet) && matchesFileExistsClaim(lowered) {
			err.matched = true
			found = append(found, buildContradiction(err, "file existence",
				"The file was not found. Verify the path before claiming to have read its content."))
			continue
		}

		if !fpIsSearchTool(err.toolName) && !fpIsReadTool(err.toolName) &&
			matchesGenericSuccessClaim(lowered) && !acknowledgesError(lowered) {
			err.matched = true
			found = append(found, buildContradiction(err, "generic success",
				"Address the tool error above before claiming success."))
			continue
		}
	}

	f.ageErrors()

	if len(found) == 0 {
		return ""
	}

	f.warningCount++
	debug.Log("agent", "false-premise: detected %d success-claim contradictions", len(found))

	var sb strings.Builder
	sb.WriteString("[False Premise Warning] Your recent success claim(s) contradict tool error results:\n")
	for _, msg := range found {
		sb.WriteString(msg)
		sb.WriteString("\n")
	}
	sb.WriteString("Ground your claims in actual tool outputs - do not confabulate positive results.")
	return sb.String()
}

func buildContradiction(err *toolErrorRecord, claimType, suggestion string) string {
	ev := err.errorSnippet
	if len(ev) > 80 {
		ev = ev[:80] + "..."
	}
	return fmt.Sprintf(
		"  - Tool '%s' returned an error, but you claimed %s. Error evidence: \"%s\". %s",
		err.toolName, claimType, ev, suggestion,
	)
}

func (f *falsePremiseState) ageErrors() {
	for i := range f.recentErrors {
		f.recentErrors[i].turnsAgo++
	}
}

func fpIsBuildTestTool(name string) bool {
	switch name {
	case "run_command", "start_command", "task_output", "read_command_output", "wait_command":
		return true
	}
	return false
}

func fpIsSearchTool(name string) bool {
	switch name {
	case "grep", "search_files", "glob", "code_search", "lsp_workspace_symbols", "lsp_references", "lsp_definition":
		return true
	}
	return false
}

func fpIsReadTool(name string) bool {
	switch name {
	case "read_file", "multi_file_read", "list_directory":
		return true
	}
	return false
}

func indicatesNoResult(snippet string) bool {
	low := strings.ToLower(snippet)
	for _, ind := range []string{
		"no matches", "no results", "0 matches", "zero matches",
		"not found", "no files found", "returned 0",
		"did not match", "no such file",
	} {
		if strings.Contains(low, ind) {
			return true
		}
	}
	return false
}

func indicatesNotFound(snippet string) bool {
	low := strings.ToLower(snippet)
	for _, ind := range []string{
		"no such file", "not found", "does not exist", "file not found",
		"stat:", "cannot find",
	} {
		if strings.Contains(low, ind) {
			return true
		}
	}
	return false
}

var buildSuccessRe = regexp.MustCompile(`(?i)(build\s+(passed|succeed|succeeded|successful|success)|compiles?\s+(successfully|without error|clean)|compilation\s+success|tests?\s+(pass|passed|all pass|all passing|succeed)|all\s+tests?\s+pass|test\s+suite\s+pass|lint\s+(pass|passed|clean|ok)|^ok\s+|^PASS$|^\s*---\s*PASS:)`)

func matchesBuildSuccessClaim(lowered string, isBuildOnly bool) bool {

	if isBuildOnly && strings.TrimSpace(lowered) == "" {
		return true
	}
	return buildSuccessRe.MatchString(lowered)
}

var foundResultRe = regexp.MustCompile(`(?i)(found\s+[1-9][0-9]*\s+(match|result|file|reference|occurrence)|returned\s+[1-9][0-9]*\s+(match|result))`)

var foundZeroCancelRe = regexp.MustCompile(`(?i)((found|returned)\s+0\s+(match|result|file|reference|occurrence))`)

func matchesFoundResultClaim(lowered string) bool {
	if foundZeroCancelRe.MatchString(lowered) {
		return false
	}
	return foundResultRe.MatchString(lowered)
}

var fileExistsRe = regexp.MustCompile(`(?i)(the\s+file\s+(exists|contains|has)|file\s+content\s+(is|shows|contains)|i\s+read\s+the\s+file|contents?\s+of\s+(the|this)\s+file)`)
var fileNotFoundCancelRe = regexp.MustCompile(`(?i)(file\s+does\s+not\s+exist|not\s+found|no\s+such\s+file|does\s+not\s+exist|wrong\s+path|incorrect\s+path)`)

func matchesFileExistsClaim(lowered string) bool {
	if fileNotFoundCancelRe.MatchString(lowered) {
		return false
	}
	return fileExistsRe.MatchString(lowered)
}

var genericSuccessRe = regexp.MustCompile(`(?i)\b(done|success(fully)?|fixed|resolved|complete[ds]?|all\s+set|finished|works?\s+(now|correctly)|problem\s+is\s+(fixed|solved|resolved)|issue\s+is\s+(fixed|solved|resolved)|everything\s+(works?|passes?|is\s+(fine|good|correct)))\b`)
var acknowledgesErrorRe = regexp.MustCompile(`(?i)\b(error|fail(ed|ure)?|did\s+not\s+(work|pass)|not\s+(working|passing)|incorrect|wrong|still\s+(fail|broken)|was\s+(unable|not\s+able)|could\s+not|cannot)\b`)

func matchesGenericSuccessClaim(lowered string) bool {
	return genericSuccessRe.MatchString(lowered)
}

func acknowledgesError(lowered string) bool {
	return acknowledgesErrorRe.MatchString(lowered)
}
