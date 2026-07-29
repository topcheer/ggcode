package permission

import (
	"encoding/json"
	"testing"
)

func TestCommandPrefixToPattern(t *testing.T) {
	tests := []struct {
		command string
		want    string
	}{
		{"git diff --stat", "git diff*"},
		{"npm test", "npm test*"},
		{"go build -tags goolm ./...", "go build*"},
		{"go test ./...", "go test*"},
		{"make", "make*"},
		{"ls -la", "ls*"},
		{"git status", "git status*"},
		{"git", "git*"},
		{"  ", ""},
		{"kubectl get pods", "kubectl get*"},
		{"docker ps", "docker ps*"},
	}
	for _, tt := range tests {
		t.Run(tt.command, func(t *testing.T) {
			got := CommandPrefixToPattern(tt.command)
			if got != tt.want {
				t.Errorf("CommandPrefixToPattern(%q) = %q, want %q", tt.command, got, tt.want)
			}
		})
	}
}

func TestCompileCommandPattern(t *testing.T) {
	tests := []struct {
		pattern string
		command string
		want    bool
	}{
		// Basic prefix matching with wildcard
		{"git diff*", "git diff --stat", true},
		{"git diff*", "git diff", true},
		{"git diff*", "git diff HEAD~1", true},
		{"git diff*", "git push", false},
		{"git diff*", "git log", false},

		// Exact (with trailing *)
		{"npm test*", "npm test", true},
		{"npm test*", "npm test --watch", true},
		{"npm test*", "npm run build", false},

		// Full wildcard
		{"make*", "make", true},
		{"make*", "make build", true},
		{"make*", "make test CI=true", true},

		// Regex special chars should be escaped
		{"go build*", "go build ./...", true},
		{"go build*", "go build -tags goolm", true},
		{"go build*", "go test", false},

		// Case insensitive
		{"git diff*", "GIT DIFF", true},
		{"git diff*", "Git Diff --stat", true},
	}
	for _, tt := range tests {
		t.Run(tt.pattern+"/"+tt.command, func(t *testing.T) {
			re, err := compileCommandPattern(tt.pattern)
			if err != nil {
				t.Fatalf("compileCommandPattern(%q) error: %v", tt.pattern, err)
			}
			got := re.MatchString(tt.command)
			if got != tt.want {
				t.Errorf("pattern %q matching %q = %v, want %v", tt.pattern, tt.command, got, tt.want)
			}
		})
	}
}

func TestCommandRuleSet_Check(t *testing.T) {
	rs := NewCommandRuleSetFromLists(
		[]string{"git diff*", "go build*", "npm test*"},
		[]string{"git push*"},
	)

	tests := []struct {
		command string
		want    Decision
		matched bool
	}{
		{"git diff --stat", Allow, true},
		{"git diff", Allow, true},
		{"go build -tags goolm", Allow, true},
		{"npm test", Allow, true},
		{"npm test --watch", Allow, true},
		{"git push origin main", Deny, true},
		{"git push --force", Deny, true},
		{"git log", Ask, false},
		{"rm -rf /tmp", Ask, false},
		{"", Ask, false},
	}
	for _, tt := range tests {
		t.Run(tt.command, func(t *testing.T) {
			got, matched := rs.Check(tt.command)
			if got != tt.want || matched != tt.matched {
				t.Errorf("Check(%q) = (%v, %v), want (%v, %v)", tt.command, got, matched, tt.want, tt.matched)
			}
		})
	}
}

func TestCommandRuleSet_AddAllowPattern(t *testing.T) {
	rs := NewCommandRuleSet()

	// Before adding: no match
	d, matched := rs.Check("git diff")
	if matched {
		t.Errorf("expected no match before adding pattern, got %v", d)
	}

	// Add pattern
	rs.AddAllowPattern("git diff*")

	// After adding: should match
	d, matched = rs.Check("git diff --stat")
	if !matched || d != Allow {
		t.Errorf("expected (Allow, true) after adding pattern, got (%v, %v)", d, matched)
	}

	// Unrelated command should still not match
	d, matched = rs.Check("git push")
	if matched {
		t.Errorf("expected no match for unrelated command, got %v", d)
	}
}

func TestCommandRuleSet_DenyPrecedence(t *testing.T) {
	rs := NewCommandRuleSet()
	rs.AddAllowPattern("git*")
	rs.AddDenyPattern("git push*")

	// Allow pattern should match general git commands
	d, matched := rs.Check("git diff")
	if !matched || d != Allow {
		t.Errorf("expected Allow for 'git diff', got (%v, %v)", d, matched)
	}

	// Deny should override allow for git push
	d, matched = rs.Check("git push origin main")
	if !matched || d != Deny {
		t.Errorf("expected Deny for 'git push' (deny overrides allow), got (%v, %v)", d, matched)
	}
}

func TestCommandRuleSet_NoDuplicatePatterns(t *testing.T) {
	rs := NewCommandRuleSet()
	rs.AddAllowPattern("git diff*")
	rs.AddAllowPattern("git diff*") // duplicate

	if len(rs.AllowPatterns()) != 1 {
		t.Errorf("expected 1 pattern after duplicate add, got %d", len(rs.AllowPatterns()))
	}
}

func TestConfigPolicy_CommandRulesIntegrated(t *testing.T) {
	policy := NewConfigPolicyWithMode(nil, []string{"."}, SupervisedMode)

	// Add a command allow pattern
	policy.AllowCommandPattern("git diff*")

	// "git diff" should now be auto-allowed in supervised mode
	input := json.RawMessage(`{"command":"git diff --stat"}`)
	d, err := policy.Check("run_command", input)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if d != Allow {
		t.Errorf("expected Allow for matched command pattern, got %v", d)
	}

	// Unmatched command should still ask
	input2 := json.RawMessage(`{"command":"git push"}`)
	d2, _ := policy.Check("run_command", input2)
	if d2 != Ask {
		t.Errorf("expected Ask for unmatched command, got %v", d2)
	}
}

func TestConfigPolicy_CommandRulesStillCheckDangerous(t *testing.T) {
	policy := NewConfigPolicyWithMode(nil, []string{"."}, SupervisedMode)

	// Even if a pattern matches, dangerous commands should still ask
	policy.AllowCommandPattern("rm*")

	input := json.RawMessage(`{"command":"rm -rf /tmp/test"}`)
	d, _ := policy.Check("run_command", input)
	// rm -rf is dangerous — should still ask despite matching allow pattern
	if d != Ask {
		t.Errorf("expected Ask for dangerous command despite allow pattern, got %v", d)
	}
}

func TestConfigPolicy_CommandRulesOnlyForCommandTools(t *testing.T) {
	policy := NewConfigPolicyWithMode(nil, []string{"."}, SupervisedMode)
	policy.AllowCommandPattern("anything*")

	// Non-command tools should not be affected
	input := json.RawMessage(`{"command":"anything here"}`)
	d, _ := policy.Check("edit_file", input)
	if d != Ask {
		t.Errorf("command rules should not affect non-command tools, got %v", d)
	}
}
