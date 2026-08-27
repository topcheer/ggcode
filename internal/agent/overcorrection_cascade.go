package agent

// Overcorrection Cascade Detector
//
// Research basis:
//   - "MAXS: Meta-Adaptive Exploration with LLM Agents" (arXiv:2601.09259, Jan 2026):
//     identifies "locally myopic generation" where agents make disproportionately
//     large responses to minor signals, disrupting trajectory stability.
//   - "Automated Program Repair at Scale: Patch Granularity Matters" (2026 APR survey):
//     over-fixing -- applying a large structural change when a small targeted edit
//     would suffice -- is a dominant cause of regressions in agent-generated patches.
//   - "Trajectory Analysis: From Failure Attribution to Enhancement" (Wang et al., 2026):
//     overcorrection cascades where each "thorough fix" introduces new breakage
//     requiring further fixes, amplifying the blast radius.
//
// Problem: No deterministic detector checks whether an edit's SIZE is proportional
// to the triggering error/feedback. When an agent encounters a minor issue (unused
// import, wrong variable name, missing comma), the optimal fix is tiny (1-3 lines).
// Instead, agents often rewrite entire functions, refactor module structure, or
// replace whole code blocks -- introducing NEW errors that cascade. This is the
// INVERSE of diminishing_edit.go (which catches edits getting too small / polish
// spiral). Overcorrection catches edits that are too LARGE relative to need.
//
// Example cascade:
//   1. Lint: "unused import" (trivial, 1-line fix)
//   2. Agent rewrites entire function (200 lines) -- introduces typo
//   3. Build fails on the typo → Agent rewrites entire file (500 lines)
//   4. More breakage → Agent refactors the whole package
//
// Detection approach:
//   - When an error/feedback signal is observed (edit failure, build error result,
//     lint warning), record its "severity" (trivial / moderate / severe)
//   - When the NEXT edit arrives, compute the edit size
//   - If edit size vastly exceeds the expected fix size for that error class
//     (e.g., >50x for trivial errors, >15x for moderate), flag overcorrection
//   - Track consecutive overcorrections to detect cascading
//
// Interaction with existing detectors:
//   - diminishing_edit: detects edits getting progressively SMALLER (polish spiral)
//   - correction_spiral: tracks error SEVERITY escalation after fixes (different axis)
//   - premature_refactor: detects refactoring before understanding (intent-based)
//   - edit_blast_radius: tracks cross-file impact (scope-based, not size-vs-cause)
//   - fix_cascade: tracks wrong-hypothesis lock-in (cognitive, not proportional)
//
// This detector is unique: it measures the RATIO of fix size to problem size,
// catching the "using a sledgehammer to crack a nut" anti-pattern that introduces
// regressions. Zero LLM cost, deterministic.

import (
	"encoding/json"
	"fmt"
	"regexp"
	"strings"
	"sync"

	"github.com/topcheer/ggcode/internal/debug"
	"github.com/topcheer/ggcode/internal/util"
)

const (
	// overcorrectionWindowSize limits how many recent error→fix pairs we track.
	overcorrectionWindowSize = 8

	// overcorrectionMaxWarn caps warnings per run to avoid nagging.
	overcorrectionMaxWarn = 2

	// Error severity thresholds (bytes of diagnostic text used as proxy)
	overcorrectionTrivialErrorBytes  = 80  // short lint warning, single-line diagnostic
	overcorrectionModerateErrorBytes = 300 // multi-line build error, type mismatch

	// Expected fix size by error severity (bytes of edit content)
	overcorrectionTrivialExpectedFix  = 100  // ~1-3 lines
	overcorrectionModerateExpectedFix = 400  // ~5-15 lines
	overcorrectionSevereExpectedFix   = 1000 // ~20-40 lines

	// Disproportion ratios: edit size / expected fix size
	overcorrectionTrivialRatio  = 50.0 // edit is 50x larger than needed
	overcorrectionModerateRatio = 15.0 // edit is 15x larger than needed
	overcorrectionSevereRatio   = 8.0  // edit is 8x larger than needed (severe errors warrant larger fixes)

	// Minimum edit size to trigger (avoid flagging tiny edits as "overcorrection")
	overcorrectionMinEditBytes = 500

	// overcorrectionMaxErrorAge is the maximum number of steps an error can
	// remain pending before it expires. Prevents misattributing old errors
	// to unrelated edits (issue #27).
	overcorrectionMaxErrorAge = 10

	// Consecutive overcorrections needed for cascade detection
	overcorrectionCascadeMin = 2
)

// errorSeverity classifies the severity of an error/diagnostic signal.
type errorSeverity int

const (
	severityNone     errorSeverity = iota
	severityTrivial                // lint warning, unused import, formatting
	severityModerate               // build error, type mismatch, missing function
	severitySevere                 // runtime panic, test crash, linker error
)

// overcorrectionEntry records a single error→fix pair observation.
type overcorrectionEntry struct {
	errorSeverity errorSeverity
	editSize      int
	overcorrected bool
	filePath      string
}

// overcorrectionState tracks error→fix proportionality for cascade detection.
type overcorrectionState struct {
	mu              sync.Mutex
	entries         []overcorrectionEntry
	pendingErr      errorSeverity // severity of the most recent unaddressed error
	stepsSinceError int           // number of non-edit steps since last error (issue #27)
	warnCount       int
}

func newOvercorrectionState() *overcorrectionState {
	return &overcorrectionState{}
}

func (s *overcorrectionState) reset() {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.entries = nil
	s.pendingErr = severityNone
	s.stepsSinceError = 0
	s.warnCount = 0
}

// recordErrorSignal classifies and records an error/diagnostic from tool results.
func (s *overcorrectionState) recordErrorSignal(toolName string, resultContent string, isError bool) {
	s.mu.Lock()
	defer s.mu.Unlock()

	if isError {
		newSev := classifyErrorSeverity(toolName, resultContent)
		// Preserve max severity: don't let a trivial lint error overwrite a
		// severe panic error (issue #27).
		if newSev > s.pendingErr {
			s.pendingErr = newSev
		}
		s.stepsSinceError = 0
		return
	}

	// Also detect non-error diagnostic signals (lint warnings in build output)
	if sev := classifyDiagnosticSeverity(toolName, resultContent); sev > s.pendingErr {
		s.pendingErr = sev
		s.stepsSinceError = 0
	}
}

// recordEdit logs a successful edit and checks for overcorrection.
// Returns guidance if overcorrection or cascade is detected.
func (s *overcorrectionState) recordEdit(size int, filePath string) string {
	s.mu.Lock()
	defer s.mu.Unlock()

	// No pending error → cannot assess overcorrection
	if s.pendingErr == severityNone {
		return ""
	}

	// Expire stale pending errors: if N or more steps have passed since the
	// error was recorded without any edit, the error is likely unrelated to
	// this edit (issue #27).
	if s.stepsSinceError >= overcorrectionMaxErrorAge {
		s.pendingErr = severityNone
		return ""
	}

	errSev := s.pendingErr

	// Edits below minimum size are never overcorrections. They must NOT
	// consume the pending error either: an edit the detector itself deems too
	// small to assess must not destroy the attribution anchor a later,
	// assessable edit needs. Only reset the step counter so the error can
	// still expire via the stale-error path.
	if size < overcorrectionMinEditBytes {
		s.stepsSinceError = 0
		s.entries = append(s.entries, overcorrectionEntry{
			errorSeverity: errSev,
			editSize:      size,
			overcorrected: false,
			filePath:      filePath,
		})
		s.trimWindow()
		return ""
	}

	// Assessable edit: this edit addresses the pending error, consume it.
	s.pendingErr = severityNone

	overcorrected := isOvercorrection(errSev, size)

	s.entries = append(s.entries, overcorrectionEntry{
		errorSeverity: errSev,
		editSize:      size,
		overcorrected: overcorrected,
		filePath:      filePath,
	})
	s.trimWindow()

	if !overcorrected {
		return ""
	}

	// Count consecutive recent overcorrections
	consecutiveOver := 0
	for i := len(s.entries) - 1; i >= 0; i-- {
		if s.entries[i].overcorrected {
			consecutiveOver++
		} else {
			break
		}
	}

	// Suppress if already warned enough
	if s.warnCount >= overcorrectionMaxWarn {
		return ""
	}

	if consecutiveOver >= overcorrectionCascadeMin {
		s.warnCount++
		return overcorrectionCascadeWarning(errSev, size, consecutiveOver)
	}

	// Single overcorrection: warn only for trivial errors (most impactful)
	if errSev == severityTrivial {
		s.warnCount++
		return overcorrectionSingleWarning(errSev, size)
	}

	return ""
}

// trimWindow keeps the entries slice bounded.
func (s *overcorrectionState) trimWindow() {
	if len(s.entries) > overcorrectionWindowSize {
		s.entries = s.entries[len(s.entries)-overcorrectionWindowSize:]
	}
}

// isOvercorrection checks if the edit size is disproportionate to the error severity.
func isOvercorrection(errSev errorSeverity, editSize int) bool {
	expectedFix := overcorrectionSevereExpectedFix
	ratio := overcorrectionSevereRatio

	switch errSev {
	case severityTrivial:
		expectedFix = overcorrectionTrivialExpectedFix
		ratio = overcorrectionTrivialRatio
	case severityModerate:
		expectedFix = overcorrectionModerateExpectedFix
		ratio = overcorrectionModerateRatio
	case severitySevere:
		expectedFix = overcorrectionSevereExpectedFix
		ratio = overcorrectionSevereRatio
	default:
		return false
	}

	// Avoid division by zero
	if expectedFix == 0 {
		return editSize > overcorrectionMinEditBytes*10
	}

	actualRatio := float64(editSize) / float64(expectedFix)
	return actualRatio > ratio
}

// classifyErrorSeverity estimates error severity from the tool name and result content.
func classifyErrorSeverity(toolName string, content string) errorSeverity {
	c := strings.ToLower(content)
	l := len(c)

	switch {
	case toolName == "run_command" || toolName == "start_command":
		// Runtime failures are typically severe
		if strings.Contains(c, "panic:") || strings.Contains(c, "fatal error:") ||
			strings.Contains(c, "signal:") || strings.Contains(c, "segfault") {
			return severitySevere
		}
		// Build/compile errors
		if strings.Contains(c, "build failed") || strings.Contains(c, "compilation error") ||
			strings.Contains(c, "undefined:") || strings.Contains(c, "cannot find") ||
			strings.Contains(c, "syntax error") || strings.Contains(c, "type mismatch") {
			return severityModerate
		}
		// Short error output suggests trivial
		if l < overcorrectionTrivialErrorBytes {
			return severityTrivial
		}
		return severityModerate

	case strings.HasPrefix(toolName, "lsp_"):
		// LSP diagnostics: short = trivial, long = moderate
		if l < overcorrectionTrivialErrorBytes {
			return severityTrivial
		}
		return severityModerate

	case toolName == "edit_file" || toolName == "multi_edit_file" || toolName == "multi_file_edit":
		// Edit failures: usually old_text mismatch = trivial
		return severityTrivial

	case toolName == "git_commit" || toolName == "git_add":
		return severityTrivial

	default:
		if l > overcorrectionModerateErrorBytes {
			return severityModerate
		}
		return severityTrivial
	}
}

// diagnosticWarningRe anchors 'warning:' to real compiler/linter diagnostic
// shapes (issue #1141). It matches only:
//   - positional output such as "util.go:27:2: warning: unused import",
//     "main.c:41:9: warning: implicit declaration of function 'f'";
//   - a line that begins with the bare marker "warning:" (bare-line form).
//
// Prose without either anchor - README text like "We prefer using the
// Makefile", install hints like "Consider restarting your shell", titles like
// "Warning: behavior changed" embedded mid-sentence or in headings after other
// text on the same line - no longer classifies a SUCCESSFUL command result as
// carrying an error.
//
// Missing a real diagnostic here is fail-safe: it only skips recording a
// pending trivial error, whereas a false match corrupts the next edit's
// overcorrection verdict (#1141).
var diagnosticWarningRe = regexp.MustCompile(
	`(?im)(?:[^\s]+\.[a-zA-Z][a-zA-Z0-9]{0,7}:\d+:\d+:[ \t]+|^[ \t]*)warning:`,
)

// classifyDiagnosticSeverity detects non-error diagnostic signals in successful results.
// For example, a passing build may still contain warnings (unused import, etc.)
//
// Issue #1141: previously this performed lowercase substring matching with
// loosely-worded patterns ("prefer", "consider", "should be", bare
// "warning:"), which flagged benign successful command output and set a
// pending error that later poisoned unrelated large edits. The list below is
// restricted to unambiguous tool/linter phrases; "warning:" requires the
// diagnostic-format anchoring in diagnosticWarningRe.
func classifyDiagnosticSeverity(toolName string, content string) errorSeverity {
	if toolName != "run_command" && toolName != "start_command" {
		return severityNone
	}
	c := strings.ToLower(content)
	// Common lint warning patterns that indicate trivial issues. Keep this
	// list narrow and phrase-specific (#1141): generic English words are
	// matched by ordinary prose in successful command output.
	for _, pattern := range []string{
		"unused import", "unused variable", "declared but not used",
		"declared and not used",
		"missing newline", "format specifier", "ineffectual assignment",
		"deprecated:", "lint:", "gosimple", "staticcheck",
	} {
		if strings.Contains(c, pattern) {
			return severityTrivial
		}
	}
	if diagnosticWarningRe.MatchString(content) {
		return severityTrivial
	}
	return severityNone
}

func overcorrectionSingleWarning(errSev errorSeverity, editSize int) string {
	sevName := severityName(errSev)
	return fmt.Sprintf(
		"[overcorrection-cascade] Fix for %s error was %d bytes - too large. Use minimal targeted change.",
		sevName, editSize,
	)
}

func overcorrectionCascadeWarning(errSev errorSeverity, editSize int, consecutive int) string {
	_ = errSev
	_ = editSize
	return fmt.Sprintf(
		"[overcorrection-cascade] %d overcorrections - fixes far larger than errors warranted. Apply minimal surgical changes.",
		consecutive,
	)
}

func severityName(s errorSeverity) string {
	switch s {
	case severityTrivial:
		return "trivial"
	case severityModerate:
		return "moderate"
	case severitySevere:
		return "severe"
	default:
		return "unknown"
	}
}

// --- Agent wrapper methods (called from agent.go) ---

// overcorrectionRecordError records an error signal from a tool result.
func (a *Agent) overcorrectionRecordError(toolName string, resultContent string, isError bool) {
	if a.overcorrection == nil {
		return
	}
	a.overcorrection.recordErrorSignal(toolName, resultContent, isError)
}

// overcorrectionRecordEdit records a successful edit and returns guidance if overcorrection detected.
// recordNonEditStep increments stepsSinceError when a non-edit tool completes.
// Called from the agent loop for all tool calls that are not edits.
func (s *overcorrectionState) recordNonEditStep() {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.pendingErr != severityNone {
		s.stepsSinceError++
	}
}

func (a *Agent) overcorrectionRecordEdit(toolName string, args json.RawMessage) string {
	if a.overcorrection == nil {
		return ""
	}
	if !productiveEditTools[toolName] {
		return ""
	}
	size := measureEditSize(toolName, args)
	filePath := firstEditFilePath(toolName, args)
	hint := a.overcorrection.recordEdit(size, filePath)
	if hint != "" {
		debug.Log("agent", "overcorrection cascade detected: %s", util.Truncate(hint, 120))
	}
	return hint
}

// resetOvercorrection clears state for a new run.
func (a *Agent) resetOvercorrection() {
	if a.overcorrection != nil {
		a.overcorrection.reset()
	}
}
