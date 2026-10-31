package agent

import "testing"

// TestBackgroundConsumeGateMatchesForegroundGate (#4661): the three gates
// around bgVerifyJobs must be predicate-aligned. Registration (agent.go,
// start_command success) accepts the FULL command set, so the terminal-state
// consumption gate must use the same predicate as the foreground clearing
// gate (isVerificationCommand) - not the narrower isVerifyCommand. The
// difference set (cargo check, mvn verify, tsc --noEmit, gradle build...)
// used to register but then short-circuit at consumption, so terminal-green
// background runs never cleared verifyDebt while the same command run in the
// foreground did (#3685-B pattern recurrence).
func TestBackgroundConsumeGateMatchesForegroundGate(t *testing.T) {
	// The exact difference set from the issue, verified live in-repo:
	// isVerificationCommand=true, isVerifyCommand=false for each.
	differenceSet := []string{"cargo check", "mvn verify", "./mvnw verify", "tsc --noEmit", "pnpm run build", "gradle build"}
	for _, cmd := range differenceSet {
		if !isVerificationCommand(cmd) {
			t.Errorf("isVerificationCommand(%q) = false; foreground gate must accept it (check the predicate didn't narrow)", cmd)
		}
		if isVerifyCommand(cmd) {
			t.Errorf("isVerifyCommand(%q) = true; it is supposed to be the NARROW predicate - if this changed, the difference-set premise is stale", cmd)
		}
	}
	// The shared core (both predicates true) must keep clearing either way.
	for _, cmd := range []string{"go test ./...", "cargo test", "npm test"} {
		if !isVerificationCommand(cmd) || !isVerifyCommand(cmd) {
			t.Errorf("shared-core command %q must pass both predicates (got verif=%v verify=%v)", cmd, isVerificationCommand(cmd), isVerifyCommand(cmd))
		}
	}
	// Non-verification commands must stay excluded from both gates.
	for _, cmd := range []string{"ls -la", "cat README.md", "echo hi"} {
		if isVerificationCommand(cmd) {
			t.Errorf("isVerificationCommand(%q) = true; non-verify command must stay excluded", cmd)
		}
	}
}
