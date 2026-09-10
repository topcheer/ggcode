package agent

// Silent Error Advancement Detection -- Unaddressed Error Proceeding Awareness
//
// Research basis: AgentForesight (arXiv:2605.08715, May 2026) introduces "online
// auditing" for multi-agent trajectories, finding that "a single decisive error
// is often silently accepted by downstream agents and cascades into
// trajectory-level failure." The key insight is that agents frequently encounter
// tool errors (failed edits, build failures, missing files) and then proceed to
// unrelated work without addressing the error, treating it as if it never
// happened. This silent acceptance is the #1 predictor of trajectory failure.
//
// Agentic Confidence Calibration (arXiv:2601.15778, Jan 2026) reinforces this:
// agents exhibit systematic overconfidence in failure states, proceeding as if
// errors are transient or irrelevant when they are actually structural.
//
// Self-correction benchmark data (agentmarketcap.ai, 2026): compound success
// probability drops to ~20% for 10-step workflows at 85% per-step accuracy.
// Each silently accepted error reduces trajectory success probability by 15%+.
//
// Gap in existing ggcode systems:
//   - error_cascade.go: clusters errors sharing the same ROOT RESOURCE. Does
//     not detect when the agent abandons any error and moves to new work.
//   - fix_cascade.go: tracks edit->verify->fail CYCLES. Only fires after
//     repeated edit+verify patterns, not when an error is simply ignored.
//   - compounding_failure: tracks aggregate FAILURE RATE across a sliding
//     window. Doesn't detect whether the agent tried to address failures.
//   - error_classifier: provides type-specific guidance on first occurrence.
//     Doesn't track whether the agent actually heeded that guidance.
//   - self_correction_gate: detects repeated edit-fail on the SAME file.
//     Doesn't cover errors from read/search/run_command that go unaddressed.
//   - error_streak: counts CONSECUTIVE failures. Resets on any success.
//     Doesn't detect non-consecutive silently-accepted errors.
//
// This detector fills the gap by tracking the pattern where:
//   1. A tool call returns an error (IsError=true or non-zero exit)
//   2. The agent's NEXT substantive action targets a DIFFERENT resource
//      (different file, different command, unrelated search)
//   3. This happens repeatedly without the agent revisiting the error
//
// After 3+ silently-accepted errors, inject guidance directing the agent to
// address unresolved errors before proceeding with new work.
//
// Design: zero LLM cost, deterministic tracking. Fires at most once per run.

import (
	"encoding/json"
	"fmt"
	"path/filepath"
	"strings"

	"github.com/topcheer/ggcode/internal/debug"
)

const (
	// silentErrorThreshold: number of silently-accepted errors before guidance.
	// Research suggests 3+ unaddressed errors strongly predict trajectory failure.
	silentErrorThreshold = 3
	// silentErrorMaxTracked: cap tracked errors to bound memory usage.
	silentErrorMaxTracked = 10
)

// silentErrorState tracks tool errors that the agent proceeds past without
// addressing â the "silent acceptance" pattern identified by AgentForesight.
type silentErrorState struct {
	// unresolvedErrors tracks errors that haven't been revisited.
	// Each entry records the tool name and a resource key (file path, command, etc.).
	unresolvedErrors []unresolvedError

	// silentAdvancementCount increments each time the agent makes a substantive
	// tool call targeting a different resource than an existing unresolved error.
	silentAdvancementCount int

	// fired tracks whether guidance has been injected this run.
	fired bool

	// lastErrorResource stores the resource key of the most recent error,
	// used to determine if the next action addresses it.
	lastErrorResource string
}

type unresolvedError struct {
	toolName     string
	resourceKey  string
	errorSnippet string
	iteration    int
}

func newSilentErrorState() *silentErrorState {
	return &silentErrorState{}
}

func (s *silentErrorState) reset() {
	s.unresolvedErrors = nil
	s.silentAdvancementCount = 0
	s.fired = false
	s.lastErrorResource = ""
}

// recordToolError records a tool error with its resource key.
// resourceKey is the primary resource the error relates to (file path,
// command string, search pattern, etc.).
func (s *silentErrorState) recordToolError(toolName, resourceKey, errorContent string, iteration int) {
	if len(s.unresolvedErrors) >= silentErrorMaxTracked {
		return
	}
	snippet := errorContent
	if len(snippet) > 120 {
		snippet = snippet[:120] + "..."
	}
	s.unresolvedErrors = append(s.unresolvedErrors, unresolvedError{
		toolName:     toolName,
		resourceKey:  resourceKey,
		errorSnippet: snippet,
		iteration:    iteration,
	})
	s.lastErrorResource = resourceKey
}

// recordToolAction records a non-error tool action. If the action does NOT
// address any unresolved error (different resource), it counts as a silent
// advancement. Returns guidance if the threshold is reached.
func (s *silentErrorState) recordToolAction(toolName, resourceKey string) string {
	if s.fired || len(s.unresolvedErrors) == 0 {
		return ""
	}

	// #1494 case D: tools whose resource model is "" (lsp_diagnostics,
	// git_diff, code_execution, todo_write, batch_replace, file_ops,
	// web_search, ...) must not count as silent advancement. The error
	// side stores empty-key errors, but the action side's early return
	// made EVERY uncovered-tool success an advancement++ - the standard
	// recovery trio (lsp_diagnostics + git_diff + todo_write after a
	// failed edit) triggered the "fix before continuing" indictment.
	// Symmetric skip: no key, no signal either way.
	if resourceKey == "" {
		return ""
	}

	// Check if this action addresses an unresolved error.
	if s.actionAddressesError(resourceKey, toolName) {
		// The agent is revisiting the error â clear matched errors.
		s.clearAddressedErrors(resourceKey)
		s.lastErrorResource = ""
		return ""
	}

	// This is a silent advancement: the agent is doing new work while
	// previous errors remain unaddressed.
	s.silentAdvancementCount++
	debug.Log("agent", "Silent error advancement #%d (tool=%s, resource=%s, unresolved=%d)",
		s.silentAdvancementCount, toolName, resourceKey, len(s.unresolvedErrors))

	if s.silentAdvancementCount >= silentErrorThreshold {
		return s.buildGuidance()
	}

	return ""
}

// actionAddressesError checks if the current tool action targets the same
// resource as an unresolved error (e.g., retrying the same edit, reading the
// error file, running the same command with fixes).
func (s *silentErrorState) actionAddressesError(resourceKey string, toolName string) bool {
	if resourceKey == "" {
		return false
	}
	resourceKey = normalizeResourceKey(resourceKey)
	for _, ue := range s.unresolvedErrors {
		ueKey := normalizeResourceKey(ue.resourceKey)
		if ueKey == "" {
			continue
		}
		// #1494 case C: command-class errors (2-token keys like "go
		// build") are canonically fixed by editing SOURCE FILES - a
		// different resource. The same-key assumption turned the textbook
		// "build fails -> edit source -> retest" flow into an
		// unaddressed-error indictment. Any file-editing action addresses
		// a command-class error.
		if isCommandKey(ueKey) && isEditTool(toolName) {
			return true
		}
		// Same resource key = directly addressing the error.
		if resourceKey == ueKey {
			return true
		}
		// Prefix match: editing the same file that had a build error,
		// or running a related command.
		if strings.HasPrefix(resourceKey, ueKey) || strings.HasPrefix(ueKey, resourceKey) {
			return true
		}
	}
	return false
}

// clearAddressedErrors removes errors whose resource matches the given key.
func (s *silentErrorState) clearAddressedErrors(resourceKey string) {
	resourceKey = normalizeResourceKey(resourceKey)
	filtered := s.unresolvedErrors[:0]
	for _, ue := range s.unresolvedErrors {
		ueKey := normalizeResourceKey(ue.resourceKey)
		// #1494 case E(a): an empty stored key must never match -
		// HasPrefix(k, "") is always true, so ONE legitimately addressed
		// error cleared ALL empty-key errors in the same sweep.
		if ueKey == "" {
			filtered = append(filtered, ue)
			continue
		}
		if ueKey == resourceKey ||
			pathPrefixMatch(resourceKey, ueKey) ||
			pathPrefixMatch(ueKey, resourceKey) {
			continue // Remove addressed error
		}
		filtered = append(filtered, ue)
	}
	s.unresolvedErrors = filtered
}

// pathPrefixMatch reports whether longer starts with shorter as a
// filesystem-path prefix (separator boundary), not a byte prefix:
// /a/foo.go must NOT match /a/foobar.go (#1494 case E(b)).
func pathPrefixMatch(longer, shorter string) bool {
	if !strings.HasPrefix(longer, shorter) {
		return false
	}
	if len(longer) == len(shorter) {
		return true
	}
	// Boundary: next char must be a separator (or the shorter endswith one).
	if strings.HasSuffix(shorter, "/") {
		return true
	}
	return len(longer) > len(shorter) && (longer[len(shorter)] == '/' || longer[len(shorter)] == filepath.Separator)
}

// isCommandKey reports whether a resource key is command-shaped (the
// 2-token form normalizeResourceKey produces for go/npm/cargo/python
// commands) rather than a file path or pattern.
func isCommandKey(key string) bool {
	fields := strings.Fields(key)
	if len(fields) != 2 {
		return false
	}
	switch fields[0] {
	case "go", "npm", "cargo", "python", "python3", "yarn", "pnpm", "make", "pytest":
		return true
	}
	return false
}

func (s *silentErrorState) buildGuidance() string {
	s.fired = true

	var examples []string
	for i, ue := range s.unresolvedErrors {
		if i >= 3 {
			break
		}
		examples = append(examples, fmt.Sprintf("  %d. [%s] %s", i+1, ue.toolName, ue.errorSnippet))
	}

	return fmt.Sprintf("[silent-error] %d unaddressed tool error(s) moved past. Fix before continuing:\n%s",
		len(s.unresolvedErrors), strings.Join(examples, "\n"))
}

// extractErrorResourceKey extracts the primary resource (file path, command,
// search pattern) from a tool call's arguments for matching purposes.
func extractErrorResourceKey(toolName string, args json.RawMessage) string {
	switch toolName {
	case "edit_file", "write_file", "multi_edit_file", "read_file":
		return extractFilePathFromArgs(toolName, args)
	case "run_command", "start_command":
		return normalizeResourceKey(extractCommandFromArgs(args))
	case "grep", "search_files":
		return extractJSONStringField(args, "pattern")
	case "glob":
		return extractJSONStringField(args, "pattern")
	default:
		return ""
	}
}

// normalizeResourceKey normalizes a resource key for matching: trims whitespace,
// lowercases commands, removes common prefixes.
func normalizeResourceKey(key string) string {
	key = strings.TrimSpace(key)
	// For commands, use the first meaningful token (the command itself)
	// to match related commands (e.g., "go build ./..." and "go build ./pkg/...").
	if strings.Contains(key, " ") {
		parts := strings.Fields(key)
		if len(parts) > 1 && (parts[0] == "go" || parts[0] == "npm" || parts[0] == "cargo" || strings.HasPrefix(parts[0], "python")) {
			// For multi-word commands, keep first 2 tokens (e.g., "go build", "npm test")
			if len(parts) >= 2 {
				return strings.ToLower(parts[0] + " " + parts[1])
			}
		}
	}
	return strings.ToLower(key)
}
