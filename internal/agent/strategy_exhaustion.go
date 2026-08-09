package agent

// Strategy Exhaustion Detector
//
// Research basis:
//   - EEA (Entropy-Based Evaluation of AI Agents, 2025): identifies
//     "robustness entropy" as a key behavioral dimension - measures how
//     diverse an agent's recovery strategies are when encountering errors.
//     Low robustness entropy = stuck retrying the same approach; but
//     persistently HIGH entropy with no convergence = flailing without
//     resolution. Both are failure modes.
//   - MiRA (Google DeepMind, 2026): milestone-based subgoal decomposition
//     helps when local strategies fail - the agent should decompose or
//     escalate rather than try yet another variation of the same approach.
//   - "Taxonomy of Failure Modes in Agentic AI Systems" (arXiv:2508.13143):
//     identifies "repeated failure despite strategy changes" as a distinct
//     category - the agent keeps trying new approaches but none work.
//
// Problem: When an agent encounters the same error repeatedly and has already
// tried 4+ DISTINCT recovery strategies (different tool combinations, different
// files, different approaches), continuing to try yet another variation is
// unlikely to succeed. The agent should step back: decompose the task, revisit
// the root cause from a fundamentally different angle, or ask the user.
//
// This is the INVERSE of errStrategyLoop (which detects repeating the SAME
// strategy). Strategy exhaustion detects trying DIVERSE strategies for the
// same intractable problem - "thrashing" through approaches without
// convergence.
//
// Distinction from existing detectors:
//   - errStrategyLoop: SAME strategy repeated for same error (procedural
//     memory gap). We detect DIVERSE strategies exhausted.
//   - recurringError: same error fingerprint recurs. We track HOW MANY
//     distinct recovery attempts preceded each recurrence.
//   - fixCascade: wrong-hypothesis lock-in (one hypothesis, many failed
//     fixes). We detect multiple DIFFERENT hypotheses exhausted.
//   - solutionFixation: diagnosis anchoring on one file. We detect
//     diagnosis SPREAD across many files without resolution.
//
// Approach:
//   - Maintain a rolling record of tool calls with their error status
//   - When an error occurs, fingerprint the error (normalized signature)
//   - Between consecutive occurrences of the same error fingerprint, record
//     the set of tool names used as a "recovery strategy signature"
//   - If 4+ DISTINCT strategy signatures appear for the same error
//     fingerprint and the error still recurs, inject guidance to
//     decompose/escalate/reassess
//   - Zero LLM cost, deterministic

import (
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"regexp"
	"strings"
	"sync"

	"github.com/topcheer/ggcode/internal/debug"
)

const (
	// seMaxDistinctStrategies: number of distinct recovery strategy signatures
	// for the same error before triggering. 4 means the agent has tried four
	// fundamentally different approaches and none resolved the error.
	seMaxDistinctStrategies = 4

	// seMaxWarnings: cap warnings per run to avoid noise.
	seMaxWarnings = 2

	// seFingerprintMaxLen: maximum length of error content to fingerprint.
	seFingerprintMaxLen = 500

	// seToolHistoryWindow: number of tool calls to retain for computing
	// recovery strategy signatures between error occurrences.
	seToolHistoryWindow = 30
)

// seStrategyExhaustionState tracks tool calls and errors to detect strategy
// exhaustion (diverse recovery strategies failing for the same error).
type seStrategyExhaustionState struct {
	mu sync.Mutex

	// toolHistory records recent tool calls (name only) for computing
	// recovery strategy signatures.
	toolHistory []string

	// errorClusters maps error fingerprint to cluster data.
	errorClusters map[string]*seErrorCluster

	// warningCount caps warnings per run.
	warningCount int

	// lastWarnIteration prevents consecutive-injection noise.
	lastWarnIteration int
}

// seErrorCluster tracks recovery attempts for a specific error fingerprint.
type seErrorCluster struct {
	// fingerprintsSeen: number of times this error fingerprint appeared.
	fingerprintsSeen int

	// strategySignatures: set of distinct recovery tool-name sequences
	// observed between consecutive occurrences of this error.
	strategySignatures map[string]bool

	// toolsSinceLastError: tool names accumulated since the last occurrence
	// of this error, used to build the next strategy signature.
	toolsSinceLastError []string
}

func newStrategyExhaustionState() *seStrategyExhaustionState {
	return &seStrategyExhaustionState{
		toolHistory:       make([]string, 0, seToolHistoryWindow),
		errorClusters:     make(map[string]*seErrorCluster),
		lastWarnIteration: -1,
	}
}

func (s *seStrategyExhaustionState) reset() {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.toolHistory = s.toolHistory[:0]
	s.errorClusters = make(map[string]*seErrorCluster)
	s.warningCount = 0
	s.lastWarnIteration = -1
}

// recordToolCall records a tool call result. It tracks tool names in the
// history and, if the result is an error, fingerprints the error and updates
// the cluster. Returns a guidance message if strategy exhaustion is detected,
// empty string otherwise.
func (s *seStrategyExhaustionState) recordToolCall(toolName string, isError bool, errorContent string, iteration int) string {
	s.mu.Lock()
	defer s.mu.Unlock()

	// Append to rolling tool history (bounded).
	s.toolHistory = append(s.toolHistory, toolName)
	if len(s.toolHistory) > seToolHistoryWindow {
		s.toolHistory = s.toolHistory[len(s.toolHistory)-seToolHistoryWindow:]
	}

	if !isError {
		// Accumulate this tool into all clusters' "tools since last error" lists.
		// This builds the recovery strategy signature for each active cluster.
		for _, cluster := range s.errorClusters {
			if cluster.fingerprintsSeen > 0 {
				cluster.toolsSinceLastError = append(cluster.toolsSinceLastError, toolName)
			}
		}
		return ""
	}

	// Error occurred: fingerprint it.
	fp := seFingerprintError(errorContent)

	cl, exists := s.errorClusters[fp]
	if !exists {
		cl = &seErrorCluster{
			strategySignatures: make(map[string]bool),
		}
		s.errorClusters[fp] = cl
	}

	// If this isn't the first occurrence, record the recovery strategy used
	// between the previous and current occurrence.
	if cl.fingerprintsSeen > 0 && len(cl.toolsSinceLastError) > 0 {
		sig := seStrategySignature(cl.toolsSinceLastError)
		if !cl.strategySignatures[sig] {
			cl.strategySignatures[sig] = true
			debug.Log("agent", "strategy exhaustion: new distinct strategy #%d for error %s",
				len(cl.strategySignatures), fp[:seMin(16, len(fp))])
		}
	}

	cl.fingerprintsSeen++
	cl.toolsSinceLastError = cl.toolsSinceLastError[:0]

	// Check for exhaustion: 4+ distinct strategies tried and error still recurs.
	if len(cl.strategySignatures) >= seMaxDistinctStrategies &&
		cl.fingerprintsSeen >= seMaxDistinctStrategies+1 {
		if s.warningCount < seMaxWarnings && s.lastWarnIteration != iteration {
			s.warningCount++
			s.lastWarnIteration = iteration
			return seBuildWarning(len(cl.strategySignatures), cl.fingerprintsSeen)
		}
	}

	return ""
}

// seFingerprintError creates a normalized signature from error content.
// Extracts the key error message, strips variable parts (line numbers,
// memory addresses, timestamps, hex hashes) to group similar errors.
var (
	seLineNumRe  = regexp.MustCompile(`:\d+:\d*`)
	seHexRe      = regexp.MustCompile(`0x[0-9a-fA-F]+`)
	seHashRe     = regexp.MustCompile(`\b[0-9a-fA-F]{12,}\b`)
	seNumRe      = regexp.MustCompile(`\b\d+\b`)
	seStackRe    = regexp.MustCompile(`\(goroutine \d+.*?\)`)
	seFilePathRe = regexp.MustCompile(`/[^\s:]+\.go`)
	seWSRe       = regexp.MustCompile(`\s+`)
)

func seFingerprintError(content string) string {
	c := content
	if len(c) > seFingerprintMaxLen {
		c = c[:seFingerprintMaxLen]
	}
	// Normalize variable parts to group similar errors.
	c = seFilePathRe.ReplaceAllString(c, "<path>")
	c = seLineNumRe.ReplaceAllString(c, ":<ln>")
	c = seHexRe.ReplaceAllString(c, "0x<addr>")
	c = seHashRe.ReplaceAllString(c, "<hash>")
	c = seNumRe.ReplaceAllString(c, "<n>")
	c = seStackRe.ReplaceAllString(c, "")
	c = seWSRe.ReplaceAllString(c, " ")
	c = strings.TrimSpace(c)

	h := sha256.Sum256([]byte(c))
	return hex.EncodeToString(h[:8])
}

// seStrategySignature computes a hash of the recovery tool-name sequence
// to identify distinct strategies. Two sequences with the same set of tools
// in the same order are considered the same strategy.
func seStrategySignature(tools []string) string {
	joined := strings.Join(tools, "|")
	h := sha256.Sum256([]byte(joined))
	return hex.EncodeToString(h[:8])
}

// seBuildWarning constructs the guidance message.
func seBuildWarning(distinctStrategies, totalOccurrences int) string {
	return fmt.Sprintf(
		"[Strategy Exhaustion] You've tried %d distinct recovery strategies "+
			"for an error that has recurred %d times - none resolved it. "+
			"Continuing to try variations is unlikely to succeed. Consider: "+
			"(1) decompose the problem into smaller subtasks and verify each "+
			"independently, (2) step back and re-examine the root cause from a "+
			"fundamentally different angle, or (3) ask the user for guidance. "+
			"(Research: EEA robustness entropy, MiRA subgoal decomposition)",
		distinctStrategies, totalOccurrences)
}

// seMin returns the smaller of a or b.
func seMin(a, b int) int {
	if a < b {
		return a
	}
	return b
}
