package tool

import (
	"context"
	"encoding/json"
	"fmt"
	"os/exec"
	"strconv"
	"strings"
)

// GitLog implements the git_log tool.
type GitLog struct{}

func (t GitLog) Name() string { return "git_log" }

func (t GitLog) Description() string {
	return "Show git commit history in oneline format."
}

func (t GitLog) Parameters() json.RawMessage {
	return json.RawMessage(`{
		"type": "object",
		"properties": {
			"path": {
				"type": "string",
				"description": "Repository path (default: current directory)"
			},
			"count": {
				"type": "integer",
				"description": "Number of commits to show (default: 10)"
			}
		}
	}`)
}

func (t GitLog) Execute(ctx context.Context, input json.RawMessage) (Result, error) {
	var args struct {
		Path  string `json:"path"`
		Count int    `json:"count"`
	}
	if err := json.Unmarshal(input, &args); err != nil {
		return Result{IsError: true, Content: fmt.Sprintf("invalid input: %v", err)}, nil
	}

	if args.Count <= 0 || args.Count > 100 {
		args.Count = 10
	}

	cmd := exec.CommandContext(ctx, "git", "log", "--oneline", "-"+strconv.Itoa(args.Count))
	cmd.Dir = args.Path

	out, err := cmd.CombinedOutput()
	if err != nil {
		return Result{IsError: true, Content: fmt.Sprintf("git log failed: %v\n%s", err, out)}, nil
	}

	trimmed := strings.TrimSpace(string(out))
	if trimmed == "" {
		return Result{Content: "No commits found."}, nil
	}

	return Result{Content: trimmed}, nil
}
