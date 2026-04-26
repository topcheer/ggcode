package tool

import (
	"context"
	"encoding/json"
	"fmt"
	"time"

	"github.com/topcheer/ggcode/internal/debug"
	"github.com/topcheer/ggcode/internal/permission"
)

type StartCommandTool struct {
	Manager *CommandJobManager
	Policy  permission.PermissionPolicy
}

func (t StartCommandTool) Name() string { return "start_command" }

func (t StartCommandTool) Description() string {
	return "Start a shell command in the background. Use read_command_output or wait_command to monitor progress. Defaults to a 30-minute timeout."
}

func (t StartCommandTool) Parameters() json.RawMessage {
	return json.RawMessage(`{
		"type": "object",
		"properties": {
			"command": {
				"type": "string",
				"description": "Shell command to execute in the background"
			},
			"timeout": {
				"type": "integer",
				"description": "Timeout in seconds before the job is cancelled (default: 1800)"
			}
		},
		"required": ["command"]
	}`)
}

// isBypassMode returns true when the permission policy allows
// automatic execution of Ask-level commands (Bypass or Autopilot).
func (t StartCommandTool) isBypassMode() bool {
	if t.Policy == nil {
		return false
	}
	m := t.Policy.Mode()
	return m == permission.BypassMode || m == permission.AutopilotMode
}

func (t StartCommandTool) Execute(ctx context.Context, input json.RawMessage) (Result, error) {
	var args struct {
		Command string `json:"command"`
		Timeout int    `json:"timeout"`
	}
	if err := json.Unmarshal(input, &args); err != nil {
		return Result{IsError: true, Content: fmt.Sprintf("invalid input: %v", err)}, nil
	}
	if t.Manager == nil {
		return Result{IsError: true, Content: "command job manager is unavailable"}, nil
	}

	// === Safety gate (Allow/Ask/Block) ===
	gate := NewCommandGate()
	gateResult := gate.Check(args.Command)
	if gateResult.IsBlocked() {
		return Result{IsError: true, Content: gateResult.Reason}, nil
	}
	if gateResult.NeedsConfirmation() {
		if t.isBypassMode() {
			debug.Log("command-gate", "ASK→ALLOW (bypass mode): %s", gateResult.Reason)
		} else {
			// In non-bypass mode, treat Ask as Block for background jobs.
			return Result{IsError: true, Content: "Command requires confirmation: " + gateResult.Reason}, nil
		}
	}
	if len(gateResult.Warnings) > 0 {
		debug.Log("command-gate", "warnings: %v", gateResult.Warnings)
	}
	if gateResult.CleanedCmd != "" {
		args.Command = gateResult.CleanedCmd
	}

	snap, err := t.Manager.Start(ctx, args.Command, secondsToDuration(args.Timeout, defaultCommandTimeout))
	if err != nil {
		return Result{IsError: true, Content: err.Error()}, nil
	}
	return Result{Content: formatCommandJobSnapshot(*snap, false)}, nil
}

type ReadCommandOutputTool struct {
	Manager *CommandJobManager
}

func (t ReadCommandOutputTool) Name() string { return "read_command_output" }

func (t ReadCommandOutputTool) Description() string {
	return "Read recent output from a background command job."
}

func (t ReadCommandOutputTool) Parameters() json.RawMessage {
	return json.RawMessage(`{
		"type": "object",
		"properties": {
			"job_id": {
				"type": "string",
				"description": "The command job ID returned by start_command"
			},
			"tail_lines": {
				"type": "integer",
				"description": "How many recent lines to return (default: 20)"
			},
			"since_line": {
				"type": "integer",
				"description": "Only return lines after this 1-based line number"
			}
		},
		"required": ["job_id"]
	}`)
}

func (t ReadCommandOutputTool) Execute(ctx context.Context, input json.RawMessage) (Result, error) {
	var args struct {
		JobID     string `json:"job_id"`
		TailLines int    `json:"tail_lines"`
		SinceLine int    `json:"since_line"`
	}
	if err := json.Unmarshal(input, &args); err != nil {
		return Result{IsError: true, Content: fmt.Sprintf("invalid input: %v", err)}, nil
	}
	if t.Manager == nil {
		return Result{IsError: true, Content: "command job manager is unavailable"}, nil
	}
	snap, err := t.Manager.Read(args.JobID, args.TailLines, args.SinceLine)
	if err != nil {
		return Result{IsError: true, Content: err.Error()}, nil
	}
	return Result{Content: formatCommandJobSnapshot(snap, true)}, nil
}

type WaitCommandTool struct {
	Manager *CommandJobManager
}

func (t WaitCommandTool) Name() string { return "wait_command" }

func (t WaitCommandTool) Description() string {
	return "Wait briefly for a background command job and then return its current status plus recent output."
}

func (t WaitCommandTool) Parameters() json.RawMessage {
	return json.RawMessage(`{
		"type": "object",
		"properties": {
			"job_id": {
				"type": "string",
				"description": "The command job ID returned by start_command"
			},
			"wait_seconds": {
				"type": "integer",
				"description": "How long to wait before returning (default: 30)"
			},
			"tail_lines": {
				"type": "integer",
				"description": "How many recent lines to return (default: 20)"
			},
			"since_line": {
				"type": "integer",
				"description": "Only return lines after this 1-based line number"
			}
		},
		"required": ["job_id"]
	}`)
}

func (t WaitCommandTool) Execute(ctx context.Context, input json.RawMessage) (Result, error) {
	var args struct {
		JobID       string `json:"job_id"`
		WaitSeconds int    `json:"wait_seconds"`
		TailLines   int    `json:"tail_lines"`
		SinceLine   int    `json:"since_line"`
	}
	if err := json.Unmarshal(input, &args); err != nil {
		return Result{IsError: true, Content: fmt.Sprintf("invalid input: %v", err)}, nil
	}
	if t.Manager == nil {
		return Result{IsError: true, Content: "command job manager is unavailable"}, nil
	}
	wait := secondsToDuration(args.WaitSeconds, 30*time.Second)
	snap, err := t.Manager.Wait(ctx, args.JobID, wait, args.TailLines, args.SinceLine)
	if err != nil {
		return Result{IsError: true, Content: err.Error()}, nil
	}
	return Result{Content: formatCommandJobSnapshot(snap, true)}, nil
}

type StopCommandTool struct {
	Manager *CommandJobManager
}

func (t StopCommandTool) Name() string { return "stop_command" }

func (t StopCommandTool) Description() string {
	return "Stop a running background command job."
}

func (t StopCommandTool) Parameters() json.RawMessage {
	return json.RawMessage(`{
		"type": "object",
		"properties": {
			"job_id": {
				"type": "string",
				"description": "The command job ID returned by start_command"
			}
		},
		"required": ["job_id"]
	}`)
}

func (t StopCommandTool) Execute(ctx context.Context, input json.RawMessage) (Result, error) {
	var args struct {
		JobID string `json:"job_id"`
	}
	if err := json.Unmarshal(input, &args); err != nil {
		return Result{IsError: true, Content: fmt.Sprintf("invalid input: %v", err)}, nil
	}
	if t.Manager == nil {
		return Result{IsError: true, Content: "command job manager is unavailable"}, nil
	}
	snap, err := t.Manager.Stop(args.JobID)
	if err != nil {
		return Result{IsError: true, Content: err.Error()}, nil
	}
	return Result{Content: formatCommandJobSnapshot(snap, false)}, nil
}

type WriteCommandInputTool struct {
	Manager *CommandJobManager
}

func (t WriteCommandInputTool) Name() string { return "write_command_input" }

func (t WriteCommandInputTool) Description() string {
	return "Send stdin input to a running background command job. Use this for prompts, REPLs, or interactive commands."
}

func (t WriteCommandInputTool) Parameters() json.RawMessage {
	return json.RawMessage(`{
		"type": "object",
		"properties": {
			"job_id": {
				"type": "string",
				"description": "The command job ID returned by start_command"
			},
			"input": {
				"type": "string",
				"description": "Text to write to the command's stdin"
			},
			"append_newline": {
				"type": "boolean",
				"description": "Whether to append a trailing newline after the input (default: true)"
			}
		},
		"required": ["job_id", "input"]
	}`)
}

func (t WriteCommandInputTool) Execute(ctx context.Context, input json.RawMessage) (Result, error) {
	var args struct {
		JobID         string `json:"job_id"`
		Input         string `json:"input"`
		AppendNewline *bool  `json:"append_newline"`
	}
	if err := json.Unmarshal(input, &args); err != nil {
		return Result{IsError: true, Content: fmt.Sprintf("invalid input: %v", err)}, nil
	}
	if t.Manager == nil {
		return Result{IsError: true, Content: "command job manager is unavailable"}, nil
	}
	appendNewline := true
	if args.AppendNewline != nil {
		appendNewline = *args.AppendNewline
	}
	snap, err := t.Manager.Write(args.JobID, args.Input, appendNewline)
	if err != nil {
		return Result{IsError: true, Content: err.Error()}, nil
	}
	return Result{Content: formatCommandJobSnapshot(snap, false)}, nil
}

type ListCommandsTool struct {
	Manager *CommandJobManager
}

func (t ListCommandsTool) Name() string { return "list_commands" }

func (t ListCommandsTool) Description() string {
	return "List background command jobs and their current status."
}

func (t ListCommandsTool) Parameters() json.RawMessage {
	return json.RawMessage(`{"type":"object","properties":{}}`)
}

func (t ListCommandsTool) Execute(ctx context.Context, input json.RawMessage) (Result, error) {
	if t.Manager == nil {
		return Result{IsError: true, Content: "command job manager is unavailable"}, nil
	}
	jobs := t.Manager.List()
	if len(jobs) == 0 {
		return Result{Content: "No command jobs have been started."}, nil
	}
	content := ""
	for i, job := range jobs {
		if i > 0 {
			content += "\n\n"
		}
		content += formatCommandJobSnapshot(job, false)
	}
	return Result{Content: content}, nil
}

func secondsToDuration(seconds int, fallback time.Duration) time.Duration {
	if seconds <= 0 {
		return fallback
	}
	return time.Duration(seconds) * time.Second
}
