package agent

import "fmt"

// Final-turn evidence gate (r357; "done vs verified", 2026 pattern: a May
// 2026 analysis of 20,574 real coding-agent sessions named "premature
// completion claim" a distinct failure mode - the agent reports the
// requested condition satisfied despite no supporting artifact). Unlike
// the lexical unverified-claim detector (claimsSupervision family, default
// off), this gate is behavior-triggered: it fires only when the agent is
// about to stop after editing source code WITHOUT having executed any
// build/test/verify command in this run. That conjunction has a much
// smaller false-positive surface, so the gate is on by default.
//
// The gate blocks the stop ONCE per run (injects the concrete verify
// command and continues the loop); if the agent still stops without
// evidence, it is allowed to - a hard loop would be worse than the lie.

// finalTurnEvidenceGate decides whether the agent's stop should be gated.
// Pure function over the run state; returns the message to inject (empty
// string = allow the stop).
func finalTurnEvidenceGate(editsThisRun int, lastSourceFile string, realBuildOrTestRun, gateAlreadyFired bool, workingDir string) string {
	if gateAlreadyFired || editsThisRun == 0 || realBuildOrTestRun {
		return ""
	}
	fallback := detectBuildSystem(workingDir)
	if fallback == "" {
		// No build system: there is no canonical receipt to demand.
		return ""
	}
	cmd := targetedVerifyCommand(workingDir, lastSourceFile)
	if cmd == "" {
		// #3796-B: targetedVerifyCommand returns "" for source extensions
		// with no language profile (.sh/.php/.cs/... vs the 8-profile map)
		// and for Go files outside any go.mod - silently letting those edits
		// skip the gate. Fall back to the build-system command we already
		// detected instead of waving the stop through.
		cmd = fallback
	}
	return fmt.Sprintf(
		"[Evidence Gate] You edited %d source file(s) this run but have not executed any "+
			"build/test/verify command. Do not claim the work is done - run `%s` first and "+
			"report the actual result. If it fails, fix it. (This gate fires once per run; "+
			"stopping again without evidence will be allowed but the claim is unsupported.)",
		editsThisRun, cmd)
}
