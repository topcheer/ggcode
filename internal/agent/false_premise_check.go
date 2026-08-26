package agent

// False Premise / Ungrounded Success Claim Detector
//
// Research basis: World-model desynchronization in LLM agents (DreamerV3,
// Nature 2025) and self-improving agent trajectory analysis (SICA,
// arXiv:2504.15228). When LLM agents operate in tool-augmented loops, they
// frequently confabulate positive outcomes that contradict their own tool
// results - "the build passed" when the build command returned exit code 1,
// "found 3 matches" when grep returned zero results, "the file exists" when
// read_file returned a not-found error.
//
// This "world-model drift" is a distinct failure class from the process
// patterns tracked by other detectors:
//   - verify_disconnect.go: checks if the agent CLAIMS to verify but does not
//   - self_diagnosis.go: detects unverifiable self-assessment claims
//   - evidence_overconfidence.go: detects overconfidence from sparse evidence
//
// THIS detector fills the orthogonal gap: detects FACTUAL CONTRADICTIONS
// between the agent's success claims and the most recent tool error results.
// It catches the moment the agent's internal "world model" diverges from
// observable tool evidence.
//
// Common patterns caught:
//   1. Build/test command returned error but agent says "build passed",
//      "tests pass", "compiles successfully"
//   2. Grep/search returned no matches but agent says "found N matches"
//   3. File read returned not-found but agent references file content as if
//      it exists
//   4. Any tool error but agent says "done", "success", "fixed", "complete"
//      without acknowledging the error
//
// Design:
//   - Zero LLM cost — pure string matching on tool results + assistant text
//   - Fires at most 2 times per run (cap total warnings)
//   - Non-blocking: guidance injected as a user message, execution proceeds
//   - Tracks recent tool errors with a freshness window (few turns)

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

// buildVerifyToolPrefixes are command-execution tools whose outcomes are
// command-granular, not tool-granular: a later successful ls does NOT clear
// an earlier go build failure (#546 Bug 2).
var buildVerifyToolPrefixes = map[string]bool{
	"run_command":         true,
	"start_command":       true,
	"wait_command":        true,
	"read_command_output": true,
}

// buildVerifyErrorRe identifies build/test/lint failures inside command-tool
// error snippets — the records that command-granular clearing preserves.
var buildVerifyErrorRe = regexp.MustCompile(`(?i)(build\s+fail|compil(e|ation)\s+(error|fail)|test.*(fail|panic)|FAIL\s|panic:|signal:\s+killed|fatal error:|lint.*(error|fail)|exit\s+status\s+[1-9]|exit\s+code\s+[1-9])`)

// Issue #1045: buildVerifyErrorRe now matches panic/signal:killed/fatal error patterns
// to preserve test failure records through unrelated command successes.

// buildErrorRe identifies build-specific errors (excluding test failures)
// for distinguishing build-only failures from test failures (#593 P6).
var buildErrorRe = regexp.MustCompile(`(?i)(build\s+(error|fail)|compil(e|ation)\s+(error|fail))`)

// recordToolResult is called after each tool execution.
func (f *falsePremiseState) recordToolResult(toolName string, resultContent string, isError bool) {
	if !isError {
		// Same tool succeeded on a later run: the earlier error has been
		// superseded by newer evidence (#331). "fail → fix → re-run → report"
		// is the core correct workflow; a stale error record for a tool that
		// has since succeeded must not poison later success-claim checks.
		//
		// Exception (#546): command-execution tools are command-granular —
		// a successful ls says nothing about an earlier failed go build, so
		// build/test error snippets from these tools survive same-tool
		// successes until aged out; all other errors keep #331 supersede.
		kept := f.recentErrors[:0]
		for _, e := range f.recentErrors {
			// Issue #593 P5: clear by fpIsBuildTestTool category, not exact toolName.
			// If the current tool and the error tool are both in the same build/test
			// verification category, a success should supersede the error (cross-tool
			// rerun: run_command → start_command → wait_command).
			sameCategory := fpIsBuildTestTool(toolName) && fpIsBuildTestTool(e.toolName)
			isBuildTestError := buildVerifyToolPrefixes[e.toolName] && buildVerifyErrorRe.MatchString(e.errorSnippet)
			if !sameCategory && e.toolName != toolName {
				kept = append(kept, e)
				continue
			}
			// Same tool always clears non-build-test errors (#331 semantics)
			if e.toolName == toolName && !isBuildTestError {
				continue
			}
			// Build/test errors require matching success (#593 P5 + #546)
			if isBuildTestError {
				// Command-granular (#546): only a success that is ITSELF a
				// build/verify success ("build passed, 42 tests ok") may
				// supersede a build failure — preserving #331's fail→fix→
				// re-run-the-build→report workflow while an unrelated ls
				// success no longer clears the record.
				// Issue #593 P6: check if error was build-only to allow empty output.
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

// checkFalsePremise scans assistant text for contradicting success claims.
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

		// Aligned with branch 4: acknowledging the earlier error means the claim
		// is grounded, not confabulated (#331).
		// Issue #593 P6: check if error was build-only to allow empty output.
		// Issue #1044: empty assistant text (e.g., reasoning-only tool_use rounds) must NOT
		// be treated as a success claim. The P6 empty-output logic applies to COMMAND OUTPUT
		// (run_command stdout), not to assistant declarative text.
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

		// Search/read tools have dedicated branches above that require
		// error-snippet indicators ("no matches" / "not found"); a bare syntax
		// error from grep must not trigger the generic branch (#331).
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

// --- Tool category helpers ---

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

// --- Error snippet indicators ---

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

// --- Claim matchers ---

var buildSuccessRe = regexp.MustCompile(`(?i)(build\s+(passed|succeed|succeeded|successful|success)|compiles?\s+(successfully|without error|clean)|compilation\s+success|tests?\s+(pass|passed|all pass|all passing|succeed)|all\s+tests?\s+pass|test\s+suite\s+pass|lint\s+(pass|passed|clean|ok)|^ok\s+|^PASS$|^\s*---\s*PASS:)`)

func matchesBuildSuccessClaim(lowered string, isBuildOnly bool) bool {
	// Issue #593 P6: empty output counts as success for build-only commands
	// (not test commands which should have PASS/ok output).
	if isBuildOnly && strings.TrimSpace(lowered) == "" {
		return true
	}
	return buildSuccessRe.MatchString(lowered)
}

// foundResultRe counts must be ≥ 1 ([1-9] first digit): "found 0 matches"
// faithfully relaying a no-hit grep is a true statement, not a confabulated
// positive (#546). Zero-count statements are excluded via foundZeroCancelRe
// below — the same cancel pattern the fileExists branch uses
// (fileNotFoundCancelRe).
var foundResultRe = regexp.MustCompile(`(?i)(found\s+[1-9][0-9]*\s+(match|result|file|reference|occurrence)|returned\s+[1-9][0-9]*\s+(match|result))`)

// foundZeroCancelRe marks zero-count statements as truthful no-hit reports
// ("found 0 matches", "returned 0 results as expected") — evidence of
// checking, not confabulation (#546 Bug 1).
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
