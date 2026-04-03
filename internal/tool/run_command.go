package tool

import (
	"bytes"
	"context"
	"strings"
	"encoding/json"
	"fmt"
	"os/exec"
	"time"
)

const (
	maxOutputSize = 100 * 1024 // 100KB
)

// RunCommand implements the run_command tool for executing shell commands.
type RunCommand struct {
	// WorkingDir is the fixed working directory set by the agent.
	// LLM-provided working_dir is ignored to prevent sandbox escape.
	WorkingDir string
}

func (t RunCommand) Name() string { return "run_command" }

func (t RunCommand) Description() string {
	return "Execute a shell command and return stdout/stderr. Has a 30-second timeout by default."
}

func (t RunCommand) Parameters() json.RawMessage {
	return json.RawMessage(`{
		"type": "object",
		"properties": {
			"command": {
				"type": "string",
				"description": "Shell command to execute"
			},
			"working_dir": {
				"type": "string",
				"description": "Working directory for the command (default: current directory)"
			},
			"timeout": {
				"type": "integer",
				"description": "Timeout in seconds (default: 30)"
			}
		},
		"required": ["command"]
	}`)
}

func (t RunCommand) Execute(ctx context.Context, input json.RawMessage) (Result, error) {
	var args struct {
		Command    string `json:"command"`
		WorkingDir string `json:"working_dir"`
		Timeout    int    `json:"timeout"`
	}
	if err := json.Unmarshal(input, &args); err != nil {
		return Result{IsError: true, Content: fmt.Sprintf("invalid input: %v", err)}, nil
	}

	if args.Timeout <= 0 {
		args.Timeout = 30
	}

	timeoutCtx, cancel := context.WithTimeout(ctx, time.Duration(args.Timeout)*time.Second)
	defer cancel()

	cmd := exec.CommandContext(timeoutCtx, "sh", "-c", args.Command)
	// Use the fixed WorkingDir from agent, ignore LLM-provided working_dir
	if t.WorkingDir != "" {
		cmd.Dir = t.WorkingDir
	}

	var stdout, stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr

	err := cmd.Run()

	output := stdout.String()
	errOutput := stderr.String()

	// Truncate output if too large
	if len(output) > maxOutputSize {
		output = output[:maxOutputSize] + "\n... [output truncated]"
	}
	if len(errOutput) > maxOutputSize {
		errOutput = errOutput[:maxOutputSize] + "\n... [stderr truncated]"
	}

	var sb strings.Builder
	if output != "" {
		sb.WriteString(output)
	}
	if errOutput != "" {
		if sb.Len() > 0 {
			sb.WriteString("\n")
		}
		sb.WriteString("STDERR:\n")
		sb.WriteString(errOutput)
	}

	if err != nil {
		return Result{IsError: true, Content: fmt.Sprintf("%s\nCommand failed: %v", sb.String(), err)}, nil
	}

	if sb.Len() == 0 {
		return Result{Content: "Command completed with no output."}, nil
	}

	return Result{Content: sb.String()}, nil
}
