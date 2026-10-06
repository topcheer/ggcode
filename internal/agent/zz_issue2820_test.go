package agent

import (
	"strings"
	"testing"
)

// zz_issue2820_test.go - probe for #2820: cvPathMatchesPattern must normalize
// the leading "./" on both tool-arg paths and quoted patterns. Before the fix,
// "./auth/handler/x.go" vs pattern "auth/handler" missed on all three match
// paths, producing scope false-positives ("outside scope") and avoid
// false-negatives (a declared-untouched directory edited without warning).
func TestIssue2820DotSlashNormalization(t *testing.T) {
	cases := []struct {
		path, pattern string
		want          bool
	}{
		{"./auth/handler/x.go", "auth/handler", true},
		{"./internal/db/schema.go", "internal/db", true},
		{"auth/handler/x.go", "./auth/handler", true},
		{"./auth/handler/x.go", "./auth/handler", true},
		// Pre-existing behavior must be preserved (no ./ involved).
		{"auth/handler/x.go", "auth/handler", true},
		{"internal/auth/handler.go", "auth", true},
		{"internal/authorization/handler.go", "auth", false},
		// "./" alone must not become an empty pattern match-all.
		{"./auth/handler/x.go", ".", false},
	}
	for _, tc := range cases {
		if got := cvPathMatchesPattern(tc.path, tc.pattern); got != tc.want {
			t.Errorf("cvPathMatchesPattern(%q, %q) = %v, want %v", tc.path, tc.pattern, got, tc.want)
		}
	}

	// End-to-end through checkToolCall: scope constraint + "./" prefixed edit
	// target must NOT be flagged as outside scope.
	s := &constraintViolationState{
		constraints: []cvConstraint{{constraintT: "scope", pattern: "auth/handler", iter: 1, excerpt: "limit my changes to `auth/handler`"}},
	}
	if msg := s.checkToolCall("edit_file", map[string]any{"file_path": "./auth/handler/login.go"}, 2); msg != "" {
		t.Errorf("#2820 scope false-positive survived: %s", msg)
	}

	// Avoid constraint + "./" prefixed edit target MUST warn (was missed).
	s2 := &constraintViolationState{
		constraints: []cvConstraint{{constraintT: "avoid", pattern: "internal/db", iter: 1, excerpt: "won't touch `internal/db`"}},
	}
	msg := s2.checkToolCall("edit_file", map[string]any{"file_path": "./internal/db/schema.go"}, 2)
	if msg == "" || !strings.Contains(msg, "avoid") {
		t.Errorf("#2820 avoid false-negative survived: %q", msg)
	}
}
