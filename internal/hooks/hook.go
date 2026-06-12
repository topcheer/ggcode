package hooks

// Hook represents a single pre or post tool-use hook.
type Hook struct {
	Match        string `yaml:"match"`         // glob pattern to match tool names
	Command      string `yaml:"command"`       // shell command to run
	InjectOutput bool   `yaml:"inject_output"` // for post hooks: inject stdout into tool result
}

// HookConfig holds all hooks from configuration.
type HookConfig struct {
	PreToolUse  []Hook `yaml:"pre_tool_use"`
	PostToolUse []Hook `yaml:"post_tool_use"`
}

// HookResult is the result of running a hook.
type HookResult struct {
	Allowed bool   // false means block the tool execution (pre-hook only)
	Output  string // captured stdout (for inject_output)
	Err     error
}

// HookEnv holds environment variables available to hook commands.
type HookEnv struct {
	ToolName   string
	FilePath   string // extracted from tool arguments when applicable
	WorkingDir string
	RawInput   string // raw JSON tool arguments
}
