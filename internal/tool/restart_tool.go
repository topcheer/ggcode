package tool

import (
	"context"
	"encoding/json"
	"fmt"

	"github.com/topcheer/ggcode/internal/debug"
)

// RestartRequester is implemented by the hosting UI (TUI/Desktop) to perform
// a self-restart that PRESERVES the current session (equivalent to the
// /restart slash command: exec the binary with --resume <session-id>).
type RestartRequester interface {
	RequestRestart(debugMode bool)
}

// RestartTool lets the LLM restart the ggcode process after changes that
// only take effect on a fresh process (e.g. binary updates applied by an
// updater, or fixes that alter startup-time behavior). The restart resumes
// the current session, so conversation state is not lost.
//
// The tool only signals the host UI — it never execs the binary itself,
// because the terminal must be released (TUI torn down) before exec.
type RestartTool struct {
	Requester RestartRequester // injected by TUI/Desktop after registration
}

func (t *RestartTool) Name() string { return "restart" }

func (t *RestartTool) Description() string {
	return "Restart the ggcode process itself, preserving the current session (conversation history is resumed automatically after restart). " +
		"Use ONLY when a change requires a fresh process to take effect — e.g. the ggcode binary was updated/rebuilt, or a fix to startup-time behavior (config loading, early initialization) was made. " +
		"Regular code changes in the workspace do NOT need a restart. This should typically be the last tool call in your turn."
}

func (t *RestartTool) Parameters() json.RawMessage {
	return json.RawMessage(`{
		"type": "object",
		"properties": {
			"reason": {
				"type": "string",
				"description": "REQUIRED. Why a restart is needed (e.g. 'binary updated to fix #123'). Shown to the user before the process restarts."
			},
			"debug": {
				"type": "boolean",
				"description": "Restart with GGCODE_DEBUG=1 enabled. Default false."
			}
		},
		"required": ["reason"]
	}`)
}

func (t *RestartTool) Execute(ctx context.Context, input json.RawMessage) (Result, error) {
	var args struct {
		Reason string `json:"reason"`
		Debug  bool   `json:"debug"`
	}
	if err := json.Unmarshal(input, &args); err != nil {
		return Result{IsError: true, Content: fmt.Sprintf("invalid input: %v", err)}, nil
	}
	if t.Requester == nil {
		return Result{IsError: true, Content: "restart is not available in this host (no restart requester injected; use the /restart slash command instead)"}, nil
	}

	debug.Log("restart", "restart tool invoked: reason=%q debug=%v", args.Reason, args.Debug)
	// Signal the host UI. The actual exec happens after the TUI releases the
	// terminal; the process continues serving this tool result until then.
	t.Requester.RequestRestart(args.Debug)
	return Result{Content: fmt.Sprintf("OK: restart requested (%s). The restart fires after this turn completes and its results are persisted; the process will then restart and resume this session automatically.", args.Reason)}, nil
}
