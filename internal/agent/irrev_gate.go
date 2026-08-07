package agent

// irrev_gate.go -- Irreversibility-Weighted Calibration Gate
//
// Research basis:
//   - Calibrated Abstention and Emotionally Legible Uncertainty
//     Contracts (CA-EUC), Curve Labs, March 2026: agents should
//     "estimate uncertainty, abstain or defer when risk exceeds
//     calibrated thresholds." A core dimension is the irreversibility
//     score: how hard it would be to undo an action.
//   - Abstain-R1 (arXiv:2604.17073, 2026): calibrated abstention
//     via post-refusal clarification for autonomous agents.
//   - OpenAI Alignment (Jan 2026): "if confidence is low and
//     sentiment is degrading, defaulting to abstain/defer is often
//     preferable to speculative completion."
//
// Problem: AI coding agents treat all actions with the same caution
// level. A `read_file` and a `git push --force` trigger the same
// pre-action checks. The CA-EUC insight is that caution must SCALE
// with irreversibility: the harder an action is to undo, the more
// grounding (exploration, verification) the agent should demonstrate
// beforehand. Without this, agents:
//
//  1. Execute destructive git operations (push --force, reset --hard)
//     without having verified the state of the repository
//  2. Delete files or run bulk operations without confirming scope
//  3. Run deployment/CI operations without checking build status
//  4. Make irreversible changes when they lack the information to
//     know they're correct
//
// Design:
//   - Classifies each tool call into an irreversibility tier
//     (0=none, 1=low, 2=medium, 3=high)
//   - Tracks the agent's "grounding depth" — how much exploration
//     has been done in recent iterations
//   - For medium/high tier actions with insufficient grounding,
//     injects an abstention advisory
//   - Non-blocking, max 3 warnings per run, zero LLM cost
//   - Complements reckless_exec.go (which checks binary read-before-
//     edit in early iterations) by adding irreversibility weighting
//     across ALL iterations

import (
	"strconv"
	"strings"
)

const (
	irrevTierNone   = 0 // read-only, no side effects
	irrevTierLow    = 1 // normal edits, easily undone (undo_edit, git stash)
	irrevTierMedium = 2 // significant changes, harder to undo (git commit, bulk replace)
	irrevTierHigh   = 3 // destructive, very hard to undo (force push, hard reset, rm)

	irrevMaxWarnings     = 3
	irrevGroundingWindow = 5 // iterations to look back for grounding actions
	irrevMaxHistory      = 40
)

// irrevGateState tracks grounding actions and warns on under-grounded
// high-irreversibility actions.
type irrevGateState struct {
	grounding     []bool // recent iterations: true if grounding action taken
	warnings      int
	totalGrounded int
}

func newIrrevGateState() *irrevGateState {
	return &irrevGateState{
		grounding: make([]bool, 0, irrevMaxHistory),
	}
}

func (s *irrevGateState) reset() {
	s.grounding = s.grounding[:0]
	s.warnings = 0
	s.totalGrounded = 0
}

// irrevClassifyTool returns the irreversibility tier of a tool call
// based on tool name and arguments.
func irrevClassifyTool(toolName, args string) int {
	switch toolName {
	// Tier 0: Read-only / no side effects
	case "read_file", "multi_file_read", "search_files", "grep", "glob",
		"list_directory", "code_search", "lsp_definition", "lsp_references",
		"lsp_symbols", "lsp_hover", "lsp_workspace_symbols", "lsp_implementation",
		"lsp_incoming_calls", "lsp_outgoing_calls", "lsp_prepare_call_hierarchy",
		"lsp_diagnostics", "lsp_document_highlights", "lsp_code_actions",
		"git_show", "git_diff", "git_blame", "git_log", "git_status",
		"git_branch_list", "git_remote", "git_stash_list", "git_tag",
		"web_search", "web_fetch", "code_execution", "runtime", "clipboard":
		return irrevTierNone

	// Tier 1: Low irreversibility (easily undone)
	case "edit_file", "write_file", "multi_edit_file", "multi_file_edit",
		"multi_file_write", "notebook_edit":
		return irrevTierLow
	case "undo_edit", "git_stash":
		return irrevTierLow
	case "git_add", "git_checkout":
		return irrevTierLow

	// Tier 2: Medium irreversibility (harder to undo)
	case "git_commit", "git_revert":
		return irrevTierMedium
	case "file_ops", "batch_replace":
		return irrevTierMedium
	case "start_command", "run_command":
		// Commands are at least medium — could be anything
		if irrevIsDestructiveCommand(args) {
			return irrevTierHigh
		}
		return irrevTierMedium

	// Tier 3: High irreversibility (very hard to undo)
	case "git_push", "git_reset":
		return irrevTierHigh
	default:
		// Check for destructive patterns in unknown tools
		if irrevIsDestructiveCommand(args) {
			return irrevTierHigh
		}
		return irrevTierLow
	}
}

// irrevIsDestructiveCommand checks argument text for destructive patterns.
func irrevIsDestructiveCommand(args string) bool {
	lower := strings.ToLower(args)
	patterns := []string{
		"push --force", "push -f", "push --force-with-lease",
		"reset --hard", "checkout -- .", "clean -fd", "clean -f",
		"rm -rf", "rm -r -f", "rmdir /s",
		"drop table", "drop database", "truncate ",
		"dd if=", "mkfs", "fdisk",
		":(){:|:&};:", // fork bomb
		"chmod -r 777", "chown -r",
		"git push origin --delete", "git branch -d", "git branch -d",
		"git tag -d", "git remote remove",
		"sudo rm", "shutdown", "reboot", "halt",
	}
	for _, p := range patterns {
		if strings.Contains(lower, p) {
			return true
		}
	}
	return false
}

// irrevIsGroundingAction returns true for tools that demonstrate
// the agent is building understanding before acting.
func irrevIsGroundingAction(toolName string) bool {
	switch toolName {
	case "read_file", "multi_file_read", "search_files", "grep", "glob",
		"list_directory", "code_search", "lsp_definition", "lsp_references",
		"lsp_symbols", "lsp_hover", "lsp_workspace_symbols",
		"lsp_diagnostics", "lsp_implementation", "lsp_incoming_calls",
		"lsp_outgoing_calls", "git_show", "git_diff", "git_blame",
		"git_log", "git_status", "review_changes", "code_health",
		"scan_todos", "dep_graph":
		return true
	}
	return false
}

// recordAction records a tool action and returns a warning string if
// the action is high-irreversibility with insufficient grounding.
func (s *irrevGateState) recordAction(toolName, args string) string {
	tier := irrevClassifyTool(toolName, args)
	isGrounding := irrevIsGroundingAction(toolName)

	// Track grounding history
	if len(s.grounding) >= irrevMaxHistory {
		s.grounding = s.grounding[1:]
	}
	s.grounding = append(s.grounding, isGrounding)
	if isGrounding {
		s.totalGrounded++
	}

	// Only gate medium+ irreversibility actions
	if tier < irrevTierMedium {
		return ""
	}

	// Check grounding depth in recent window
	recentGrounding := 0
	start := len(s.grounding) - irrevGroundingWindow
	if start < 0 {
		start = 0
	}
	for i := start; i < len(s.grounding); i++ {
		if s.grounding[i] {
			recentGrounding++
		}
	}

	// Tier-based thresholds: higher irreversibility needs more grounding
	var threshold int
	switch tier {
	case irrevTierMedium:
		threshold = 1 // at least 1 grounding action recently
	case irrevTierHigh:
		threshold = 2 // at least 2 grounding actions recently
	default:
		threshold = 0
	}

	if recentGrounding >= threshold {
		return ""
	}

	if s.warnings >= irrevMaxWarnings {
		return ""
	}

	s.warnings++

	tierLabel := "medium-impact"
	if tier >= irrevTierHigh {
		tierLabel = "HIGH-IMPACT (hard to reverse)"
	}

	return "[irreversibility-gate] You are about to execute a " + tierLabel +
		" action (" + toolName + ") with only " + strconv.Itoa(recentGrounding) +
		" grounding action(s) in the last " + strconv.Itoa(irrevGroundingWindow) +
		" iterations. Calibrated abstention principle: the more " +
		"irreversible the action, the more certain you should be. " +
		"VERIFY: check repository state (git status/diff), confirm " +
		"the operation is correct and necessary, and ensure you " +
		"have sufficient information before proceeding with this " +
		"difficult-to-reverse action."
}
