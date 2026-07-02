package tool

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"

	"github.com/topcheer/ggcode/internal/debug"
)

// DebugLogTool lets the LLM inspect recent debug log entries from the
// in-memory ring buffer. This is useful for diagnosing issues with internal
// subsystems (agent loop, provider calls, IM adapters, harness, etc.)
// without needing to read log files from disk.
type DebugLogTool struct{}

func (t DebugLogTool) Name() string { return "debug_log" }

func (t DebugLogTool) Description() string {
	return "Read recent entries from the in-memory debug log ring buffer. Useful for diagnosing internal issues such as provider errors, agent loop behavior, IM adapter problems, or harness failures. The ring buffer captures all debug.Log calls regardless of GGCODE_DEBUG setting. Returns formatted log lines with timestamps."
}

func (t DebugLogTool) Parameters() json.RawMessage {
	return json.RawMessage(`{
		"type": "object",
		"properties": {
			"category": {
				"type": "string",
				"description": "Optional category filter. Matches against category names (e.g. 'agent', 'provider', 'harness', 'openai', 'anthropic', 'tui', 'im') or tags embedded in log messages. Case-insensitive substring match."
			},
			"lines": {
				"type": "integer",
				"description": "Maximum number of recent entries to return (default 50, max 200)."
			},
			"description": {
				"type": "string",
				"description": "REQUIRED. Brief activity label shown in the UI. You MUST always provide this field."
			}
		},
		"required": ["description"]
	}`)
}

func (t DebugLogTool) Execute(ctx context.Context, input json.RawMessage) (Result, error) {
	var args struct {
		Category    string `json:"category"`
		Lines       int    `json:"lines"`
		Description string `json:"description"`
	}
	if err := json.Unmarshal(input, &args); err != nil {
		return Result{IsError: true, Content: fmt.Sprintf("invalid input: %v", err)}, nil
	}

	lines := args.Lines
	if lines <= 0 {
		lines = 50
	}

	category := strings.TrimSpace(args.Category)
	entries := debug.RingHistory(lines, category)

	count, capacity := debug.RingStats()

	if len(entries) == 0 {
		return Result{Content: fmt.Sprintf("No debug log entries found (ring buffer: %d/%d entries). Tips:\n- Set GGCODE_DEBUG=1 to enable file logging\n- The ring buffer captures debug.Log calls regardless of GGCODE_DEBUG\n- Try a different category filter if one was provided", count, capacity)}, nil
	}

	var b strings.Builder
	b.WriteString(fmt.Sprintf("Showing %d of %d entries (ring buffer capacity: %d):\n\n", len(entries), count, capacity))
	for _, e := range entries {
		b.WriteString(fmt.Sprintf("[%s] %s\n", e.Time, e.Message))
	}

	return Result{Content: b.String()}, nil
}
