package agent

import (
	"encoding/json"
	"fmt"
	"strings"
	"sync"

	"github.com/topcheer/ggcode/internal/debug"
)

// Action Annihilation Detector -- Cross-Tool Side-Effect Cancellation Awareness
//
// Research basis:
//   - "A Self-Improving Coding Agent" (SICA, arXiv:2504.15228, NeurIPS 2025):
//     identifies trajectory waste as the primary bottleneck in agent performance.
//     Agents waste 17-53% of iterations on actions that don't advance the task.
//     The paper's key insight: non-gradient learning via reflection on which
//     actions contributed to success vs. waste.
//   - "Unsupervised Approaches to Futile Cycle Detection in AI Agents" (IBM
//     Research, 2025): introduces the concept of repetitive futile cycles where
//     the agent revisits state-space regions without forward progress.
//   - SWE-bench trajectory analysis: ~15% of failing trajectories contain
//     action reversal pairs where the agent creates, then destroys, the same
//     state -- net-zero progress on wasted iterations.
//
// Problem: AI coding agents sometimes issue tool calls whose side effects
// cancel a prior call's side effects within the same run. Examples:
//
//	git_add("file.go") → git_reset("file.go")     // stage then unstage
//	git_commit("msg")   → git_revert(commit)       // commit then revert
//	edit_file(X)        → undo_edit                // edit then undo
//	mkdir("dir")        → delete("dir")            // create then destroy
//	git_checkout(B)     → git_checkout(A)          // switch then switch back
//	start_command(cmd)  → stop_command(job)        // start then kill (no read)
//
// Each annihilation pair represents wasted iterations -- the agent spent tokens
// and time creating state, then more tokens destroying it, with net-zero
// forward progress. Left unchecked, these pairs compound: the agent may repeat
// the cycle multiple times across a run.
//
// Distinct from existing detectors:
//   - edit_oscillation_detect.go: tracks SEMANTIC content reversal on the SAME
//     file (old_text/new_text swap). Action annihilation tracks CROSS-TOOL
//     side-effect cancellation (git_add vs git_reset, mkdir vs delete).
//   - futile_cycle.go: tracks read revisits without mutation. Action annihilation
//     tracks MUTATING actions that cancel each other.
//   - bgorphan_detect.go: tracks unchecked background commands. Action annihilation
//     tracks explicitly stopped commands that were never read.
//   - tool_sequence.go: tracks suboptimal ORDERING. Action annihilation tracks
//     actions that produce ZERO net state change.
//
// What it detects: When the agent's most recent tool call cancels a prior tool
// call's side effects within a sliding window. When detected, it injects a
// guidance nudge to reflect on whether the approach is productive.

// annihilationPair defines a cancellation relationship between two tool calls.
type annihilationPair struct {
	// priorTool is the tool whose side effect gets cancelled.
	priorTool string
	// cancelTool is the tool that cancels the prior side effect.
	cancelTool string
	// matchFn returns true if the cancel call's args match the prior call.
	// If nil, any prior→cancel sequence within the window matches.
	matchFn func(priorArgs, cancelArgs json.RawMessage) bool
	// description for the warning message.
	description string
}

// trackedAction records a mutating tool call for annihilation matching.
type trackedAction struct {
	tool      string
	args      json.RawMessage
	iteration int
}

// actionAnnihilateState tracks tool calls to detect side-effect cancellation pairs.
type actionAnnihilateState struct {
	mu sync.Mutex

	// recent actions within the lookback window (max 20).
	actions []trackedAction

	// cancellation count this run.
	cancelCount int
	// max warnings per run.
	maxWarns int
	// warns issued so far.
	warnsIssued int
	// lookback iterations (how many recent tool calls to search for a match).
	lookback int
}

// newActionAnnihilateState creates a new detector instance.
func newActionAnnihilateState() *actionAnnihilateState {
	return &actionAnnihilateState{
		maxWarns: 2,
		lookback: 20,
	}
}

func (s *actionAnnihilateState) reset() {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.actions = nil
	s.cancelCount = 0
	s.warnsIssued = 0
}

// annihilationPairs defines known cancellation patterns.
// Each pair (priorTool → cancelTool) represents a net-zero state change.
var annihilationPairs = []annihilationPair{
	{
		priorTool:   "git_add",
		cancelTool:  "git_reset",
		description: "git_add then git_reset (staged then unstaged the same files)",
		matchFn:     matchGitAddReset,
	},
	{
		priorTool:   "git_commit",
		cancelTool:  "git_revert",
		description: "git_commit then git_revert (committed then reverted)",
		matchFn:     nil, // any commit→revert pair within window
	},
	{
		priorTool:   "edit_file",
		cancelTool:  "undo_edit",
		description: "edit_file then undo_edit (edited then undid your own change)",
		matchFn:     nil,
	},
	{
		priorTool:   "multi_edit_file",
		cancelTool:  "undo_edit",
		description: "multi_edit_file then undo_edit (edited then undid your own change)",
		matchFn:     nil,
	},
	{
		priorTool:   "write_file",
		cancelTool:  "undo_edit",
		description: "write_file then undo_edit (wrote then undid your own change)",
		matchFn:     nil,
	},
	{
		priorTool:   "file_ops",
		cancelTool:  "file_ops",
		description: "file_ops mkdir then file_ops delete on the same path",
		matchFn:     matchMkdirDelete,
	},
	{
		priorTool:   "git_checkout",
		cancelTool:  "git_checkout",
		description: "git_checkout switch then switch back (branch thrashing)",
		matchFn:     matchCheckoutRoundtrip,
	},
}

// recordToolCall processes a tool call and returns a warning if an annihilation
// pair is detected. The warning is empty if no cancellation is found or if the
// warning cap has been reached.
func (s *actionAnnihilateState) recordToolCall(toolName string, args json.RawMessage, iteration int) string {
	s.mu.Lock()
	defer s.mu.Unlock()

	warning := s.checkAnnihilation(toolName, args, iteration)

	// Record this action for future matching.
	s.actions = append(s.actions, trackedAction{
		tool:      toolName,
		args:      args,
		iteration: iteration,
	})

	// Trim to lookback window.
	if len(s.actions) > s.lookback {
		s.actions = s.actions[len(s.actions)-s.lookback:]
	}

	return warning
}

// checkAnnihilation examines whether the current tool call cancels a prior one.
func (s *actionAnnihilateState) checkAnnihilation(currentTool string, currentArgs json.RawMessage, iteration int) string {
	for _, pair := range annihilationPairs {
		if currentTool != pair.cancelTool {
			continue
		}

		// Search recent actions for a matching prior call.
		for i := len(s.actions) - 1; i >= 0; i-- {
			prior := s.actions[i]
			if prior.tool != pair.priorTool {
				continue
			}
			// Only match if they're close enough in the window (already trimmed).
			if pair.matchFn != nil && !pair.matchFn(prior.args, currentArgs) {
				continue
			}

			s.cancelCount++
			if s.warnsIssued >= s.maxWarns {
				return ""
			}
			s.warnsIssued++
			debug.Log("agent", "Iteration %d: action annihilation detected: %s (pair #%d)",
				iteration, pair.description, s.cancelCount)
			return formatAnnihilationWarning(pair.description, prior.iteration, iteration, s.cancelCount)
		}
	}

	return ""
}

func formatAnnihilationWarning(desc string, priorIter, curIter int, totalCancels int) string {
	var sb strings.Builder
	sb.WriteString(fmt.Sprintf("[Action Annihilation] Iterations %d-%d: %s.\n", priorIter, curIter, desc))
	sb.WriteString("These two actions produced net-zero state change -- the work between them was wasted.\n")
	if totalCancels > 1 {
		sb.WriteString(fmt.Sprintf("This is cancellation pair #%d this run. Repeated annihilation signals an undecided or oscillating approach.\n", totalCancels))
	}
	sb.WriteString("Reflect: what are you actually trying to accomplish? Commit to a single approach and execute it.")
	return sb.String()
}

// --- Matchers ---

// matchGitAddReset checks if git_reset targets the same files that git_add staged.
func matchGitAddReset(priorArgs, cancelArgs json.RawMessage) bool {
	priorFiles := extractStringSlice(priorArgs, "files")
	if len(priorFiles) == 0 || (len(priorFiles) == 1 && (priorFiles[0] == "." || priorFiles[0] == "*")) {
		// git_add with ["."], ["*"], or no specific files -- match any reset.
		return true
	}
	resetFiles := extractStringSlice(cancelArgs, "files")
	if len(resetFiles) == 0 {
		// git_reset with no specific files -- resets everything, matches.
		return true
	}
	// Check if any file overlaps.
	priorSet := make(map[string]bool, len(priorFiles))
	for _, f := range priorFiles {
		priorSet[f] = true
	}
	for _, f := range resetFiles {
		if priorSet[f] {
			return true
		}
	}
	return false
}

// matchMkdirDelete checks if file_ops delete targets a path that was mkdir'd.
func matchMkdirDelete(priorArgs, cancelArgs json.RawMessage) bool {
	priorOps := extractOps(priorArgs)
	cancelOps := extractOps(cancelArgs)
	for _, po := range priorOps {
		if po.action != "mkdir" {
			continue
		}
		for _, co := range cancelOps {
			if co.action == "delete" && co.source == po.source {
				return true
			}
		}
	}
	return false
}

// matchCheckoutRoundtrip detects git_checkout A → git_checkout B → git_checkout A.
func matchCheckoutRoundtrip(priorArgs, cancelArgs json.RawMessage) bool {
	priorBranch := extractStringField(priorArgs, "branch")
	cancelBranch := extractStringField(cancelArgs, "branch")
	// Only flags a roundtrip if we're going back to a previously-checked-out branch.
	// The prior and current calls must have the same target branch.
	return priorBranch != "" && priorBranch == cancelBranch
}

// --- Helpers ---

// extractStringField extracts a single string field from JSON args.
func extractStringField(args json.RawMessage, field string) string {
	var m map[string]interface{}
	if err := json.Unmarshal(args, &m); err != nil {
		return ""
	}
	v, ok := m[field]
	if !ok {
		return ""
	}
	str, ok := v.(string)
	if !ok {
		return ""
	}
	return str
}

// extractStringSlice extracts a []string field from JSON args.
func extractStringSlice(args json.RawMessage, field string) []string {
	var m map[string]interface{}
	if err := json.Unmarshal(args, &m); err != nil {
		return nil
	}
	v, ok := m[field]
	if !ok {
		return nil
	}
	arr, ok := v.([]interface{})
	if !ok {
		return nil
	}
	result := make([]string, 0, len(arr))
	for _, item := range arr {
		if s, ok := item.(string); ok {
			result = append(result, s)
		}
	}
	return result
}

// fileOpEntry represents one operation in file_ops's "operations" array.
type fileOpEntry struct {
	action string
	source string
}

// extractOps extracts operations from file_ops args.
func extractOps(args json.RawMessage) []fileOpEntry {
	var m map[string]interface{}
	if err := json.Unmarshal(args, &m); err != nil {
		return nil
	}
	v, ok := m["operations"]
	if !ok {
		return nil
	}
	arr, ok := v.([]interface{})
	if !ok {
		return nil
	}
	result := make([]fileOpEntry, 0, len(arr))
	for _, item := range arr {
		om, ok := item.(map[string]interface{})
		if !ok {
			continue
		}
		action, _ := om["action"].(string)
		source, _ := om["source"].(string)
		if action != "" {
			result = append(result, fileOpEntry{action: action, source: source})
		}
	}
	return result
}
