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
	// #1776 case 3: the grounding append happens BEFORE execution (the
	// gate must fire pre-action), so the outcome is unknown there. This
	// remembers the last recorded action so a FAILED verification can be
	// retroactively un-grounded.
	lastGroundingTool string
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
		"git_branch_list", "git_remote", "git_stash_list",
		"web_search", "web_fetch", "code_execution", "runtime", "clipboard":
		return irrevTierNone

	// #1621-B: git_tag carries a destructive "delete" action (the shell
	// pattern table in this same file already treats `git tag -d` as a
	// destructive pattern - the two paths contradicted each other).
	// Tier-0 tools short-circuit before grounding tracking entirely, so a
	// deleted (possibly pushed) release tag vanished with zero signal.
	case "git_tag":
		var a struct {
			Action string `json:"action"`
		}
		if json.Unmarshal([]byte(args), &a) == nil && a.Action == "delete" {
			return irrevTierMedium
		}
		return irrevTierNone

	// Tier 1: Low irreversibility (easily undone)
	case "edit_file", "write_file", "multi_edit_file", "multi_file_edit",
		"multi_file_write", "notebook_edit":
		return irrevTierLow
	case "undo_edit":
		return irrevTierLow
	// #1621-A: stash drop permanently discards uncommitted work - the
	// tool's own schema says "destructive" and recovery means fsck-ing
	// dangling commits, NOT "easily undone". The shell pattern table
	// covers `git branch -d`/`git tag -d` but never stash drop, and the
	// tool path short-circuited on the tool NAME - the #1579-A class
	// (destructive sub-action encoded in a schema field, static
	// grouping by tool name never consumes it).
	case "git_stash":
		var a struct {
			Action string `json:"action"`
		}
		if json.Unmarshal([]byte(args), &a) == nil && a.Action == "drop" {
			return irrevTierHigh
		}
		return irrevTierLow
	case "git_add", "git_checkout":
		return irrevTierLow

	// Tier 2: Medium irreversibility (harder to undo)
	case "git_commit", "git_revert":
		return irrevTierMedium
	case "file_ops", "batch_replace", "lsp_rename":
		// lsp_rename applies LSP workspace edits across files (no dispatch-layer
		// checkpoint, see agent_tool.go) -- same tier as file_ops/batch_replace.
		// #1798 case 3: file_ops delete+recursive removes a whole directory -
		// semantically equal to shell `rm -rf` (High) but used to sit a tier
		// lower with a weaker threshold; the most dangerous file_ops shape
		// had the weakest gate.
		if toolName == "file_ops" {
			var a struct {
				Action    string `json:"action"`
				Recursive bool   `json:"recursive"`
			}
			if json.Unmarshal([]byte(args), &a) == nil && a.Action == "delete" && a.Recursive {
				return irrevTierHigh
			}
		}
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
			Mode  string   `json:"mode"`
			Files []string `json:"files"`
		}
		modeHard := false
		if err := json.Unmarshal([]byte(args), &resetArgs); err == nil && strings.EqualFold(strings.TrimSpace(resetArgs.Mode), "hard") {
			modeHard = true
		}
		// #1621 side note: when files are specified the tool ignores mode
		// entirely and always performs a per-file mixed unstage (its schema
		// says so) - a leftover "mode":"hard" alongside files is NOT a
		// hard reset and must not fire the HIGH-IMPACT advisory.
		if len(resetArgs.Files) > 0 {
			return irrevTierLow
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
	// #1579-C: the run_command schema mandates a leading '# ' comment
	// line - matching over the whole payload lets a pattern mentioned in
	// a MERE COMMENT (e.g. "# drop table leftovers") tier the call High.
	// Match only the command field, comment stripped - mirroring
	// verify_hint's stripLeadingShellComment usage.
	var payload struct {
		Command string `json:"command"`
	}
	cmdStr := args
	if err := json.Unmarshal([]byte(args), &payload); err == nil && payload.Command != "" {
		cmdStr = payload.Command
	}
	lower := strings.ToLower(stripLeadingShellComment(cmdStr))
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
		// #1798 case 4: #1621 claimed "BOTH paths missed stash drop" but
		// only fixed the tool path - the shell table never gained it.
		"git stash drop", "git stash clear",
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

// recordOutcome retroactively corrects the grounding ledger after the
// tool result lands (#1776 case 3): a FAILED run_command (go test error,
// build error) is NOT grounding - counting it let a string of failures
// satisfy the threshold and then push --force sailed through ungated,
// exactly the scenario the gate exists to catch.
func (s *irrevGateState) recordOutcome(toolName string, isError bool) {
	if !isError || len(s.grounding) == 0 {
		return
	}
	if !s.grounding[len(s.grounding)-1] || s.lastGroundingTool != toolName {
		return
	}
	s.grounding[len(s.grounding)-1] = false
	if s.totalGrounded > 0 {
		s.totalGrounded--
	}
	s.lastGroundingTool = ""
}

// recordAction records a tool action and returns a warning string if
// the action is high-irreversibility with insufficient grounding.
func (s *irrevGateState) recordAction(toolName, args string) string {
	tier := irrevClassifyTool(toolName, args)
	isGrounding := irrevIsGroundingAction(toolName)

	// #1798 case 2: snapshot the window BEFORE appending the current call -
	// the current call used to count itself as its own grounding evidence
	// (run_command's Medium warning was mathematically unreachable, and two
	// consecutive destructive commands served as each other's proof).
	// Grounding still only counts the ledger, never success/failure of the
	// current in-flight call (recordOutcome handles that retroactively).
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

	// Track grounding history
	if len(s.grounding) >= irrevMaxHistory {
		s.grounding = s.grounding[1:]
	}
	s.grounding = append(s.grounding, isGrounding)
	if isGrounding {
		s.totalGrounded++
		s.lastGroundingTool = toolName
	}

	// Only gate medium+ irreversibility actions
	if tier < irrevTierMedium {
		return ""
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
