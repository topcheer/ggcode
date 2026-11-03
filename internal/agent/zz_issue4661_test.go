package agent

import "testing"

func TestBackgroundConsumeGateMatchesForegroundGate(t *testing.T) {

	differenceSet := []string{"cargo check", "mvn verify", "./mvnw verify", "tsc --noEmit", "pnpm run build", "gradle build"}
	for _, cmd := range differenceSet {
		if !isVerificationCommand(cmd) {
			t.Errorf("isVerificationCommand(%q) = false; foreground gate must accept it (check the predicate didn't narrow)", cmd)
		}
		if isVerifyCommand(cmd) {
			t.Errorf("isVerifyCommand(%q) = true; it is supposed to be the NARROW predicate - if this changed, the difference-set premise is stale", cmd)
		}
	}

	for _, cmd := range []string{"go test ./...", "cargo test", "npm test"} {
		if !isVerificationCommand(cmd) || !isVerifyCommand(cmd) {
			t.Errorf("shared-core command %q must pass both predicates (got verif=%v verify=%v)", cmd, isVerificationCommand(cmd), isVerifyCommand(cmd))
		}
	}

	for _, cmd := range []string{"ls -la", "cat README.md", "echo hi"} {
		if isVerificationCommand(cmd) {
			t.Errorf("isVerificationCommand(%q) = true; non-verify command must stay excluded", cmd)
		}
	}
}
