package tool

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"

	"github.com/topcheer/ggcode/internal/debug"
)

type RestartRequester interface {
	RequestRestart(reason string, debugMode bool)
}

type RestartTool struct {
	Requester RestartRequester
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

func (t *RestartTool) Available() bool { return t.Requester != nil }

func (t *RestartTool) Execute(ctx context.Context, input json.RawMessage) (Result, error) {
	var args struct {
		Reason string `json:"reason"`
		Debug  bool   `json:"debug"`
	}
	if err := json.Unmarshal(input, &args); err != nil {
		return Result{IsError: true, Content: fmt.Sprintf("invalid input: %v", err)}, nil
	}
	if t.Requester == nil {
		return Result{IsError: true, Content: "restart is not available in this host — no restart requester was injected. Ask the user to restart the process manually if a restart is truly required."}, nil
	}

	if strings.TrimSpace(args.Reason) == "" {
		return Result{IsError: true, Content: "reason is REQUIRED: explain why the restart is needed (e.g. 'binary updated to fix #123') - it is shown to the user before the process restarts."}, nil
	}
	debug.Log("restart", "restart tool invoked: reason=%q debug=%v", args.Reason, args.Debug)

	t.Requester.RequestRestart(args.Reason, args.Debug)
	return Result{Content: "OK: restart armed. The process restarts as soon as this turn finishes (with a short fallback timeout). Do NOT issue any further tool calls — end your turn now. The session resumes automatically after restart."}, nil
}
