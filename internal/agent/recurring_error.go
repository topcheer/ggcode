package agent

// Recurring Error Fingerprint Detection
//
// Research basis: AgentDebug (arXiv:2509.25370) and trajectory analysis studies
// show that the #1 cause of agent failure in real coding tasks is making
// incremental edits that don't address the root cause — the agent edits a file,
// runs the build, gets an error, edits again (a different or nearby spot), runs
// the build, and gets the SAME error. This wastes iterations without progress.
//
// Existing ggcode systems do NOT detect this pattern:
//   - loop_detect: only catches CONSECUTIVE identical tool calls. File edits
//     between build runs break the consecutive streak, so the loop detector
//     never fires.
//   - error_classifier: fires once per error category, then stops. It can't
//     tell that the SAME error has returned after multiple edit cycles.
//   - repetition_tracker: tracks FAILED FILE EDITS, not recurring command
//     output. A build that fails isn't a "failed edit."
//   - confidence: tracks aggregate success rates, not error-content recurrence.
//
// This component fills the gap with deterministic, zero-LLM-cost detection:
//
//   1. FINGERPRINT EXTRACTION: when a build/test command returns errors, extract
//      a normalized error signature (strip line numbers, column numbers,
//      absolute paths, timestamps) so trivial edits that shift line numbers
//      don't create a new fingerprint.
//
//   2. RECURRENCE TRACKING: track how many times each fingerprint appears
//      across iterations. Only count occurrences where file edits happened
//      between them — otherwise it's a consecutive-duplicate issue already
//      handled by loop_detect.
//
//   3. ESCALATING GUIDANCE: when the same fingerprint recurs after edits,
//      inject targeted guidance telling the agent its edits aren't addressing
//      the root cause. Escalate at 2nd and 3rd occurrences, then stop.

import (
	"crypto/sha256"
	"encoding/hex"
	"regexp"
	"strconv"
	"strings"
	"sync"

	"github.com/topcheer/ggcode/internal/debug"
)

const (
	// recurringSoftThreshold: same error appears this many times with edits
	// between → first guidance (gentle nudge to re-examine).
	recurringSoftThreshold = 2

	// recurringHardThreshold: same error persists → strong guidance to stop
	// incremental edits and find the root cause.
	recurringHardThreshold = 3

	// maxFingerprintLines: only the first N error lines form the fingerprint.
	// This keeps the signature stable when later lines vary (e.g., different
	// suggestion text) while capturing the distinctive error identity.
	maxFingerprintLines = 5
)

// recurringErrorState tracks build/test error fingerprints across iterations
// to detect the "edits don't fix the root cause" anti-pattern.
type recurringErrorState struct {
	mu sync.Mutex

	// fingerprintCounts maps normalized error fingerprint → number of times
	// it has appeared during this run.
	fingerprintCounts map[string]int

	// editsSinceLastError tracks file edits since the last build error was
	// recorded. Incremented on successful file edits; reset when a new error
	// is recorded. Used to distinguish "same error, no edits" (a loop_detect
	// issue) from "same error despite edits" (the root-cause gap we target).
	editsSinceLastError int

	// firedLevels maps fingerprint → highest guidance level already fired.
	// Prevents re-firing the same level for the same fingerprint.
	firedLevels map[string]int
}

func newRecurringErrorState() *recurringErrorState {
	return &recurringErrorState{
		fingerprintCounts: make(map[string]int),
		firedLevels:       make(map[string]int),
	}
}

func (r *recurringErrorState) reset() {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.fingerprintCounts = make(map[string]int)
	r.editsSinceLastError = 0
	r.firedLevels = make(map[string]int)
}

// recordEdit increments the edit counter. Called after a successful file edit.
func (r *recurringErrorState) recordEdit() {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.editsSinceLastError++
}

// recordBuildError processes a failed build/test command output, extracts its
// fingerprint, and returns guidance if the same error has recurred after edits.
// Returns empty string if no guidance is warranted.
func (r *recurringErrorState) recordBuildError(output string) string {
	fp := fingerprintBuildError(output)
	if fp == "" {
		return ""
	}

	r.mu.Lock()
	defer r.mu.Unlock()

	r.fingerprintCounts[fp]++
	count := r.fingerprintCounts[fp]

	// Only count as a meaningful recurrence if edits happened between
	// occurrences. If editsSinceLastError == 0, the agent just re-ran the
	// build without changing anything — that's a loop_detect issue, not ours.
	hadEdits := r.editsSinceLastError > 0
	r.editsSinceLastError = 0 // reset for the next interval

	if !hadEdits || count < recurringSoftThreshold {
		return ""
	}

	fired := r.firedLevels[fp]
	var guidance string

	if count >= recurringHardThreshold && fired < 2 {
		r.firedLevels[fp] = 2
		guidance = recurringHardGuidance(count)
		debug.Log("recurring-error", "SAME error persisted across %d build cycles despite edits — injecting hard guidance", count)
	} else if count >= recurringSoftThreshold && fired < 1 {
		r.firedLevels[fp] = 1
		guidance = recurringSoftGuidance(count)
		debug.Log("recurring-error", "SAME error recurred across %d build cycles with edits between — injecting soft guidance", count)
	}

	return guidance
}

// --- Fingerprint extraction ---

// Pre-compiled regexes for error-line detection and normalization.
var (
	// errorLineMarkers identifies lines that carry diagnostic information.
	errorLineMarkers = []string{
		"error", "error[", "error:", "error e",
		"fail", "fail:", "--- fail", "panic:",
		"undefined", "cannot find", "not defined",
		"expected", "mismatch", "incompatible",
		"fatal", "traceback", "exception",
		"nameerror", "typeerror", "valueerror",
		"syntaxerror", "attributeerror",
	}

	// lineColPattern strips Go-style line:col references: ./path/file.go:42:5
	lineColPattern = regexp.MustCompile(`:\d+:\d+`)
	// lineNumberPattern strips standalone line references: file.go:42
	lineNumberPattern = regexp.MustCompile(`:\d+(?::\d+)?`)
	// lineWordPattern strips "line 42", "at line 42" patterns
	lineWordPattern = regexp.MustCompile(`(?i)\bline\s+\d+`)
	// pathPrefixPattern strips directory prefixes, keeping only the basename.
	// Matches sequences like ./a/b/ or /a/b/c/ and removes them so only
	// the final file.ext remains. This makes fingerprints stable across
	// path relocations (relative vs absolute, different root directories).
	pathPrefixPattern = regexp.MustCompile(`(?:/?[\w.\-]+/)+`)
	// wsPattern collapses multiple whitespace into single space
	wsPattern = regexp.MustCompile(`\s+`)
)

// fingerprintBuildError extracts a normalized error fingerprint from build/test
// command output. The fingerprint is stable across trivial edits that change
// line numbers but keep the same underlying error.
//
// Returns empty string if no error lines are found in the output.
func fingerprintBuildError(output string) string {
	if output == "" {
		return ""
	}

	lines := strings.Split(output, "\n")
	var sigLines []string

	for _, line := range lines {
		trimmed := strings.TrimSpace(line)
		if trimmed == "" || len(trimmed) < 5 {
			continue
		}
		lower := strings.ToLower(trimmed)
		if !errorContainsAny(lower, errorLineMarkers...) {
			continue
		}
		normalized := normalizeErrorLine(trimmed)
		if normalized != "" {
			sigLines = append(sigLines, normalized)
		}
		if len(sigLines) >= maxFingerprintLines {
			break
		}
	}

	if len(sigLines) == 0 {
		return ""
	}

	joined := strings.Join(sigLines, "\n")
	h := sha256.Sum256([]byte(joined))
	return hex.EncodeToString(h[:8])
}

// normalizeErrorLine strips volatile elements (line/column numbers, absolute
// paths, excessive whitespace) from a single error line to produce a stable
// signature component.
func normalizeErrorLine(line string) string {
	s := line

	// Strip Go-style :line:col references first (most specific).
	s = lineColPattern.ReplaceAllString(s, ":N")
	// Then strip remaining :line references (after basename).
	s = lineNumberPattern.ReplaceAllString(s, ":N")
	// Strip "line 42" word references.
	s = lineWordPattern.ReplaceAllString(s, "line N")

	// Normalize paths to basenames: strip directory prefixes so
	// /home/user/project/internal/foo.go and ./internal/foo.go both
	// become foo.go. This prevents path-changes from creating new
	// fingerprints when the underlying error is identical.
	s = stripPathToBasename(s)

	// Collapse whitespace.
	s = wsPattern.ReplaceAllString(s, " ")
	s = strings.TrimSpace(s)

	return s
}

// stripPathToBasename replaces directory path prefixes with empty string,
// leaving only the basename. E.g. ./internal/agent/foo.go → foo.go,
// /home/user/project/foo.go → foo.go. This makes fingerprints stable across
// path relocations.
func stripPathToBasename(s string) string {
	return pathPrefixPattern.ReplaceAllString(s, "")
}

// (containsAny is provided by error_classifier.go as errorContainsAny.)

// --- Guidance text ---

func recurringSoftGuidance(count int) string {
	return "[Recurring error: the SAME build/test error has appeared " +
		strconv.Itoa(count) + " times despite your edits. " +
		"Your changes are not addressing the root cause. " +
		"Re-read the FULL error output, identify the exact failing line and its cause, " +
		"and make a targeted fix rather than incremental tweaks.]"
}

func recurringHardGuidance(count int) string {
	return "[CRITICAL: the same error has persisted across " +
		strconv.Itoa(count) + " edit-and-rebuild cycles. Incremental edits will not fix this. " +
		"STOP and analyze: (1) read the complete error message word by word, " +
		"(2) trace the root cause — is it a missing import, wrong type, naming mismatch, or architectural issue? " +
		"(3) make ONE decisive fix that directly addresses the error, not a workaround.]"
}

// --- Agent integration methods ---

// recurringErrorRecordEdit records a successful file edit for recurrence tracking.
func (a *Agent) recurringErrorRecordEdit() {
	if a.recurringError != nil {
		a.recurringError.recordEdit()
	}
}

// recurringErrorCheckCommand processes a build/test command result and returns
// guidance if the same error has recurred across edit cycles.
func (a *Agent) recurringErrorCheckCommand(toolName string, args []byte, resultContent string, isError bool) string {
	if a.recurringError == nil {
		return ""
	}
	// Only track run_command results that are verify commands.
	if toolName != "run_command" {
		return ""
	}
	cmd := extractCommandFromArgs(args)
	if cmd == "" || !isVerifyCommand(cmd) {
		return ""
	}
	// Only process outputs that contain errors. isError may be true, or the
	// output may contain error markers even if the exit code wasn't captured.
	if !isError && !hasErrorMarkers(resultContent) {
		return ""
	}
	return a.recurringError.recordBuildError(resultContent)
}

// resetRecurringError clears state for a new run.
func (a *Agent) resetRecurringError() {
	if a.recurringError != nil {
		a.recurringError.reset()
	}
}

// hasErrorMarkers does a quick check whether the output contains recognizable
// error/failure indicators.
func hasErrorMarkers(output string) bool {
	if output == "" {
		return false
	}
	lower := strings.ToLower(output)
	return errorContainsAny(lower, errorLineMarkers...)
}
