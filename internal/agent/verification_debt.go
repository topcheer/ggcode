package agent

// Verification Debt Tracker -- SAUP-inspired uncertainty propagation detection
//
// Research basis:
//   - SAUP (Situation Awareness Uncertainty Propagation), ACL 2025
//     Zhao et al. -- uncertainty compounds across agent steps; situational
//     weighting determines which steps contribute most to accumulated drift.
//   - Sherlock (Microsoft Research, Nov 2025) -- selective verification at
//     high-propagation-risk nodes prevents error cascades.
//   - MAST taxonomy (Cemri et al., 2025) -- "propagation failures" where
//     downstream steps trust upstream errors are the dominant failure mode.
//
// GAP in ggcode: existing systems track aggregate distribution (thermal
// profile) or success rate (confidence scorer), but NEITHER tracks the
// TEMPORAL recency of verification relative to modifications. The dangerous
// pattern: agent makes a series of edits/writes without ever re-reading
// changed files, running build/test, or checking diagnostics. Each edit
// compounds uncertainty on potentially stale premises.
//
// This tracker counts "verification debt" -- unverified modifications since
// the last grounding action. When debt exceeds a threshold, the agent is
// operating on accumulated assumptions rather than verified reality.
//
// Classification:
//   GROUNDING:   read_file, multi_file_read, search, grep, glob, lsp_*
//   MODIFYING:   edit_file, write_file, multi_edit_file, multi_file_edit
//   VERIFYING:   run_command (build/test/vet/lint), git_diff, lsp_diagnostics
//
// The tracker fires when modifications-without-verification exceed a threshold,
// indicating the agent should pause and verify before continuing to stack
// changes on unverified premises.
//
// Zero LLM cost -- pure counter-based, O(1) per call.

import (
	"fmt"
	"strings"
)

const (
	// verificationDebtThreshold: number of modifications without any grounding
	// or verification action before warning. At 5+ unverified edits, the
	// probability that earlier edits had side effects the agent hasn't seen
	// grows significantly.
	verificationDebtThreshold = 5

	// verificationDebtMinTotal: minimum total tool calls before evaluation
	// to avoid false positives early in the trajectory.
	verificationDebtMinTotal = 6

	// verificationDebtMaxWarn: max warnings per run (advisory, not blocking).
	verificationDebtMaxWarn = 2
)

// debtAction classifies a tool call's relationship to verification.
type debtAction int

const (
	debtGrounding debtAction = iota // information-gathering (reduces uncertainty)
	debtModifying                   // changes files (increases uncertainty)
	debtVerifying                   // explicit verification (resets debt)
	debtNeutral                     // neither here nor there (git status, etc.)
)

// verificationDebtTools maps tool names to debt actions.
var verificationDebtTools = map[string]debtAction{
	// Grounding: information gathering
	"read_file":               debtGrounding,
	"multi_file_read":         debtGrounding,
	"search_files":            debtGrounding,
	"grep":                    debtGrounding,
	"glob":                    debtGrounding,
	"code_search":             debtGrounding,
	"list_directory":          debtGrounding,
	"lsp_hover":               debtGrounding,
	"lsp_definition":          debtGrounding,
	"lsp_references":          debtGrounding,
	"lsp_symbols":             debtGrounding,
	"lsp_workspace_symbols":   debtGrounding,
	"lsp_implementation":      debtGrounding,
	"lsp_document_highlights": debtGrounding,
	"web_search":              debtGrounding,
	"web_fetch":               debtGrounding,

	// Modifying: changes files
	"edit_file":       debtModifying,
	"write_file":      debtModifying,
	"multi_edit_file": debtModifying,
	"multi_file_edit": debtModifying,
	"file_ops":        debtModifying,
	"notebook_edit":   debtModifying,
	"batch_replace":   debtModifying,

	// Verifying: explicit validation
	"run_command":      debtVerifying,
	"lsp_diagnostics":  debtVerifying,
	"lsp_code_actions": debtVerifying,
}

// classifyDebtAction determines the debt action for a tool call.
// For run_command, we check the command content to distinguish
// verification (build/test/vet) from other commands.
func classifyDebtAction(toolName, args string) debtAction {
	if action, ok := verificationDebtTools[toolName]; ok {
		if action == debtVerifying && toolName == "run_command" {
			// Only count as verification if it's actually a build/test/lint command
			if !isVerificationCommand(args) {
				return debtNeutral
			}
		}
		return action
	}
	return debtNeutral
}

// isVerificationCommand checks if a run_command argument string represents
// a verification action (build, test, vet, lint, typecheck).
// Uses token-based matching for single-word patterns to avoid false matches
// like "check" matching "checkout" or "test" matching "testfile".
func isVerificationCommand(args string) bool {
	lower := strings.ToLower(args)
	tokens := strings.Fields(lower)
	singleWordMarkers := []string{"build", "test", "vet", "lint", "typecheck", "tsc", "check", "compile", "pytest", "jest", "mypy", "flake8", "eslint", "prettier", "gradle"}
	multiWordMarkers := []string{"go test", "go build", "go vet", "cargo build", "cargo test", "npm run", "dotnet test", "dotnet build"}
	prefixMarkers := []string{"make ", "cargo ", "yarn ", "pnpm ", "mvn ", "sbt "}

	// Check single-word markers as complete tokens
	for _, t := range tokens {
		for _, m := range singleWordMarkers {
			if t == m {
				return true
			}
		}
	}

	// Check multi-word markers as substrings (specific enough)
	for _, m := range multiWordMarkers {
		if strings.Contains(lower, m) {
			return true
		}
	}

	// Check prefix markers (make/cargo/yarn/etc followed by any subcommand)
	for _, m := range prefixMarkers {
		if strings.HasPrefix(lower, strings.TrimSpace(m)) { // changed to trimmed prefix
			return true
		}
	}

	return false
}

// verificationDebtState tracks modification-vs-verification temporal pattern.
type verificationDebtState struct {
	totalCalls     int
	modifyCount    int
	groundCount    int
	verifyCount    int
	debt           int // current unverified modifications
	maxDebt        int // peak debt this run
	warningsIssued int
	lastAction     debtAction
}

func newVerificationDebtState() *verificationDebtState {
	return &verificationDebtState{}
}

func (v *verificationDebtState) reset() {
	v.totalCalls = 0
	v.modifyCount = 0
	v.groundCount = 0
	v.verifyCount = 0
	v.debt = 0
	v.maxDebt = 0
	v.warningsIssued = 0
	v.lastAction = debtNeutral
}

// recordToolCall updates the debt state based on the tool call.
func (v *verificationDebtState) recordToolCall(toolName, args string) {
	action := classifyDebtAction(toolName, args)
	v.totalCalls++
	v.lastAction = action

	switch action {
	case debtModifying:
		v.modifyCount++
		v.debt++
		if v.debt > v.maxDebt {
			v.maxDebt = v.debt
		}
	case debtGrounding:
		v.groundCount++
		// Grounding partially reduces debt -- the agent checked something
		// before continuing. Reduce by 1 (not reset) because reading isn't
		// the same as verifying your changes compile.
		if v.debt > 0 {
			v.debt--
		}
	case debtVerifying:
		v.verifyCount++
		// Verification resets debt entirely -- build/test results are the
		// ground truth that validates prior modifications.
		v.debt = 0
	case debtNeutral:
		// No change to debt
	}
}

// maybeWarn checks if the current verification debt warrants intervention.
// Returns a guidance message if the agent should pause and verify.
func (v *verificationDebtState) maybeWarn() string {
	if v.warningsIssued >= verificationDebtMaxWarn {
		return ""
	}
	if v.totalCalls < verificationDebtMinTotal {
		return ""
	}
	if v.debt < verificationDebtThreshold {
		return ""
	}

	v.warningsIssued++

	return fmt.Sprintf(
		"[Verification Debt] You have made %d file modifications since the last "+
			"build/test/diagnostic check. Each unverified edit compounds the risk "+
			"of building on stale or incorrect premises (SAUP: uncertainty propagation, "+
			"ACL 2025). Before making more changes, run a build or test to verify your "+
			"edits are correct so far. This prevents error cascades where a single wrong "+
			"assumption propagates through all subsequent edits.",
		v.debt,
	)
}
