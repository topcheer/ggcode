package permission

import (
	"encoding/json"
	"os"
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

		{"git diff*", "git diff --stat", true},
		{"git diff*", "git diff", true},
		{"git diff*", "git diff HEAD~1", true},
		{"git diff*", "git push", false},
		{"git diff*", "git log", false},

		{"npm test*", "npm test", true},
		{"npm test*", "npm test --watch", true},
		{"npm test*", "npm run build", false},

		{"make*", "make", true},
		{"make*", "make build", true},
		{"make*", "make test CI=true", true},

		{"go build*", "go build ./...", true},
		{"go build*", "go build -tags goolm", true},
		{"go build*", "go test", false},

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

	d, matched := rs.Check("git diff")
	if matched {
		t.Errorf("expected no match before adding pattern, got %v", d)
	}

	rs.AddAllowPattern("git diff*")

	d, matched = rs.Check("git diff --stat")
	if !matched || d != Allow {
		t.Errorf("expected (Allow, true) after adding pattern, got (%v, %v)", d, matched)
	}

	d, matched = rs.Check("git push")
	if matched {
		t.Errorf("expected no match for unrelated command, got %v", d)
	}
}

func TestCommandRuleSet_DenyPrecedence(t *testing.T) {
	rs := NewCommandRuleSet()
	rs.AddAllowPattern("git*")
	rs.AddDenyPattern("git push*")

	d, matched := rs.Check("git diff")
	if !matched || d != Allow {
		t.Errorf("expected Allow for 'git diff', got (%v, %v)", d, matched)
	}

	d, matched = rs.Check("git push origin main")
	if !matched || d != Deny {
		t.Errorf("expected Deny for 'git push' (deny overrides allow), got (%v, %v)", d, matched)
	}
}

func TestCommandRuleSet_NoDuplicatePatterns(t *testing.T) {
	rs := NewCommandRuleSet()
	rs.AddAllowPattern("git diff*")
	rs.AddAllowPattern("git diff*")

	if len(rs.AllowPatterns()) != 1 {
		t.Errorf("expected 1 pattern after duplicate add, got %d", len(rs.AllowPatterns()))
	}
}

func TestConfigPolicy_CommandRulesIntegrated(t *testing.T) {
	policy := NewConfigPolicyWithMode(nil, []string{"."}, SupervisedMode)

	policy.AllowCommandPattern("git diff*")

	input := json.RawMessage(`{"command":"git diff --stat"}`)
	d, err := policy.Check("run_command", input)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if d != Allow {
		t.Errorf("expected Allow for matched command pattern, got %v", d)
	}

	input2 := json.RawMessage(`{"command":"git push"}`)
	d2, _ := policy.Check("run_command", input2)
	if d2 != Allow {
		t.Errorf("expected Allow for unmatched command (default Allow), got %v", d2)
	}
}

func TestConfigPolicy_CommandRulesStillCheckDangerous(t *testing.T) {
	policy := NewConfigPolicyWithMode(nil, []string{"."}, SupervisedMode)

	policy.AllowCommandPattern("rm*")

	input := json.RawMessage(`{"command":"rm -rf /tmp/test"}`)
	d, _ := policy.Check("run_command", input)
	if d != Allow {
		t.Errorf("expected Allow for non-extreme rm (no gate for ordinary dangerous), got %v", d)
	}

	inputED := json.RawMessage(`{"command":"rm -rf /"}`)
	dED, _ := policy.Check("run_command", inputED)
	if dED != Ask {
		t.Errorf("expected Ask for extremely dangerous rm -rf / despite allow pattern, got %v", dED)
	}
}

func TestConfigPolicy_CommandRulesOnlyForCommandTools(t *testing.T) {
	policy := NewConfigPolicyWithMode(nil, []string{"."}, SupervisedMode)
	policy.AllowCommandPattern("anything*")

	input := json.RawMessage(`{"command":"anything here"}`)
	d, _ := policy.Check("edit_file", input)
	if d != Allow {
		t.Errorf("command rules should not affect non-command tools (supervised default Allow), got %v", d)
	}
}

func TestCommandRuleSetSaveAtomicRoundtrip(t *testing.T) {
	dir := t.TempDir()
	path := dir + "/cmd_rules.json"
	rs := NewCommandRuleSetFromLists([]string{"git status"}, []string{"rm -rf *"})
	if err := rs.Save(path); err != nil {
		t.Fatal(err)
	}
	b, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	var got commandRuleFile
	if err := json.Unmarshal(b, &got); err != nil {
		t.Fatalf("saved file not valid JSON (truncated?): %v", err)
	}
	if len(got.Deny) != 1 || got.Deny[0] != "rm -rf *" {
		t.Fatalf("deny rules lost: %+v", got)
	}
	if entries, _ := os.ReadDir(dir); len(entries) != 1 {
		t.Fatalf("temp file leaked: %d entries", len(entries))
	}
}
