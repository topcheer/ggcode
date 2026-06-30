package hooks

import (
	"strings"
	"testing"
)

func TestMatchTool_SimpleName(t *testing.T) {
	tests := []struct {
		pattern  string
		toolName string
		rawInput string
		want     bool
	}{
		{"write_file", "write_file", `{}`, true},
		{"write_file", "read_file", `{}`, false},
		{"write_*", "write_file", `{}`, true},
		{"write_*", "write_dir", `{}`, true},
		{"run_command", "run_command", `{}`, true},
		// Function call pattern
		{"run_command(git commit *)", "run_command", `{"command":"git commit -m test"}`, true},
		{"run_command(git commit *)", "run_command", `{"command":"ls"}`, false},
		{"run_command(*)", "run_command", `{"command":"anything"}`, true},
		{"run_command()", "run_command", `{}`, true},
		// Pipe-separated
		{"write_file|edit_file", "write_file", `{}`, true},
		{"write_file|edit_file", "edit_file", `{}`, true},
		{"write_file|edit_file", "read_file", `{}`, false},
	}

	for _, tt := range tests {
		got := matchTool(tt.pattern, tt.toolName, tt.rawInput)
		if got != tt.want {
			t.Errorf("matchTool(%q, %q, %q) = %v, want %v", tt.pattern, tt.toolName, tt.rawInput, got, tt.want)
		}
	}
}

func TestExtractFilePath(t *testing.T) {
	tests := []struct {
		toolName string
		rawInput string
		want     string
	}{
		{"write_file", `{"file_path":"/tmp/test.go","content":"hello"}`, "/tmp/test.go"},
		{"read_file", `{"path":"src/main.go"}`, "src/main.go"},
		{"edit_file", `{"file":"/etc/config.yaml"}`, "/etc/config.yaml"},
		{"run_command", `{"command":"ls -la"}`, ""},
	}

	for _, tt := range tests {
		got := ExtractFilePath(tt.toolName, tt.rawInput)
		if got != tt.want {
			t.Errorf("ExtractFilePath(%q, %q) = %q, want %q", tt.toolName, tt.rawInput, got, tt.want)
		}
	}
}

func TestRunPreHooks_BlockOnExit2(t *testing.T) {
	h := Hook{
		Match:   "run_command(rm *)",
		Command: "exit 2",
	}
	env := HookEnv{ToolName: "run_command", RawInput: `{"command":"rm -rf /tmp/test"}`}
	result := RunPreHooks([]Hook{h}, env)
	if result.Allowed {
		t.Error("expected pre-hook to block execution")
	}
}

func TestRunPreHooks_PassThrough(t *testing.T) {
	h := Hook{
		Match:   "write_file",
		Command: "echo 'running pre hook'",
	}
	env := HookEnv{ToolName: "write_file", RawInput: `{}`}
	result := RunPreHooks([]Hook{h}, env)
	if !result.Allowed {
		t.Error("expected pre-hook to allow execution")
	}
}

func TestRunPreHooks_NoMatch(t *testing.T) {
	h := Hook{
		Match:   "write_file",
		Command: "exit 2",
	}
	env := HookEnv{ToolName: "read_file", RawInput: `{}`}
	result := RunPreHooks([]Hook{h}, env)
	if !result.Allowed {
		t.Error("expected no blocking when hook doesn't match")
	}
}

func TestRunPostHooks_InjectOutput(t *testing.T) {
	h := Hook{
		Match:        "write_file",
		Command:      "echo 'formatted successfully'",
		InjectOutput: true,
	}
	env := HookEnv{ToolName: "write_file", RawInput: `{}`}
	result := RunPostHooks([]Hook{h}, env)
	if !result.Allowed {
		t.Error("expected post-hook to allow")
	}
	if result.Output == "" {
		t.Error("expected post-hook to inject output")
	}
}

func TestRunPostHooks_NoInject(t *testing.T) {
	h := Hook{
		Match:        "write_file",
		Command:      "echo 'formatted successfully'",
		InjectOutput: false,
	}
	env := HookEnv{ToolName: "write_file", RawInput: `{}`}
	result := RunPostHooks([]Hook{h}, env)
	if result.Output != "" {
		t.Errorf("expected no output injection, got %q", result.Output)
	}
}

func TestRunPostHooks_ToolResultFields(t *testing.T) {
	h := Hook{
		Match:        "run_command",
		Command:      "echo \"${TOOL_SUCCESS}|${TOOL_ERROR}|${TOOL_DURATION}\"",
		InjectOutput: true,
	}
	env := HookEnv{
		ToolName:     "run_command",
		RawInput:     `{}`,
		ToolSuccess:  false,
		ToolError:    "command not found",
		ToolDuration: "5ms",
	}
	result := RunPostHooks([]Hook{h}, env)
	if !strings.Contains(result.Output, "false") {
		t.Errorf("expected TOOL_SUCCESS=false in output, got %q", result.Output)
	}
	if !strings.Contains(result.Output, "command not found") {
		t.Errorf("expected TOOL_ERROR in output, got %q", result.Output)
	}
	if !strings.Contains(result.Output, "5ms") {
		t.Errorf("expected TOOL_DURATION in output, got %q", result.Output)
	}
}

func TestRunPostHooks_EnvVars(t *testing.T) {
	h := Hook{
		Match:        "run_command",
		Command:      "echo $GGCODE_TOOL_SUCCESS:$GGCODE_TOOL_NAME",
		InjectOutput: true,
	}
	env := HookEnv{
		ToolName:    "run_command",
		RawInput:    `{}`,
		ToolSuccess: true,
	}
	result := RunPostHooks([]Hook{h}, env)
	if !strings.Contains(result.Output, "true") {
		t.Errorf("expected GGCODE_TOOL_SUCCESS=true in output, got %q", result.Output)
	}
	if !strings.Contains(result.Output, "run_command") {
		t.Errorf("expected GGCODE_TOOL_NAME in output, got %q", result.Output)
	}
}

func TestRunPreHooks_MultipleHooks(t *testing.T) {
	hooks := []Hook{
		{Match: "write_file", Command: "echo first"},
		{Match: "write_file", Command: "exit 2"},
		{Match: "write_file", Command: "echo should-not-run"},
	}
	env := HookEnv{ToolName: "write_file", RawInput: `{}`}
	result := RunPreHooks(hooks, env)
	if result.Allowed {
		t.Error("expected second hook to block execution")
	}
}

func TestRunPostHooks_MultipleInject(t *testing.T) {
	hooks := []Hook{
		{Match: "write_file", Command: "echo first", InjectOutput: true},
		{Match: "write_file", Command: "echo second", InjectOutput: true},
		{Match: "write_file", Command: "echo no-inject", InjectOutput: false},
	}
	env := HookEnv{ToolName: "write_file", RawInput: `{}`}
	result := RunPostHooks(hooks, env)
	if !strings.Contains(result.Output, "first") {
		t.Errorf("expected 'first' in output, got %q", result.Output)
	}
	if !strings.Contains(result.Output, "second") {
		t.Errorf("expected 'second' in output, got %q", result.Output)
	}
	if strings.Contains(result.Output, "no-inject") {
		t.Errorf("expected 'no-inject' to NOT be in output, got %q", result.Output)
	}
}
