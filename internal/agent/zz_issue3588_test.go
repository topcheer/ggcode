package agent

// #3588 probe: git-family content commands whose arguments QUOTE a
// verification verb ("go test" as a search literal) must not be classified
// as verification runs - the verb is data, not execution. Real verification
// commands must still be classified (both quoted-strip and blocklist must
// not over-block).

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
	// "make test pass" in the commit message is data.
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
	// Unquoted, command-position verb: must still classify and flag the
	// redundant repeat exactly as before the fix.
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
	// A verification verb in command position after an env prefix (no text
	// tool, no wrapper) still classifies even when another arg quotes text.
	if got := s3588Classify(t, `env CGO_ENABLED=0 go vet ./...`); got != "lint" {
		t.Fatalf("env-prefixed go vet classified %q, want lint", got)
	}
	// The stripper itself: length-preserving, quoted contents erased.
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

// Production shape: agent.go passes RAW JSON arguments (tc.Arguments) where
// the shell command lives in the "command" field. JSON-level quoting must
// not confuse the shell-level analysis, in both directions.
func TestIssue3588_RawJSONArgsBothDirections(t *testing.T) {
	s := newRedundantReverifyState()
	// JSON git grep quoting a verb: the escaped quotes become real shell
	// quotes after extraction - git is blocked AND the verb is in quotes.
	gitJSON := `{"command":"git grep -n \"go test\" ./internal/","description":"find literal"}`
	if h1 := s.recordToolCall("run_command", gitJSON, 1, false); h1 != "" {
		t.Fatalf("JSON git grep must not hint, got %q", h1)
	}
	if h2 := s.recordToolCall("run_command", gitJSON, 2, false); h2 != "" {
		t.Fatalf("JSON git grep repeat must not hint, got %q", h2)
	}
	// JSON real verification: classify + redundant repeat warns (#1486 parity).
	s2 := newRedundantReverifyState()
	goJSON := `{"command":"go test ./..."}`
	s2.recordToolCall("run_command", goJSON, 1, false)
	if h := s2.recordToolCall("run_command", goJSON, 2, false); h == "" {
		t.Fatal("JSON go test repeat MUST still hint")
	}
}
