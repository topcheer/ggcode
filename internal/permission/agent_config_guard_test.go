package permission

// Tests for the sa-43 agent config guard: writes to the agent's own
// configuration / instruction files require a fresh human confirmation in
// every mode, and learned approvals never auto-approve them.

import (
	"encoding/json"
	"path/filepath"
	"testing"

	"github.com/topcheer/ggcode/internal/config"
)

func guardPolicy(mode PermissionMode, rules map[string]Decision) *ConfigPolicy {
	return NewConfigPolicyWithMode(rules, []string{"/sa43-proj"}, mode)
}

func guardCheck(t *testing.T, mode PermissionMode, tool, input string) Decision {
	t.Helper()
	d, err := guardPolicy(mode, nil).Check(tool, json.RawMessage(input))
	if err != nil {
		t.Fatalf("Check(%s, %s) error: %v", tool, input, err)
	}
	return d
}

func TestAgentConfigGuardBypassBlocksConfigWrites(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	cases := []struct {
		name string
		path string
	}{
		{"mcp json workspace root", "/sa43-proj/.mcp.json"},
		{"mcp json nested", "/sa43-proj/packages/app/.mcp.json"},
		{"instruction ggcode.md", "/sa43-proj/GGCODE.md"},
		{"instruction nested AGENTS.md", "/sa43-proj/sub/AGENTS.md"},
		{"ggcode harness config", "/sa43-proj/.ggcode/harness.yaml"},
		{"ggcode skill definition", "/sa43-proj/.ggcode/skills/deploy.md"},
		{"global config.yaml", filepath.Join(config.ConfigDir(), "config.yaml")},
		{"global mcp servers", filepath.Join(config.ConfigDir(), "mcp_servers.yaml")},
		{"case dodge", "/sa43-proj/.MCP.JSON"},
	}
	for _, mode := range []PermissionMode{BypassMode, AutopilotMode} {
		for _, tc := range cases {
			t.Run(mode.String()+"/"+tc.name, func(t *testing.T) {
				in, _ := json.Marshal(map[string]string{"file_path": tc.path})
				d, err := guardPolicy(mode, nil).Check("write_file", in)
				if err != nil {
					t.Fatalf("Check error: %v", err)
				}
				if d != Ask {
					t.Fatalf("%s write_file %s = %v, want Ask", mode, tc.path, d)
				}
			})
		}
	}
}

func TestAgentConfigGuardLeavesOrdinaryWritesAlone(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	paths := []string{
		"/sa43-proj/README.md",
		"/sa43-proj/internal/foo/foo.go",
		"/sa43-proj/docs/guide.md",
		// state dirs are agent-owned data, not configuration
		"/sa43-proj/.ggcode/memories/notes.md",
		"/sa43-proj/.ggcode/worktrees/sa-43/x.go",
		"/sa43-proj/.ggcode/memory/auto.md",
		"/sa43-proj/.ggcode/run/state.json",
	}
	for _, mode := range []PermissionMode{BypassMode, AutopilotMode, AutoMode} {
		for _, p := range paths {
			in, _ := json.Marshal(map[string]string{"file_path": p})
			d, err := guardPolicy(mode, nil).Check("write_file", in)
			if err != nil {
				t.Fatalf("Check error: %v", err)
			}
			if d != Allow {
				t.Fatalf("%s write_file %s = %v, want Allow", mode, p, d)
			}
		}
	}
}

func TestAgentConfigGuardReadsStayFree(t *testing.T) {
	in, _ := json.Marshal(map[string]string{"file_path": "/sa43-proj/.mcp.json"})
	d, err := guardPolicy(BypassMode, nil).Check("read_file", in)
	if err != nil {
		t.Fatalf("Check error: %v", err)
	}
	if d != Allow {
		t.Fatalf("read_file .mcp.json = %v, want Allow (reads are legitimate)", d)
	}
}

func TestAgentConfigGuardAutoModeAsks(t *testing.T) {
	in, _ := json.Marshal(map[string]string{"file_path": "/sa43-proj/.mcp.json"})
	d, err := guardPolicy(AutoMode, nil).Check("edit_file", in)
	if err != nil {
		t.Fatalf("Check error: %v", err)
	}
	if d != Ask {
		t.Fatalf("auto edit_file .mcp.json = %v, want Ask", d)
	}
}

func TestAgentConfigGuardRedirectTargets(t *testing.T) {
	cases := []struct {
		mode PermissionMode
		cmd  string
		want Decision
	}{
		{BypassMode, "echo '{}' > /sa43-proj/.mcp.json", Ask},
		{BypassMode, "echo note >> /sa43-proj/GGCODE.md", Ask},
		{BypassMode, "echo ok > /sa43-proj/out.txt", Allow},
		{AutoMode, "echo '{}' > /sa43-proj/.mcp.json", Ask},
		{AutoMode, "echo ok > /sa43-proj/out.txt", Allow},
	}
	for _, tc := range cases {
		in, _ := json.Marshal(map[string]string{"command": tc.cmd})
		d, err := guardPolicy(tc.mode, nil).Check("run_command", in)
		if err != nil {
			t.Fatalf("Check error: %v", err)
		}
		if d != tc.want {
			t.Fatalf("%s run_command %q = %v, want %v", tc.mode, tc.cmd, d, tc.want)
		}
	}
}

func TestAgentConfigGuardExplicitAllowRuleDowngrades(t *testing.T) {
	p := guardPolicy(SupervisedMode, map[string]Decision{"write_file": Allow})
	in, _ := json.Marshal(map[string]string{"file_path": "/sa43-proj/.mcp.json"})
	d, err := p.Check("write_file", in)
	if err != nil {
		t.Fatalf("Check error: %v", err)
	}
	if d != Ask {
		t.Fatalf("explicit allow rule + .mcp.json = %v, want Ask", d)
	}
	// ordinary files keep the explicit allow
	in2, _ := json.Marshal(map[string]string{"file_path": "/sa43-proj/README.md"})
	if d, _ := p.Check("write_file", in2); d != Allow {
		t.Fatalf("explicit allow rule + README = %v, want Allow", d)
	}
}

func TestAgentConfigGuardBlocksAutoApprove(t *testing.T) {
	p := guardPolicy(SupervisedMode, nil)
	in, _ := json.Marshal(map[string]string{"file_path": "/sa43-proj/.mcp.json"})
	if !p.BlocksAutoApprove("write_file", in) {
		t.Fatal("BlocksAutoApprove must block learned approvals for agent-config writes")
	}
	in2, _ := json.Marshal(map[string]string{"file_path": "/sa43-proj/README.md"})
	if p.BlocksAutoApprove("write_file", in2) {
		t.Fatal("BlocksAutoApprove must not block ordinary writes")
	}
}

func TestAgentConfigGuardMultiFileWritePaths(t *testing.T) {
	in, _ := json.Marshal(map[string]any{
		"files": []map[string]string{
			{"path": "/sa43-proj/README.md", "content": "x"},
			{"path": "/sa43-proj/.mcp.json", "content": "{}"},
		},
	})
	d, err := guardPolicy(BypassMode, nil).Check("multi_file_write", in)
	if err != nil {
		t.Fatalf("Check error: %v", err)
	}
	if d != Ask {
		t.Fatalf("multi_file_write touching .mcp.json = %v, want Ask", d)
	}
}

func TestAgentConfigGuardPlanModeUnchanged(t *testing.T) {
	in, _ := json.Marshal(map[string]string{"file_path": "/sa43-proj/README.md"})
	d, err := guardPolicy(PlanMode, nil).Check("write_file", in)
	if err != nil {
		t.Fatalf("Check error: %v", err)
	}
	if d != Deny {
		t.Fatalf("plan write_file README = %v, want Deny (plan mode denies all writes)", d)
	}
}
