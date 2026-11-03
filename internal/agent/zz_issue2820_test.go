package agent

import (
	"strings"
	"testing"
)

func TestIssue2820DotSlashNormalization(t *testing.T) {
	cases := []struct {
		path, pattern string
		want          bool
	}{
		{"./auth/handler/x.go", "auth/handler", true},
		{"./internal/db/schema.go", "internal/db", true},
		{"auth/handler/x.go", "./auth/handler", true},
		{"./auth/handler/x.go", "./auth/handler", true},

		{"auth/handler/x.go", "auth/handler", true},
		{"internal/auth/handler.go", "auth", true},
		{"internal/authorization/handler.go", "auth", false},

		{"./auth/handler/x.go", ".", false},
	}
	for _, tc := range cases {
		if got := cvPathMatchesPattern(tc.path, tc.pattern); got != tc.want {
			t.Errorf("cvPathMatchesPattern(%q, %q) = %v, want %v", tc.path, tc.pattern, got, tc.want)
		}
	}

	s := &constraintViolationState{
		constraints: []cvConstraint{{constraintT: "scope", pattern: "auth/handler", iter: 1, excerpt: "limit my changes to `auth/handler`"}},
	}
	if msg := s.checkToolCall("edit_file", map[string]any{"file_path": "./auth/handler/login.go"}, 2); msg != "" {
		t.Errorf("#2820 scope false-positive survived: %s", msg)
	}

	s2 := &constraintViolationState{
		constraints: []cvConstraint{{constraintT: "avoid", pattern: "internal/db", iter: 1, excerpt: "won't touch `internal/db`"}},
	}
	msg := s2.checkToolCall("edit_file", map[string]any{"file_path": "./internal/db/schema.go"}, 2)
	if msg == "" || !strings.Contains(msg, "avoid") {
		t.Errorf("#2820 avoid false-negative survived: %q", msg)
	}
}
