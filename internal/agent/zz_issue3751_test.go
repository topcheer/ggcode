package agent

import "testing"

func TestIssue3751SegmentEnvPrefixAvailable(t *testing.T) {
	if !verifyCommandAvailable("cd /app && GOFLAGS=-p=1 go test ./...") {
		t.Errorf("compound segment with env prefix should be available: cd /app && GOFLAGS=-p=1 go test ./...")
	}

	if !verifyCommandAvailable("cd /app && go test ./...") {
		t.Errorf("compound segment without env prefix should be available")
	}

	if !verifyCommandAvailable(`GOFLAGS="-p=1" make verify-ci`) {
		t.Errorf("whole-command leading env prefix should be available")
	}
}

func TestIssue3751QuotedEnvValueStrip(t *testing.T) {

	if !verifyCommandAvailable(`GREETING="hello world" make test`) {
		t.Errorf("quoted env value with space should not break availability of make")
	}
	if !verifyCommandAvailable(`ARG='foo bar' make test`) {
		t.Errorf("single-quoted env value with space should still be recognized as make")
	}

	if got := stripEnvAssignments("GOFLAGS=-p=1 go vet ./..."); got != "go vet ./..." {
		t.Errorf("plain env value strip = %q, want %q", got, "go vet ./...")
	}

	if got := stripEnvAssignments("make test"); got != "make test" {
		t.Errorf("non-assignment must be unchanged, got %q", got)
	}
}

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
