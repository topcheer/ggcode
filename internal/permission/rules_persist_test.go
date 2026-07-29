package permission

import (
	"encoding/json"
	"path/filepath"
	"testing"
)

func TestRulesPersist_SaveLoad(t *testing.T) {
	tmpDir := t.TempDir()
	path := filepath.Join(tmpDir, "permission_rules.json")

	data := &PermissionRulesFile{
		ToolOverrides: map[string]string{
			"run_command": "allow",
			"write_file":  "deny",
		},
		CommandAllowPatterns: []string{"git diff*", "npm test*"},
		CommandDenyPatterns:  []string{"rm -rf*"},
	}

	if err := SaveRules(path, data); err != nil {
		t.Fatal(err)
	}

	loaded := LoadRules(path)
	if len(loaded.ToolOverrides) != 2 {
		t.Errorf("expected 2 tool overrides, got %d", len(loaded.ToolOverrides))
	}
	if loaded.ToolOverrides["run_command"] != "allow" {
		t.Errorf("expected run_command=allow, got %s", loaded.ToolOverrides["run_command"])
	}
	if len(loaded.CommandAllowPatterns) != 2 {
		t.Errorf("expected 2 allow patterns, got %d", len(loaded.CommandAllowPatterns))
	}
	if len(loaded.CommandDenyPatterns) != 1 {
		t.Errorf("expected 1 deny pattern, got %d", len(loaded.CommandDenyPatterns))
	}
}

func TestRulesPersist_LoadMissing(t *testing.T) {
	loaded := LoadRules("/nonexistent/path/rules.json")
	if loaded == nil {
		t.Fatal("expected non-nil result for missing file")
	}
	if len(loaded.ToolOverrides) != 0 {
		t.Errorf("expected empty tool overrides, got %d", len(loaded.ToolOverrides))
	}
}

func TestRulesPersist_ApplyToPolicy(t *testing.T) {
	data := &PermissionRulesFile{
		ToolOverrides: map[string]string{
			"run_command": "allow",
			"edit_file":   "deny",
		},
		CommandAllowPatterns: []string{"git diff*"},
		CommandDenyPatterns:  []string{"rm*"},
	}

	p := NewConfigPolicy(nil, nil)
	data.ApplyToPolicy(p)

	if d, _ := p.Check("run_command", json.RawMessage(`{"command":"git diff"}`)); d != Allow {
		t.Errorf("expected run_command git diff to be allowed")
	}
	if d, _ := p.Check("edit_file", json.RawMessage(`{"file_path":"/tmp/x"}`)); d != Deny {
		t.Errorf("expected edit_file to be denied")
	}
}

func TestRulesPersist_SnapshotRoundTrip(t *testing.T) {
	p := NewConfigPolicy(nil, []string{"/tmp"})
	p.SetOverride("write_file", Allow)
	p.AllowCommandPattern("go build*")

	data := SnapshotRules(p, "/tmp")
	if data == nil {
		t.Fatal("expected non-nil snapshot")
	}
	if data.ToolOverrides["write_file"] != "allow" {
		t.Errorf("expected write_file=allow, got %s", data.ToolOverrides["write_file"])
	}
	// go build* should be in allow patterns
	found := false
	for _, pat := range data.CommandAllowPatterns {
		if pat == "go build*" {
			found = true
			break
		}
	}
	if !found {
		t.Errorf("expected 'go build*' in allow patterns, got %v", data.CommandAllowPatterns)
	}

	// Round-trip: apply snapshot to a new policy
	p2 := NewConfigPolicy(nil, []string{"/tmp"})
	data.ApplyToPolicy(p2)
	if d, _ := p2.Check("write_file", json.RawMessage(`{"file_path":"/tmp/x","content":"y"}`)); d != Allow {
		t.Errorf("expected write_file Allow after round-trip, got %s", d)
	}
	if d, _ := p2.Check("run_command", json.RawMessage(`{"command":"go build ./..."}`)); d != Allow {
		t.Errorf("expected 'go build' allowed via command pattern, got %s", d)
	}
}

func TestUserFriendlyPatterns(t *testing.T) {
	tests := []struct {
		regex string
		glob  string
	}{
		{"(?i)^git\\ diff.*", "git diff*"},
		{"(?i)^npm\\ test.*", "npm test*"},
		{"(?i)^make.*", "make*"},
		{"(?i)^ls.*", "ls*"},
	}
	for _, tt := range tests {
		got := userFriendlyPatterns([]string{tt.regex})
		if len(got) != 1 || got[0] != tt.glob {
			t.Errorf("userFriendlyPatterns(%q) = %v, want %q", tt.regex, got, tt.glob)
		}
	}
}

func TestSupervisedReadOnlyAutoAllow(t *testing.T) {
	p := NewConfigPolicyWithMode(nil, []string{"/tmp/work"}, SupervisedMode)

	// read_file inside sandbox: auto-allowed
	d, _ := p.Check("read_file", json.RawMessage(`{"file_path":"/tmp/work/test.go"}`))
	if d != Allow {
		t.Errorf("expected read_file inside sandbox to be auto-allowed, got %s", d)
	}

	// read_file on non-sensitive path outside sandbox: auto-allowed
	d, _ = p.Check("read_file", json.RawMessage(`{"file_path":"/tmp/other/test.go"}`))
	if d != Allow {
		t.Errorf("expected read_file outside sandbox non-sensitive to be auto-allowed, got %s", d)
	}

	// read_file on sensitive path outside sandbox: still Ask
	d, _ = p.Check("read_file", json.RawMessage(`{"file_path":"/root/.ssh/id_rsa"}`))
	if d != Ask {
		t.Errorf("expected read_file sensitive path to be Ask, got %s", d)
	}

	// grep: read-only, always allowed
	d, _ = p.Check("grep", json.RawMessage(`{"pattern":"TODO"}`))
	if d != Allow {
		t.Errorf("expected grep to be auto-allowed, got %s", d)
	}

	// Non-read-only tools: still Ask by default
	d, _ = p.Check("edit_file", json.RawMessage(`{"file_path":"/tmp/work/test.go"}`))
	if d != Ask {
		t.Errorf("expected edit_file to be Ask, got %s", d)
	}
}
