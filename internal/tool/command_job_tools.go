package tool

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"time"

	"github.com/topcheer/ggcode/internal/debug"
	"github.com/topcheer/ggcode/internal/permission"
)

type StartCommandTool struct {
	Manager *CommandJobManager
	Policy  permission.PermissionPolicy
	// OutputTee, if non-nil, receives a copy of stdout/stderr in real time.
	// Used by the TUI to mirror command output to a tmux command pane.
	OutputTee  io.Writer
	OnPreExec  func(command, description string)
	OnPostExec func(exitCode int, err error)
}

func (t StartCommandTool) Name() string { return "start_command" }

func (t StartCommandTool) Description() string {
	return "Start a shell command in the background for long-running, streaming, or interactive commands. Prefer run_command for quick one-shot commands. Use read_command_output/wait_command with the returned job_id to monitor, write_command_input for stdin, stop_command to cancel. Defaults to 30-minute timeout. Set detach=true for long-running services (dev servers, watchers) with no timeout."
}

func (t StartCommandTool) Parameters() json.RawMessage {
	return json.RawMessage(`{
	"type": "object",
	"properties": {
		"command": {
			"type": "string",
			"description": "Shell command to execute in the background. Start with a '# ' comment line describing its purpose (shown as activity label in the UI)."
		},
		"description": {
			"type": "string",
			"description": "REQUIRED. Brief activity label shown in the UI. Write in the user's language (e.g. 'Searching for TODO patterns', '检查构建配置'). You MUST always provide this field."
		},
		"timeout": {
			"type": "integer",
			"description": "Timeout in seconds before the job is cancelled (default: 1800). Ignored when detach=true."
		},
		"detach": {
			"type": "boolean",
			"description": "When true, the command runs with no timeout — it will not be killed until it exits naturally or stop_command is called. Use this for long-running services like dev servers, file watchers, or daemons. Default: false."
		}
	},
	"required": [
		"command",
		"description"
	]
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
		Detach  bool   `json:"detach"`
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

	if t.OnPreExec != nil {
		t.OnPreExec(args.Command, "")
	}
	if t.OutputTee != nil {
		t.Manager.SetOutputTee(t.OutputTee)
		defer t.Manager.SetOutputTee(nil)
	}
	snap, err := t.Manager.Start(ctx, args.Command, args.Detach, secondsToDuration(args.Timeout, defaultCommandTimeout))
	if err != nil {
		if t.OnPostExec != nil {
			t.OnPostExec(-1, err)
		}
		return Result{IsError: true, Content: err.Error()}, nil
	}
	// start_command is async — we don't know the exit code yet.
	// OnPostExec will be called by the caller via read_command_output/wait_command
	// when the job completes, not here.
	return Result{Content: formatCommandJobSnapshot(*snap, false)}, nil
}

type ReadCommandOutputTool struct {
	Manager *CommandJobManager
}

func (t ReadCommandOutputTool) Name() string { return "read_command_output" }

func (t ReadCommandOutputTool) Description() string {
	return "Read recent output from a background command job. Use since_line as the last Total lines value you have already seen to avoid duplicate lines; tail_lines still caps the returned output."
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
			"description": "How many recent lines to return (default: 20). This cap also applies when since_line is set."
		},
		"since_line": {
			"type": "integer",
			"description": "The last 1-based Total lines value you have already seen; only lines after this are returned. Use 0 to read from the beginning/tail."
		},
		"description": {
			"type": "string",
			"description": "REQUIRED. Brief activity label shown in the UI. Write in the user's language (e.g. 'Searching for TODO patterns', '检查构建配置'). You MUST always provide this field."
		}
	},
	"required": [
		"job_id",
		"description"
	]
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
	return "Wait briefly for a background command job and then return its current status plus recent output. Use since_line as the last Total lines value already seen to poll incrementally."
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
			"description": "How many recent lines to return (default: 20). This cap also applies when since_line is set."
		},
		"since_line": {
			"type": "integer",
			"description": "The last 1-based Total lines value you have already seen; only lines after this are returned. Use 0 to read from the beginning/tail."
		},
		"description": {
			"type": "string",
			"description": "REQUIRED. Brief activity label shown in the UI. Write in the user's language (e.g. 'Searching for TODO patterns', '检查构建配置'). You MUST always provide this field."
		}
	},
	"required": [
		"job_id",
		"description"
	]
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
	return "Stop a running background command job by job_id. Stopping an already completed or unknown job returns an error."
}

func (t StopCommandTool) Parameters() json.RawMessage {
	return json.RawMessage(`{
	"type": "object",
	"properties": {
		"job_id": {
			"type": "string",
			"description": "The command job ID returned by start_command"
		},
		"description": {
			"type": "string",
			"description": "REQUIRED. Brief activity label shown in the UI. Write in the user's language (e.g. 'Searching for TODO patterns', '检查构建配置'). You MUST always provide this field."
		}
	},
	"required": [
		"job_id",
		"description"
	]
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
	return "Send stdin input to a running background command job by job_id. Use this for prompts, REPLs, or interactive commands; it does not start a new command. Writing to a completed job returns an error."
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
			"description": "Text to write to the command's stdin. This is input for an existing running job, not a new shell command."
		},
		"append_newline": {
			"type": "boolean",
			"description": "Whether to append a trailing newline after the input (default: true)"
		},
		"description": {
			"type": "string",
			"description": "REQUIRED. Brief activity label shown in the UI. Write in the user's language (e.g. 'Searching for TODO patterns', '检查构建配置'). You MUST always provide this field."
		}
	},
	"required": [
		"job_id",
		"input",
		"description"
	]
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
	return "List background command jobs and their current status, including completed jobs retained in this session."
}

func (t ListCommandsTool) Parameters() json.RawMessage {
	return json.RawMessage(`{
	"type": "object",
	"properties": {
		"description": {
			"type": "string",
			"description": "REQUIRED. Brief activity label shown in the UI. Write in the user's language (e.g. 'Searching for TODO patterns', '检查构建配置'). You MUST always provide this field."
		}
	},
	"required": [
		"description"
	]
}`)
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
