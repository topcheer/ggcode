package agent

// Regression tests for issue #1190: verificationSignature previously
// fingerprinted only the FIRST pipeline/command segment, so
// `cd internal/agent && go test ./agentruntime/` fingerprinted as
// "cd internal/agent". A subsequent `cd internal/agent && go test ./auth/`
// collided and was wrongly flagged as a redundant re-run, discouraging the
// agent from legitimate verification of a different target (violating the
// #1173 design intent: different verification targets are never idempotent
// re-runs of each other).

import (
	"strings"
	"testing"
)

func TestIssue1190_VerificationSignature_SelectsVerificationSegment(t *testing.T) {
	cases := []struct {
		name string
		args string
		want string
	}{
		{
			name: "cd prefix before go test",
			args: "cd internal/agent && go test ./agentruntime/",
			want: "go test ./agentruntime/",
		},
		{
			name: "cd prefix, different test target",
			args: "cd internal/agent && go test ./auth/",
			want: "go test ./auth/",
		},
		{
			name: "semicolon separator",
			args: "cd pkg ; go test ./a/",
			want: "go test ./a/",
		},
		{
			name: "env assignment prefix",
			args: "FOO=1 go test ./a/",
			want: "go test ./a/",
		},
		{
			name: "dollar expansion prefix",
			args: "$(go env GOPATH) go test ./a/",
			want: "go test ./a/",
		},
		{
			name: "plain go test unchanged",
			args: "go test ./a/",
			want: "go test ./a/",
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := verificationSignature(tc.args)
			if got != tc.want {
				t.Errorf("verificationSignature(%q) = %q, want %q", tc.args, got, tc.want)
			}
		})
	}
}

// Different test targets behind the same cd prefix must NOT be treated as
// redundant re-runs (issue #1190 core scenario).
func TestIssue1190_DifferentTargetAfterCdPrefix_NoWarning(t *testing.T) {
	s := newRedundantReverifyState()
	h1 := s.recordToolCall("run_command", "cd internal/agent && go test ./agentruntime/", 1, false)
	if h1 != "" {
		t.Fatalf("first run must never warn, got: %s", h1)
	}
	h2 := s.recordToolCall("run_command", "cd internal/agent && go test ./auth/", 2, false)
	if h2 != "" {
		t.Errorf("verifying a DIFFERENT target must not be flagged redundant (#1190), got: %s", h2)
	}
}

// Same verification target expressed with different prefix forms MUST still
// be flagged as a redundant re-run (the fix must not over-suppress).
func TestIssue1190_SameTargetDifferentPrefixForms_StillWarns(t *testing.T) {
	s := newRedundantReverifyState()
	if h := s.recordToolCall("run_command", "go test ./internal/agent/", 1, false); h != "" {
		t.Fatalf("first run must never warn, got: %s", h)
	}
	h2 := s.recordToolCall("run_command", "cd . && go test ./internal/agent/", 2, false)
	if h2 == "" {
		t.Error("same target with different cd prefix should still warn as redundant")
	} else if !strings.Contains(h2, "Redundant Re-verification") {
		t.Errorf("warning should be a redundant-reverify hint, got: %s", h2)
	}
}

// Identical full command still warns (baseline behavior preserved).
func TestIssue1190_IdenticalCommandWithCdPrefix_StillWarns(t *testing.T) {
	s := newRedundantReverifyState()
	if h := s.recordToolCall("run_command", "cd internal/agent && go test ./...", 1, false); h != "" {
		t.Fatalf("first run must never warn, got: %s", h)
	}
	if h := s.recordToolCall("run_command", "cd internal/agent && go test ./...", 2, false); h == "" {
		t.Error("identical command (cd prefix included) should warn as redundant")
	}
}

// Build commands behind cd prefixes are covered by the same mechanism.
func TestIssue1190_BuildSegmentAfterCdPrefix(t *testing.T) {
	s := newRedundantReverifyState()
	if h := s.recordToolCall("run_command", "cd cmd/ggcode && go build ./...", 1, false); h != "" {
		t.Fatalf("first run must never warn, got: %s", h)
	}
	if h := s.recordToolCall("run_command", "cd cmd/ggcode && go build ./internal/...", 2, false); h != "" {
		t.Errorf("different build target must not be flagged redundant, got: %s", h)
	}
}

// Fallback: no segment matches a verification verb -> full args fingerprint,
// so identical unmatched commands still collide deterministically.
func TestIssue1190_FallbackFullArgsWhenNoVerbSegment(t *testing.T) {
	if got := verificationSignature("echo hi"); got != "echo hi" {
		t.Errorf("fallback signature = %q, want %q", got, "echo hi")
	}
}

// commandSegments sanity: || binds before |, empty segments dropped.
func TestIssue1190_CommandSegments(t *testing.T) {
	segs := commandSegments("a && b || c ; d | e")
	want := []string{"a", "b", "c", "d", "e"}
	if len(segs) != len(want) {
		t.Fatalf("commandSegments returned %v, want %v", segs, want)
	}
	for i := range want {
		if segs[i] != want[i] {
			t.Errorf("segment %d = %q, want %q", i, segs[i], want[i])
		}
	}
}
