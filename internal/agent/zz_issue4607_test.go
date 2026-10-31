//go:build darwin || linux

package agent

import (
	"context"
	"strings"
	"testing"
)

// TestZZIssue4607_MarkerFreeFailureOutputCarriesTail pins #4607: a non-zero
// exit whose output contains NONE of the extractErrorLines marker substrings
// (the real-world "signal: killed" / segfault / OOM class) must not collapse
// to the zero-information "command failed: exit status 1" fallback - the
// injected Errors entry must carry the raw output tail so the auto-repair
// loop and ratchet MatchErrors see the actual failure text.
func TestZZIssue4607_MarkerFreeFailureOutputCarriesTail(t *testing.T) {
	a := &Agent{workingDir: "."}
	// "signal: killed" hits none of error/fail/undefined:/cannot find/panic:/fatal.
	result := a.executeVerifyCommand(context.Background(),
		`sh -c 'echo signal: killed; exit 1'`)
	if result.Passed {
		t.Fatalf("expected failure, got Passed=true (Output=%q)", result.Output)
	}
	if len(result.Errors) != 1 {
		t.Fatalf("expected exactly 1 fallback error entry, got %d: %v", len(result.Errors), result.Errors)
	}
	if !strings.Contains(result.Errors[0], "command failed") {
		t.Errorf("fallback entry lost the exit-status prefix: %q", result.Errors[0])
	}
	if !strings.Contains(result.Errors[0], "signal: killed") {
		t.Errorf("#4607 regression: marker-free output tail missing from injected error - auto-repair loop is blind: %q", result.Errors[0])
	}
}

// TestZZIssue4607_MarkerOutputStillUsesExtractedLines pins the complement:
// when the output DOES contain marker lines, extraction still wins and the
// fallback tail must not be attached (no double-reporting).
func TestZZIssue4607_MarkerOutputStillUsesExtractedLines(t *testing.T) {
	a := &Agent{workingDir: "."}
	result := a.executeVerifyCommand(context.Background(),
		`sh -c 'echo something exploded; echo "build error: undefined: Foo"; exit 1'`)
	if result.Passed {
		t.Fatalf("expected failure, got Passed=true")
	}
	if len(result.Errors) == 0 {
		t.Fatal("expected extracted error lines")
	}
	for _, e := range result.Errors {
		if strings.Contains(e, "output tail") {
			t.Errorf("fallback tail must not attach when extraction produced lines: %q", e)
		}
	}
	if !zz4607Contains(strings.Join(result.Errors, "\n"), "build error: undefined: Foo") {
		t.Errorf("extracted marker line missing: %v", result.Errors)
	}
}

// TestZZIssue4607_EmptyOutputKeepsBareFallback pins the empty-output corner:
// a command that fails with NO output at all keeps the plain exit-status
// fallback (no dangling "output tail:" header).
func TestZZIssue4607_EmptyOutputKeepsBareFallback(t *testing.T) {
	a := &Agent{workingDir: "."}
	result := a.executeVerifyCommand(context.Background(), "sh -c 'exit 1'")
	if result.Passed {
		t.Fatal("expected failure")
	}
	if len(result.Errors) != 1 || !strings.Contains(result.Errors[0], "command failed") {
		t.Fatalf("expected bare fallback entry, got %v", result.Errors)
	}
	if strings.Contains(result.Errors[0], "output tail") {
		t.Errorf("empty output must not carry a tail header: %q", result.Errors[0])
	}
}

func zz4607Contains(s, want string) bool {
	return strings.Contains(s, want)
}
