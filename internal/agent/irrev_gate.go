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
	"encoding/json"
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
	grounding     []bool // recent TOOL CALLS (not iterations - #1468-A note): one bool per action
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
	case "file_ops", "batch_replace", "lsp_rename":
		// lsp_rename applies LSP workspace edits across files (no dispatch-layer
		// checkpoint, see agent_tool.go) -- same tier as file_ops/batch_replace.
		return irrevTierMedium
	case "start_command", "run_command":
		// Commands are at least medium — could be anything
		if irrevIsDestructiveCommand(args) {
			return irrevTierHigh
		}
		return irrevTierMedium

	// Tier 3: High irreversibility (very hard to undo)
	case "git_push":
		return irrevTierHigh
	case "git_reset":
		// #1468-C: soft/mixed/unstage resets are reversible - only hard
		// is a high-tier irreversible action. #1579-A: the git_reset TOOL
		// carries the mode as a schema field ({"mode":"hard"}) - it never
		// contains the '--hard' literal, so the substring test tiered every
		// real hard reset Low (zero grounding, all uncommitted work dropped
		// with no warning). Accept both shapes: the tool's mode field and a
		// shell literal via run_command.
		var resetArgs struct {
			Mode string `json:"mode"`
		}
		modeHard := false
		if err := json.Unmarshal([]byte(args), &resetArgs); err == nil && strings.EqualFold(strings.TrimSpace(resetArgs.Mode), "hard") {
			modeHard = true
		}
		if modeHard || strings.Contains(strings.ToLower(args), "--hard") {
			return irrevTierHigh
		}
		return irrevTierLow
	default:
		// #1468-B: the destructive PATTERN TABLE was designed for COMMANDS
		// (rm -rf, mkfs) but ran over the ENTIRE argument JSON of unknown
		// tools - an mcp search whose query merely CONTAINS 'shutdown' or
		// 'truncate ' got tiered HIGH-IMPACT. Unknown tools are treated as
		// read-only; pattern matching applies only when the args carry a
		// command field.
		if extractCommandFromArgs(json.RawMessage(args)) != "" && irrevIsDestructiveCommand(args) {
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
		"git push origin --delete", "git branch -d",
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
	// #1468-A: a SUCCESSFUL run_command is the strongest grounding there
	// is - verification commands (go test / builds) right before a commit
	// are exactly the evidence the gate wants; the standard
	// test-driven-commit flow used to score '0 grounding actions'.
	case "run_command":
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
