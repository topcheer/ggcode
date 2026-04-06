package hooks

import (
	"bytes"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"

	"github.com/topcheer/ggcode/internal/util"
)

// RunPreHooks runs all matching pre-tool-use hooks.
// Returns HookResult with Allowed=false if any hook blocks execution.
func RunPreHooks(hooks []Hook, env HookEnv) HookResult {
	for _, h := range hooks {
		if !matchTool(h.Match, env.ToolName, env.RawInput) {
			continue
		}
		result := runHook(h.Command, env)
		if !result.Allowed {
			return result
		}
	}
	return HookResult{Allowed: true}
}

// RunPostHooks runs all matching post-tool-use hooks.
// Collects output from hooks with inject_output=true.
func RunPostHooks(hooks []Hook, env HookEnv) HookResult {
	var allOutput strings.Builder
	for _, h := range hooks {
		if !matchTool(h.Match, env.ToolName, env.RawInput) {
			continue
		}
		result := runHook(h.Command, env)
		if result.Err != nil {
			allOutput.WriteString(fmt.Sprintf("[hook error: %v]\n", result.Err))
			continue
		}
		if h.InjectOutput && result.Output != "" {
			allOutput.WriteString(result.Output)
			if !strings.HasSuffix(result.Output, "\n") {
				allOutput.WriteString("\n")
			}
		}
	}
	return HookResult{Allowed: true, Output: allOutput.String()}
}

// runHook executes a shell command and returns the result.
// Exit code 2 means block the tool execution.
// Sensitive data (RAW_INPUT) is passed via environment variable to avoid leaking to process list.
func runHook(command string, env HookEnv) HookResult {
	cmd := os.Expand(command, func(key string) string {
		switch key {
		case "TOOL_NAME":
			return env.ToolName
		case "FILE_PATH":
			return env.FilePath
		case "WORKING_DIR":
			return env.WorkingDir
		case "RAW_INPUT":
			return env.RawInput
		default:
			return os.Getenv(key)
		}
	})

	c, _, err := util.NewShellCommand(cmd)
	if err != nil {
		return HookResult{
			Allowed: true,
			Err:     fmt.Errorf("resolve hook shell: %w", err),
		}
	}
	c.Dir = env.WorkingDir
	// Pass RAW_INPUT via environment variable instead of embedding in command string
	c.Env = append(os.Environ(), "GGCODE_RAW_INPUT="+env.RawInput)
	var stdout, stderr bytes.Buffer
	c.Stdout = &stdout
	c.Stderr = &stderr

	err = c.Run()
	if err != nil {
		if exitErr, ok := err.(*exec.ExitError); ok {
			if exitErr.ExitCode() == 2 {
				errMsg := stderr.String()
				if errMsg == "" {
					errMsg = stdout.String()
				}
				return HookResult{
					Allowed: false,
					Output:  fmt.Sprintf("Blocked by pre-tool-use hook: %s", strings.TrimSpace(errMsg)),
					Err:     err,
				}
			}
		}
		return HookResult{
			Allowed: true,
			Output:  stdout.String(),
			Err:     fmt.Errorf("hook command failed: %w", err),
		}
	}

	return HookResult{Allowed: true, Output: stdout.String()}
}

// ExtractFilePath attempts to extract a file path from common tool argument patterns.
func ExtractFilePath(toolName string, rawInput string) string {
	for _, key := range []string{"file_path", "path", "filename", "file"} {
		idx := strings.Index(rawInput, `"`+key+`"`)
		if idx < 0 {
			continue
		}
		rest := rawInput[idx:]
		colonIdx := strings.Index(rest, ":")
		if colonIdx < 0 {
			continue
		}
		val := strings.TrimSpace(rest[colonIdx+1:])
		val = strings.TrimPrefix(val, `"`)
		val = strings.TrimSuffix(val, `"`)
		if i := strings.Index(val, `"`); i > 0 {
			val = val[:i]
		}
		if val != "" {
			return val
		}
	}
	return ""
}

// matchTool checks if a hook's match pattern applies to a tool call.
func matchTool(pattern, toolName, rawInput string) bool {
	// Function call pattern: tool_name(args...)
	if parenIdx := strings.Index(pattern, "("); parenIdx > 0 {
		patTool := pattern[:parenIdx]
		patArgs := pattern[parenIdx+1 : len(pattern)-1]

		if patTool != toolName {
			return false
		}
		if patArgs == "*" || patArgs == "" {
			return true
		}
		if strings.HasSuffix(patArgs, "*") {
			prefix := strings.TrimSuffix(patArgs, "*")
			return strings.Contains(rawInput, prefix)
		}
		return strings.Contains(rawInput, patArgs)
	}

	// Simple glob match on tool name
	matched, _ := filepath.Match(pattern, toolName)
	if matched {
		return true
	}

	// Pipe-separated patterns
	if strings.Contains(pattern, "|") {
		for _, p := range strings.Split(pattern, "|") {
			p = strings.TrimSpace(p)
			if m, _ := filepath.Match(p, toolName); m {
				return true
			}
		}
	}

	return false
}
