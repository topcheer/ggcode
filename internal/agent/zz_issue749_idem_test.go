package agent

import (
	"encoding/json"
	"testing"
)

func TestBuildIdempotency_ShellMutationSuppressesWarning(t *testing.T) {
	s := newBuildIdempotencyState()
	buildArgs, _ := json.Marshal(map[string]string{"command": "go build ./..."})
	fmtArgs, _ := json.Marshal(map[string]string{"command": "gofmt -w ."})

	s.recordToolCall("run_command", buildArgs, 1)
	s.recordToolCall("run_command", fmtArgs, 2)
	if warn := s.recordToolCall("run_command", buildArgs, 3); warn != "" {
		t.Fatalf("rebuild after shell source mutation must not warn, got: %q", warn)
	}
}

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
		"go fmt ./...",
		"go fmt",
	}
	no := []string{"go build ./...", "gofmt -l .", "cat go.mod", "sed -n 1p f.go", "", "gofmt "}
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
