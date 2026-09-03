package agent

// Error Count Regression Detection -- Negative Progress Detection
//
// Research basis: 2025-2026 causal reasoning research in AI agents (arXiv
// 2509.25282 "Causal-Visual Programming") shows that agents rely on spurious
// correlations and fail to recognize when their actions introduce NEW problems.
// Industry discussions (AgentDiet FSE 2026) identify "negative progress" as a
// critical failure mode: the agent edits code, runs verification, and the error
// count INCREASES -- but the agent proceeds as if it made progress.
//
// This detector fills a gap not covered by existing systems:
//   - fix_cascade.go: tracks edit->verify->fail CYCLES (wrong hypothesis).
//     But it doesn't detect whether each cycle makes things BETTER or WORSE.
//   - recurring_error.go: detects the SAME error fingerprint recurring.
//     Regression introduces NEW errors with different fingerprints.
//   - compounding_failure: tracks aggregate failure rate, not error count
//     direction (increasing vs decreasing).
//   - convergence_lock: fires after SUCCESSFUL verification (opposite scenario).
//
// The signature pattern:
//   1. Agent runs build/test -> N errors
//   2. Agent edits code to fix some errors
//   3. Agent runs build/test -> M errors where M > N
//   4. Agent's edit introduced regressions or unmasked hidden errors
//   5. Agent continues without recognizing it went backwards
//
// Detection: count error lines in verification output. Track the previous
// count. If the count increases after edits, inject guidance.
//
// Design: zero LLM cost, deterministic. Fires at most twice per run.

import (
	"regexp"
	"strconv"
	"strings"

	"github.com/topcheer/ggcode/internal/debug"
)

// errorRegressionCheckCommand is the Agent-level wrapper that checks whether
// a verification command result shows an error count increase compared to the
// previous verification. Returns guidance if regression detected.
func (a *Agent) errorRegressionCheckCommand(toolName string, args []byte, content string, isError bool) string {
	if a.errRegression == nil {
		return ""
	}
	if toolName != "run_command" {
		return ""
	}
	cmd := extractCommandFromArgs(args)
	if cmd == "" || !isVerifyCommand(cmd) {
		return ""
	}
	return a.errRegression.recordVerify(cmd, content, isError)
}

const (
	// regressionWarnThreshold: minimum error count increase to trigger guidance.
	// Small fluctuations (1→2) may be unmasking, not regression. A jump of 3+
	// new errors strongly suggests the edit broke something.
	regressionWarnThreshold = 3

	// maxRegressionWarnings caps how many times regression guidance fires per run.
	maxRegressionWarnings = 2
)

// errorLinesRe matches common compiler/test error patterns across languages.
var errorLinesRe = regexp.MustCompile(`(?im)^.*\b(?:error|ERROR|Error)[\s:].*$`)

// countVerifyErrors returns the number of error-like lines in verification output.
// Uses a conservative regex to avoid false positives from non-error text.
func countVerifyErrors(content string) int {
	matches := errorLinesRe.FindAllString(content, -1) //nolint:all
	count := 0
	for _, m := range matches {
		// Filter out lines that are about errors in a meta-sense (e.g., "0 errors")
		// but keep actual error lines.
		lower := strings.ToLower(m)
		if strings.Contains(lower, "0 error") || strings.Contains(lower, "no error") {
			continue
		}
		count++
	}
	return count
}

// errRegressionState tracks error counts across verification runs to detect
// error count regressions (negative progress).
type errRegressionState struct {
	// lastErrorCount is the error count from the most recent verification.
	// -1 means no previous verification has been recorded.
	lastErrorCount int

	// lastCommandKind is the normalized command signature of that run
	// (#1457-C): build/test/lint/vet outputs have incomparable styles - a
	// 2-error build fixed into a first-ever-passing test run (20+ 'Error:'
	// lines) counted as 'INCREASED from 2 to 20, consider revert' on what
	// was major progress. Only same-command comparisons count now.
	lastCommandKind string

	// hadEdits tracks whether file edits occurred since the last verification.
	hadEdits bool

	// warningCount tracks how many regression warnings have fired this run.
	warningCount int
}

func newErrRegressionState() *errRegressionState {
	return &errRegressionState{lastErrorCount: -1}
}

func (e *errRegressionState) reset() {
	e.lastErrorCount = -1
	e.hadEdits = false
	e.warningCount = 0
}

// recordEdit notes that a file edit occurred. Called after successful edits.
func (e *errRegressionState) recordEdit() {
	e.hadEdits = true
}

// recordVerify processes a verification result and returns guidance if an
// error count regression is detected (current errors > previous errors by
// at least regressionWarnThreshold).
func (e *errRegressionState) recordVerify(cmd, content string, failed bool) string {
	currentErrors := countVerifyErrors(content)
	prevErrors := e.lastErrorCount
	hadEdits := e.hadEdits
	// #1457-C: normalize to the command's leading token (go build / go
	// test / make test...); a different command resets the baseline
	// rather than comparing incomparable output styles.
	kind := verifyCommandKind(cmd)
	sameCommand := kind != "" && kind == e.lastCommandKind

	// Update state for next call.
	e.lastErrorCount = currentErrors
	e.lastCommandKind = kind
	e.hadEdits = false

	// Only warn if:
	// 1. We have a previous count to compare against
	// 2. Edits occurred between the two verifications
	// 3. Error count increased beyond threshold
	// 4. We haven't exhausted warnings
	if prevErrors < 0 || !sameCommand || !hadEdits || !failed {
		return ""
	}

	increase := currentErrors - prevErrors
	if increase < regressionWarnThreshold {
		return ""
	}
	if e.warningCount >= maxRegressionWarnings {
		return ""
	}

	e.warningCount++
	debug.Log("error-regression",
		"error count increased from %d to %d after edits -- negative progress",
		prevErrors, currentErrors)
	return regressionGuidance(prevErrors, currentErrors, increase)
}

// regressionGuidance generates the guidance message for error count regression.
func regressionGuidance(prevErrors, currentErrors, increase int) string {
	return "[NEGATIVE PROGRESS WARNING] " +
		"Error count INCREASED from " + strconv.Itoa(prevErrors) + " to " +
		strconv.Itoa(currentErrors) + " (+" + strconv.Itoa(increase) +
		" new errors) after your edits. Your change likely introduced regressions " +
		"or unmasked previously hidden errors.\n\n" +
		"Before continuing to add more fixes:\n" +
		"1. Review the NEW errors carefully -- are they caused by your edit?\n" +
		"2. If so, your edit may have side effects you didn't consider. " +
		"Consider reverting and taking a different approach.\n" +
		"3. If the new errors were MASKED by previous errors (common in Go compilation), " +
		"fix them one at a time -- do not treat them as regressions.\n" +
		"4. Do NOT proceed with more edits until you understand why the error count went up."
}

// verifyCommandKind normalizes a verify command to its leading identity
// (go build / go test / make test / npm test ...) for same-command
// comparison (#1457-C).
func verifyCommandKind(cmd string) string {
	fields := strings.Fields(cmd)
	if len(fields) == 0 {
		return ""
	}
	kind := fields[0]
	if len(fields) > 1 {
		switch {
		case fields[0] == "go" && (fields[1] == "build" || fields[1] == "test" || fields[1] == "vet" || fields[1] == "lint"):
			kind = fields[0] + " " + fields[1]
		case fields[0] == "make" || fields[0] == "npm" || fields[0] == "yarn" || fields[0] == "pnpm" || fields[0] == "cargo":
			kind = fields[0] + " " + fields[1]
		}
	}
	return kind
}
