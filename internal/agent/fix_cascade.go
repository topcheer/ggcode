package agent

// Failed Fix Cascade Detection -- Wrong-Hypothesis Lock-In Prevention
//
// Research basis: Dead-end detection literature (moltq.chat 2026 industry
// discussion, AgentDiet FSE 2026) identifies "wrong-hypothesis lock-in" as a
// critical failure mode: the agent pursues a plausible but incorrect solution
// path, makes incremental edits, runs verification, fails, then interprets
// each failure as "my fix was incomplete" rather than "my hypothesis is wrong."
//
// The signature pattern is a cascade of edit->verify->fail cycles:
//   1. Agent edits a file (addressing its hypothesis)
//   2. Agent runs build/test/lint (verifies)
//   3. Verification fails (but with a DIFFERENT error than before, because the
//      code changed)
//   4. Agent edits again in the same direction
//   5. Repeat 2-4 without productive progress
//
// Existing ggcode systems do NOT detect this meta-pattern:
//   - recurring_error.go: detects the EXACT SAME error fingerprint recurring.
//     In a wrong-hypothesis lock-in, errors CHANGE with each edit (different
//     line numbers, different symbols), so the fingerprint never matches.
//   - loop_detect: catches consecutive IDENTICAL tool calls, but here each
//     cycle involves different edit content and different error output.
//   - convergence_lock: fires after successful verification (opposite scenario).
//   - self_correction_gate: detects repeated edit-fail on the SAME file, but
//     cascades often span multiple files as the agent's fix ripples outward.
//   - compounding_failure: tracks aggregate cross-tool failure rate, not the
//     edit->verify->fail cycle specifically.
//
// This detector fills the gap by tracking edit->verify->fail CYCLES regardless
// of which specific errors or files are involved. After 3 such cycles, the
// agent is very likely locked into a wrong hypothesis and should step back to
// reconsider its approach from scratch.
//
// Design: zero LLM cost, deterministic state machine. Fires at most once per
// run. Reset on new user turn.

import (
	"strconv"

	"github.com/topcheer/ggcode/internal/debug"
)

// strictVerifyCommands is the fix-cascade verification subset (#469):
// build / test / lint only. Formatters (gofmt/prettier — they fail on
// unrelated mid-refactor syntax errors) and bare task-runners (make /
// just / task — "make deploy" is not verification) are excluded.
var strictVerifyCommands = map[string]bool{
	"go build": true, "go test": true, "go vet": true,
	"cargo build": true, "cargo test": true, "cargo clippy": true,
	"npm run build": true, "npm test": true, "npm run test": true,
	"pytest": true, "flutter test": true, "ctest": true, "rake test": true,
	"golangci-lint": true, "eslint": true, "ruff": true,
}

// isStrictVerifyCommand reports whether cmd is in the strict verify set.
func isStrictVerifyCommand(cmd string) bool {
	return strictVerifyCommands[cmd]
}

// fixCascadeCheckCommand is the Agent-level wrapper that checks whether a
// tool call result represents a verify command failure, and if so, records
// it in the fix cascade state machine. Returns guidance if threshold reached.
func (a *Agent) fixCascadeCheckCommand(toolName string, args []byte, isError bool) string {
	if a.fixCascade == nil {
		return ""
	}
	// Only track run_command results that are verify commands.
	if toolName != "run_command" {
		return ""
	}
	cmd := extractCommandFromArgs(args)
	// #469: fix-cascade uses the STRICT verification set; verify_hint's
	// looser isVerifyCommand stays for its own counter-reset purpose.
	if cmd == "" || !isStrictVerifyCommand(cmd) {
		return ""
	}
	// Record the verify result. hadEdits is derived from editCount > 0.
	hadEdits := a.fixCascade.editCount > 0
	return a.fixCascade.recordVerify(hadEdits, isError)
}

const (
	// cascadeThreshold: number of edit->verify->fail cycles before guidance fires.
	// Research suggests 4-5 tool calls in the same direction rarely self-correct;
	// 3 failed verify cycles with edits between them is the actionable threshold.
	cascadeThreshold = 3
)

// fixCascadeState tracks edit->verify->fail cycles to detect wrong-hypothesis
// lock-in -- the pattern where the agent repeatedly edits code and fails
// verification, each time with different errors, because its fundamental
// approach is wrong.
type fixCascadeState struct {
	// editCount tracks file edits since the last verify command. Reset when
	// a verify command completes (success or failure).
	editCount int

	// failedVerifyCount tracks how many times a verify command has failed
	// immediately after file edits. This is the cascade depth.
	failedVerifyCount int

	// guidanceFired tracks whether cascade guidance has been injected
	// this run. Prevents repeated messages.
	guidanceFired bool
}

func newFixCascadeState() *fixCascadeState {
	return &fixCascadeState{}
}

func (f *fixCascadeState) reset() {
	f.editCount = 0
	f.failedVerifyCount = 0
	f.guidanceFired = false
}

// recordEdit increments the edit counter. Called after a successful file edit.
func (f *fixCascadeState) recordEdit() {
	f.editCount++
}

// recordVerify processes a verification command result and returns guidance
// if the cascade threshold has been reached.
//
// Parameters:
//   - hadEdits: whether file edits occurred since the last verify
//   - failed: whether this verification failed (IsError or exit code != 0)
//
// Returns guidance string if cascade threshold reached, empty string otherwise.
func (f *fixCascadeState) recordVerify(hadEdits bool, failed bool) string {
	if !failed {
		// Successful verification resets the cascade counter -- the agent
		// made progress. This mirrors convergence_lock logic.
		f.failedVerifyCount = 0
		f.editCount = 0
		return ""
	}

	if hadEdits {
		// An edit->verify->fail cycle: the agent changed code, tried to
		// verify, and verification failed. This is a meaningful cascade step.
		f.failedVerifyCount++
	}
	// Reset edit counter regardless -- the verify marks the end of a cycle.
	f.editCount = 0

	if f.failedVerifyCount >= cascadeThreshold && !f.guidanceFired {
		f.guidanceFired = true
		debug.Log("fix-cascade", "detected %d edit-verify-fail cycles -- likely wrong-hypothesis lock-in", f.failedVerifyCount)
		return cascadeGuidance(f.failedVerifyCount)
	}

	return ""
}

// cascadeGuidance generates the guidance message injected when the cascade
// threshold is reached.
func cascadeGuidance(failedCount int) string {
	return "[HYPOTHESIS LOCK-IN WARNING] " +
		"You have completed " + strconv.Itoa(failedCount) + " edit->build->fail cycles, " +
		"each ending in verification failure. This pattern strongly suggests your underlying " +
		"hypothesis is WRONG -- incremental fixes will continue to produce new errors " +
		"without solving the problem.\n\n" +
		"STOP making incremental edits. Instead:\n" +
		"1. Re-read the ORIGINAL task requirements -- are you solving the right problem?\n" +
		"2. Re-examine the ROOT CAUSE, not the latest error symptom. " +
		"Trace the issue from first principles.\n" +
		"3. Consider that your approach may need a fundamentally different direction. " +
		"Discard the current hypothesis and explore alternatives.\n" +
		"4. If stuck after reconsidering, clearly explain the problem and ask for guidance " +
		"rather than continuing to make changes that don't work."
}
