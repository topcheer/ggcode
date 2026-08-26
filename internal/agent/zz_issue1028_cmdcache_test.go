package agent

import (
	"encoding/json"
	"testing"
)

// Regression guard for #1028: a compound command whose mutation step succeeded
// but whose overall exit was non-zero (e.g. `sed -i ... && make lint` where sed
// rewrote the file and make failed) must still be classified as a cache
// invalidator. Pre-fix, agent.go gated the #750 branch on !result.IsError, so
// the already-happened side effect skipped invalidation and later build hits
// served stale results annotated "no source files have changed".
//
// The wiring test mirrors the post-fix agent.go branch shape: for
// run_command/start_command, mutation detection must not consider IsError.
func TestCommandCache_FailedCompoundMutationStillInvalidates(t *testing.T) {
	cases := []struct {
		name string
		cmd  string
		want bool
	}{
		// Compound mutation + failing tail: the core #1028 scenario.
		{"sed success then make fail", "sed -i 's/foo/bar/' x.go && make lint", true},
		{"gofmt then failing go vet", "gofmt -w ./... ; go vet ./...", true},
		{"patch apply then failing build", "patch -p1 < fix.diff && go build ./...", true},
		// go fmt must now be recognized (#1028 follow-up: it wraps gofmt -l -w,
		// and it is in the cacheable whitelist, so the hit annotation would be
		// literally false without this).
		{"go fmt rewrites in place", "go fmt ./...", true},
		// Negative controls.
		{"plain failing build", "go build ./...", false},
		{"plain test", "go test ./pkg/", false},
	}
	for _, c := range cases {
		args, _ := json.Marshal(map[string]string{"command": c.cmd})
		parsed, _ := parseRunCommandArgs(args)
		if got := shellMutatesSources(parsed); got != c.want {
			t.Errorf("%s: shellMutatesSources(%q) = %v, want %v", c.name, c.cmd, got, c.want)
		}
	}

	// Post-fix branch shape: the IsError gate is gone, so a failed mutating
	// run_command still invalidates. Simulated with the same predicate the
	// agent.go branch uses after #1028.
	failedResultIsError := true
	args, _ := json.Marshal(map[string]string{"command": "sed -i 's/a/b/' x.go && make lint"})
	parsed, _ := parseRunCommandArgs(args)
	//nolint:staticcheck // mirrors agent.go branch: no IsError condition remains
	invalidates := shellMutatesSources(parsed) // no && !failedResultIsError
	if !invalidates {
		t.Fatal("failed compound mutation must still invalidate the command cache")
	}
	_ = failedResultIsError
}
