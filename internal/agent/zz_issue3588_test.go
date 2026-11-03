package agent

import (
	"strings"
	"testing"
)

func TestIssue3588_GitGrepQuotedVerbNotVerification(t *testing.T) {
	s := newRedundantReverifyState()
	args := `git grep -n "go test" ./internal/`
	h1 := s.recordToolCall("run_command", args, 1, false)
	h2 := s.recordToolCall("run_command", args, 2, false)
	if h1 != "" || h2 != "" {
		t.Fatalf("git grep with quoted verb must never hint (got h1=%q h2=%q)", h1, h2)
	}
}

func TestIssue3588_GitCommitQuotedVerbNotVerification(t *testing.T) {
	s := newRedundantReverifyState()

	h := s.recordToolCall("run_command", `git commit -m "make test pass"`, 1, false)
	if h != "" {
		t.Fatalf("git commit with quoted verb must never hint, got %q", h)
	}
	if got := s.classifyVerificationCommand("run_command", `git commit -m "make test pass"`); got != "" {
		t.Fatalf("classified %q, want empty", got)
	}
}

func TestIssue3588_WrapperQuotedVerbNotVerification(t *testing.T) {
	s := newRedundantReverifyState()
	for _, args := range []string{
		`docker run --rm img make test`,
		`kubectl logs app | grep "go build"`,
	} {
		if got := s.classifyVerificationCommand("run_command", args); got != "" {
			t.Fatalf("wrapper %q classified %q, want empty", args, got)
		}
	}
}

func TestIssue3588_RealVerificationStillDetected(t *testing.T) {
	s := newRedundantReverifyState()

	h1 := s.recordToolCall("run_command", "go test ./internal/agent/", 1, false)
	if h1 != "" {
		t.Fatalf("first run must not hint, got %q", h1)
	}
	h2 := s.recordToolCall("run_command", "go test ./internal/agent/", 2, false)
	if h2 == "" {
		t.Fatal("real redundant go test repeat MUST still hint")
	}
}

func TestIssue3588_QuotedStripKeepsCommandPosition(t *testing.T) {

	if got := s3588Classify(t, `env CGO_ENABLED=0 go vet ./...`); got != "lint" {
		t.Fatalf("env-prefixed go vet classified %q, want lint", got)
	}

	in := `grep -n "go test" 'x y' tail`
	got := stripQuotedSpans(in)
	if len(got) != len(in) {
		t.Fatalf("stripQuotedSpans must be length-preserving: %q vs %q", got, in)
	}
	for _, leaked := range []string{"go test", "x y", `"`, `'`} {
		if strings.Contains(got, leaked) {
			t.Fatalf("quoted content %q leaked: %q", leaked, got)
		}
	}
	if !strings.Contains(got, "grep") || !strings.Contains(got, "tail") {
		t.Fatalf("unquoted parts must survive: %q", got)
	}
}

func s3588Classify(t *testing.T, args string) string {
	t.Helper()
	s := newRedundantReverifyState()
	return s.classifyVerificationCommand("run_command", args)
}

func TestIssue3588_RawJSONArgsBothDirections(t *testing.T) {
	s := newRedundantReverifyState()

	gitJSON := `{"command":"git grep -n \"go test\" ./internal/","description":"find literal"}`
	if h1 := s.recordToolCall("run_command", gitJSON, 1, false); h1 != "" {
		t.Fatalf("JSON git grep must not hint, got %q", h1)
	}
	if h2 := s.recordToolCall("run_command", gitJSON, 2, false); h2 != "" {
		t.Fatalf("JSON git grep repeat must not hint, got %q", h2)
	}

	s2 := newRedundantReverifyState()
	goJSON := `{"command":"go test ./..."}`
	s2.recordToolCall("run_command", goJSON, 1, false)
	if h := s2.recordToolCall("run_command", goJSON, 2, false); h == "" {
		t.Fatal("JSON go test repeat MUST still hint")
	}
}
