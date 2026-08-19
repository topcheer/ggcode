package agent

import (
	"encoding/json"
	"testing"
)

// Regression guard for #749: a shell source mutation (gofmt -w) between two
// builds must NOT trigger the idempotency warning - the "guaranteed identical"
// claim is factually false when sources changed via run_command.
func TestBuildIdempotency_ShellMutationSuppressesWarning(t *testing.T) {
	s := newBuildIdempotencyState()
	buildArgs, _ := json.Marshal(map[string]string{"command": "go build ./..."})
	fmtArgs, _ := json.Marshal(map[string]string{"command": "gofmt -w ."})

	s.recordToolCall("run_command", buildArgs, 1) // first build
	s.recordToolCall("run_command", fmtArgs, 2)   // source mutation via shell
	if warn := s.recordToolCall("run_command", buildArgs, 3); warn != "" {
		t.Fatalf("rebuild after shell source mutation must not warn, got: %q", warn)
	}
}

// Without the shell mutation the warning must still fire (detector intact).
func TestBuildIdempotency_PlainRebuildStillWarns(t *testing.T) {
	s := newBuildIdempotencyState()
	buildArgs, _ := json.Marshal(map[string]string{"command": "go build ./..."})

	s.recordToolCall("run_command", buildArgs, 1)
	if warn := s.recordToolCall("run_command", buildArgs, 2); warn == "" {
		t.Fatal("plain redundant rebuild must still warn")
	}
}

func TestShellMutatesSources(t *testing.T) {
	yes := []string{
		"gofmt -w .", "goimports -w ./cmd", "sed -i 's/a/b/' f.go",
		"git apply p.diff", "go mod tidy", "GOFLAGS=x go mod tidy",
		"# format\ngofmt -w .", "cd pkg && gofmt -w .",
	}
	no := []string{"go build ./...", "gofmt -l .", "cat go.mod", "sed -n 1p f.go", ""}
	for _, c := range yes {
		if !shellMutatesSources(c) {
			t.Errorf("shellMutatesSources(%q) = false, want true", c)
		}
	}
	for _, c := range no {
		if shellMutatesSources(c) {
			t.Errorf("shellMutatesSources(%q) = true, want false", c)
		}
	}
}
