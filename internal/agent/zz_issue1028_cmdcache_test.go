package agent

import (
	"encoding/json"
	"testing"
)

func TestCommandCache_FailedCompoundMutationStillInvalidates(t *testing.T) {
	cases := []struct {
		name string
		cmd  string
		want bool
	}{

		{"sed success then make fail", "sed -i 's/foo/bar/' x.go && make lint", true},
		{"gofmt then failing go vet", "gofmt -w ./... ; go vet ./...", true},
		{"patch apply then failing build", "patch -p1 < fix.diff && go build ./...", true},

		{"go fmt rewrites in place", "go fmt ./...", true},

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

	failedResultIsError := true
	args, _ := json.Marshal(map[string]string{"command": "sed -i 's/a/b/' x.go && make lint"})
	parsed, _ := parseRunCommandArgs(args)
	//nolint:staticcheck // mirrors agent.go branch: no IsError condition remains
	invalidates := shellMutatesSources(parsed)
	if !invalidates {
		t.Fatal("failed compound mutation must still invalidate the command cache")
	}
	_ = failedResultIsError
}
