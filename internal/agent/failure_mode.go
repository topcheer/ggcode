package agent

// Failure Mode Classification - Meta-level strategy guidance for tool failures.
//
// Research basis: "Failure Mode Analysis in AI Agents" (arXiv:2506.10224) and
// "Self-Refine" / "Reflexion" pattern studies show that agents waste 3-5 iterations
// retrying the same failing approach before switching strategy. The key insight is
// that failures have qualitatively different modes:
//
//   - TRANSIENT: network blip, rate limit, timeout. Retrying the same action will
//     likely succeed. The agent should retry with backoff.
//   - STRUCTURAL: wrong approach, incorrect parameters, missing file. Retrying
//     the same action will fail again. The agent must change strategy.
//   - SYSTEMIC: environment issue (missing binary, permission denied, disk full,
//     auth failure). No amount of retrying or strategy change will help. The agent
//     should stop and report to the user.
//
// Gap in existing ggcode systems:
//   - error_classifier.go: classifies single error CONTENT (file_not_found, etc.)
//     and gives targeted guidance. But doesn't classify the FAILURE MODE (retry vs
//     change vs abort) or track whether the same mode keeps recurring.
//   - transient_retry.go: silently retries HTTP transient errors. Only handles
//     network-level transients, not tool-level.
//   - compounding_failure.go: sliding-window failure rate analysis. Detects that
//     failures are accumulating but doesn't classify WHY or advise WHAT to do.
//   - error_streak: counts consecutive failures and gives escalating "step back"
//     guidance. Generic — doesn't distinguish retryable from non-retryable.
//   - tool_error_fallback.go: suggests alternative tools on first failure.
//     Doesn't classify whether the failure is worth retrying at all.
//
// This component provides META-LEVEL strategy guidance by classifying the failure
// mode of each tool error, tracking mode frequency, and injecting high-level
// strategy directives when a dominant failure mode emerges.
//
// Design:
//   - Zero LLM cost (deterministic pattern matching)
//   - Fires when a failure mode appears 3+ times (dominant pattern)
//   - Each mode fires its guidance at most once per run
//   - Complements (not replaces) error_classifier, error_streak, compounding_failure
//   - Mode classification is content-based (not tool-based) for accuracy

import (
	"fmt"
	"strings"
	"sync"
)

// FailureMode represents a classified failure mode category.
type FailureMode int

const (
	FailureModeNone       FailureMode = iota
	FailureModeTransient              // retryable: network, rate limit, timeout
	FailureModeStructural             // wrong approach: incorrect params, missing file, type error
	FailureModeSystemic               // environment: permission denied, binary not found, auth
)

func (m FailureMode) String() string {
	switch m {
	case FailureModeTransient:
		return "transient"
	case FailureModeStructural:
		return "structural"
	case FailureModeSystemic:
		return "systemic"
	default:
		return "none"
	}
}

// failureModeState tracks failure mode frequencies across a run and injects
// strategy guidance when a dominant mode emerges.
type failureModeState struct {
	mu sync.Mutex

	// counts per failure mode
	transientCount  int
	structuralCount int
	systemicCount   int

	// which modes have already fired guidance
	fired map[FailureMode]bool

	// total tool calls (for ratio calculation)
	totalCalls int
}

func newFailureModeState() *failureModeState {
	return &failureModeState{
		fired: make(map[FailureMode]bool),
	}
}

func (s *failureModeState) reset() {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.transientCount = 0
	s.structuralCount = 0
	s.systemicCount = 0
	s.fired = make(map[FailureMode]bool)
	s.totalCalls = 0
}

// classifyFailureMode examines tool error content and returns the failure mode.
// This is a pure function (no state) for testability.
func classifyFailureMode(toolName, errorContent string) FailureMode {
	c := strings.ToLower(errorContent)

	// SYSTEMIC: environment issues that no retry or strategy change can fix.
	// These require user intervention.
	systemicPatterns := []string{
		"permission denied",
		"access denied",
		"operation not permitted",
		"command not found",
		"no such file or directory", // binary not found (not source file)
		"executable file not found",
		"not enough space",
		"disk full",
		"no space left",
		"too many open files",
		"connection refused", // service not running
		"authentication failed",
		"unauthorized",
		"forbidden",
		"api key",
		"invalid api key",
		"quota exceeded",
		"billing",
		"payment required",
	}
	for _, p := range systemicPatterns {
		if strings.Contains(c, p) {
			// Special case: "no such file or directory" for source files being
			// edited is structural (agent picked wrong path), not systemic.
			// Only classify as systemic when it's about a binary/command.
			if p == "no such file or directory" {
				// #335: "no such file or directory" in run_command output is
				// almost always a wrong argument PATH (self-healable structural:
				// fix the path or mkdir the target), not a missing environment
				// binary. Only whole-phrase binary/command-not-found matches are
				// systemic; the toolName=="run_command" escape hatch is removed.
				if strings.Contains(c, "executable file not found") || strings.Contains(c, "command not found") ||
					strings.Contains(c, "exec:") { // Go exec: binary lookup failure
					return FailureModeSystemic
				}
				// For read_file/edit_file and run_command argument paths, this is
				// structural (wrong path — agent can fix it).
				continue
			}
			return FailureModeSystemic
		}
	}

	// TRANSIENT: temporary failures that a retry can fix.
	transientPatterns := []string{
		"timeout",
		"timed out",
		"deadline exceeded",
		"context deadline",
		"rate limit",
		"rate_limit",
		"429",
		"503",
		"502",
		"service unavailable",
		"bad gateway",
		"connection reset",
		"connection closed",
		"eof",
		"i/o timeout",
		"temporary failure",
		"try again",
		"overloaded",
		"capacity",
	}
	for _, p := range transientPatterns {
		if strings.Contains(c, p) {
			return FailureModeTransient
		}
	}

	// STRUCTURAL: default. The approach is wrong — wrong params, wrong path,
	// type mismatch, missing import, syntax error in input, etc.
	return FailureModeStructural
}

// recordResult tracks a tool call result for failure mode analysis.
// Returns guidance string if a dominant failure mode pattern has emerged.
func (s *failureModeState) recordResult(toolName string, isError bool, errorContent string) string {
	s.mu.Lock()
	defer s.mu.Unlock()

	s.totalCalls++

	if !isError {
		return ""
	}

	mode := classifyFailureMode(toolName, errorContent)
	switch mode {
	case FailureModeTransient:
		s.transientCount++
	case FailureModeStructural:
		s.structuralCount++
	case FailureModeSystemic:
		s.systemicCount++
	default:
		return ""
	}

	return s.checkDominantMode()
}

// checkDominantMode returns guidance if a failure mode has become dominant.
// Must be called with lock held.
func (s *failureModeState) checkDominantMode() string {
	// Systemic mode fires immediately (1 occurrence) — these are hard blockers.
	// #335: wording no longer demands abandonment — verify the environment
	// first; only escalate to the user if it genuinely persists.
	if s.systemicCount >= 1 && !s.fired[FailureModeSystemic] {
		s.fired[FailureModeSystemic] = true
		return "[Failure Mode: SYSTEMIC] An environment-level error is blocking progress " +
			"(permission denied, missing binary, auth failure, or resource exhaustion). " +
			"Verify the environment before retrying (which <cmd>, ls the path, check " +
			"permissions/disk/api key). If the environment is genuinely broken, report it " +
			"to the user instead of iterating on workarounds."
	}

	// Transient mode fires after 3+ transient failures — retry strategy isn't working.
	if s.transientCount >= 3 && !s.fired[FailureModeTransient] {
		s.fired[FailureModeTransient] = true
		return fmt.Sprintf("%s",
			"[Failure Mode: TRANSIENT] "+fmt.Sprintf("%d", s.transientCount)+
				" failures appear to be transient (timeouts, rate limits). "+
				"Consider adding longer delays between retries, reducing request frequency, "+
				"or switching to a lighter approach that makes fewer API calls.")
	}

	// Structural mode fires after 4+ structural failures — the approach is wrong.
	if s.structuralCount >= 4 && !s.fired[FailureModeStructural] {
		s.fired[FailureModeStructural] = true
		return "[Failure Mode: STRUCTURAL] " + fmt.Sprintf("%d", s.structuralCount) +
			" failures suggest the current approach is fundamentally wrong " +
			"(wrong paths, incorrect parameters, type mismatches). " +
			"Do NOT retry the same approach. Step back, re-read the relevant code, " +
			"verify assumptions (file existence, types, import paths), and try a different strategy."
	}

	return ""
}
