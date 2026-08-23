package agent

import "testing"

// TestIsVerifyCommandEnvPrefixAndCompound (#950): env-var assignment
// prefixes and compound command forms must be recognized so that a
// successful verify run clears the hint state (no stale "which FAILED"
// injection after the agent already re-verified with GOFLAGS=...).
func TestIsVerifyCommandEnvPrefixAndCompound(t *testing.T) {
	positive := []string{
		// env prefixes (issue repro forms)
		`GOFLAGS="-p=1" make verify-ci`,
		`GOFLAGS=-p=1 make verify-ci`,
		`CGO_ENABLED=0 go build ./...`,
		`GOMEMLIMIT=2GiB GOGC=50 go test ./...`, // multiple prefixes
		// compound commands
		`cd /app && go test ./...`,
		`git pull && make test`,
		`echo hi; go vet ./...`,
		`grep foo bar || make test`, // any matching segment counts
		// baseline forms still recognized
		`make verify-ci`,
		`go test ./...`,
		`task build`,
	}
	for _, cmd := range positive {
		if !isVerifyCommand(cmd) {
			t.Errorf("isVerifyCommand(%q) = false, want true", cmd)
		}
	}
	negative := []string{
		`ls -la`,
		`cat README.md`,
		`echo hello world`,
		`cd /app && ls`,
		``,    // empty
		`   `, // whitespace only
	}
	for _, cmd := range negative {
		if isVerifyCommand(cmd) {
			t.Errorf("isVerifyCommand(%q) = true, want false", cmd)
		}
	}
}

// TestStripEnvAssignments unit-checks the prefix stripper directly.
func TestStripEnvAssignments(t *testing.T) {
	cases := []struct{ in, want string }{
		{`GOFLAGS="-p=1" make verify-ci`, `make verify-ci`},
		{`CGO_ENABLED=0 go build ./...`, `go build ./...`},
		{`A=1 B=2 C=3 go test`, `go test`},
		{`make test`, `make test`}, // no prefix
		{`echo a=b`, `echo a=b`},   // assignment not at start
	}
	for _, c := range cases {
		if got := stripEnvAssignments(c.in); got != c.want {
			t.Errorf("stripEnvAssignments(%q) = %q, want %q", c.in, got, c.want)
		}
	}
}
