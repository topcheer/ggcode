package agent

import "testing"

// #3751: per-segment env-prefix stripping in verifyCommandAvailable, and
// quoted env values in envAssignPrefix. Before the fix:
//   - `cd /app && GOFLAGS=-p=1 go test ./...` probed LookPath("GOFLAGS=-p=1")
//     -> false -> verification silently skipped while reporting Passed=true
//   - `GREETING="hello world" ./run.sh` was not stripped at all
func TestIssue3751SegmentEnvPrefixAvailable(t *testing.T) {
	if !verifyCommandAvailable("cd /app && GOFLAGS=-p=1 go test ./...") {
		t.Errorf("compound segment with env prefix should be available: cd /app && GOFLAGS=-p=1 go test ./...")
	}
	// Control: without env prefix (already worked, must keep working).
	if !verifyCommandAvailable("cd /app && go test ./...") {
		t.Errorf("compound segment without env prefix should be available")
	}
	// Whole-command leading prefix (the #2122 case) must keep working.
	if !verifyCommandAvailable(`GOFLAGS="-p=1" make verify-ci`) {
		t.Errorf("whole-command leading env prefix should be available")
	}
}

func TestIssue3751QuotedEnvValueStrip(t *testing.T) {
	// Quoted value containing a space: the assignment must be consumed whole.
	if !verifyCommandAvailable(`GREETING="hello world" make test`) {
		t.Errorf("quoted env value with space should not break availability of make")
	}
	if !verifyCommandAvailable(`ARG='foo bar' make test`) {
		t.Errorf("single-quoted env value with space should still be recognized as make")
	}
	// Unquoted single-token values keep the pre-existing behavior.
	if got := stripEnvAssignments("GOFLAGS=-p=1 go vet ./..."); got != "go vet ./..." {
		t.Errorf("plain env value strip = %q, want %q", got, "go vet ./...")
	}
	// Non-assignment leading token must not be stripped.
	if got := stripEnvAssignments("make test"); got != "make test" {
		t.Errorf("non-assignment must be unchanged, got %q", got)
	}
}

// The segment-level matcher must also see through mid-command env prefixes
// (isVerifyCommandSegment already strips; guard the interplay with the new
// quoted-value regex).
func TestIssue3751VerifySegmentWithEnvPrefix(t *testing.T) {
	if !isVerifyCommandSegment("GOFLAGS=-p=1 go test ./...") {
		t.Errorf("segment with env prefix should be a verify segment")
	}
	if !isVerifyCommandSegment(`GREETING="hello world" make test`) {
		t.Errorf("segment with quoted env value should be a verify segment")
	}
	if isVerifyCommandSegment("GOFLAGS=-p=1 echo hi") {
		t.Errorf("non-verify command with env prefix must not be a verify segment")
	}
}
