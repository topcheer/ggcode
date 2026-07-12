package tool

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/topcheer/ggcode/internal/debug"
)

// DebugLogTool lets the LLM inspect recent debug log entries from the
// in-memory ring buffer and optionally export them to a temp file.
// This is useful for diagnosing issues with internal subsystems (agent loop,
// provider calls, IM adapters, harness, etc.) without needing to read log
// files from disk.
type DebugLogTool struct{}

func (t DebugLogTool) Name() string { return "debug_log" }

func (t DebugLogTool) Description() string {
	return "Read recent entries from the in-memory debug log ring buffer, or export them to a temp file. Useful for diagnosing internal issues such as provider errors, agent loop behavior, IM adapter problems, or harness failures. The ring buffer captures all debug.Log calls regardless of GGCODE_DEBUG setting. Use action=\"export\" to write filtered logs to a temp file for sharing or archival. Returns formatted log lines with timestamps."
}

func (t DebugLogTool) Parameters() json.RawMessage {
	return json.RawMessage(`{
		"type": "object",
		"properties": {
			"action": {
				"type": "string",
				"enum": ["read", "export"],
				"description": "\"read\" (default): return log entries inline. \"export\": write filtered logs to a temp file and return the file path."
			},
			"category": {
				"type": "string",
				"description": "Optional category filter. Matches against category names (e.g. 'agent', 'provider', 'harness', 'openai', 'anthropic', 'tui', 'im') or tags embedded in log messages. Case-insensitive substring match."
			},
			"keyword": {
				"type": "string",
				"description": "Optional keyword filter for export mode. Only exports log lines containing this substring (case-insensitive). Useful for extracting all entries related to a specific topic (e.g. 'cancel', 'timeout', 'deadlock')."
			},
			"lines": {
				"type": "integer",
				"description": "Maximum number of recent entries to return or export (default 50, max 2000 for export, max 200 for read)."
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
		Action      string `json:"action"`
		Category    string `json:"category"`
		Keyword     string `json:"keyword"`
		Lines       int    `json:"lines"`
		Description string `json:"description"`
	}
	if err := json.Unmarshal(input, &args); err != nil {
		return Result{IsError: true, Content: fmt.Sprintf("invalid input: %v", err)}, nil
	}

	action := strings.TrimSpace(args.Action)
	if action == "" {
		action = "read"
	}

	category := strings.TrimSpace(args.Category)
	keyword := strings.TrimSpace(args.Keyword)

	lines := args.Lines
	if lines <= 0 {
		lines = 50
	}

	if action == "export" {
		return t.doExport(lines, category, keyword)
	}
	return t.doRead(lines, category)
}

func (t DebugLogTool) doRead(lines int, category string) (Result, error) {
	if lines > 200 {
		lines = 200
	}
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

func (t DebugLogTool) doExport(lines int, category, keyword string) (Result, error) {
	// For export, allow up to 2000 lines
	if lines > 2000 {
		lines = 2000
	}

	// Get all entries (bypass the 200 limit by calling RingHistoryMax)
	entries := debug.RingHistoryMax(lines, category)
	count, capacity := debug.RingStats()

	if len(entries) == 0 {
		return Result{Content: fmt.Sprintf("No debug log entries to export (ring buffer: %d/%d entries).", count, capacity)}, nil
	}

	// Apply keyword filter if provided
	filtered := entries
	if keyword != "" {
		kwLower := strings.ToLower(keyword)
		filtered = filtered[:0]
		for _, e := range entries {
			if strings.Contains(strings.ToLower(e.Message), kwLower) ||
				strings.Contains(strings.ToLower(e.Category), kwLower) {
				filtered = append(filtered, e)
			}
		}
	}

	if len(filtered) == 0 {
		return Result{Content: fmt.Sprintf("No entries matched filters (category=%q keyword=%q). Total in buffer: %d/%d", category, keyword, count, capacity)}, nil
	}

	// Write to temp file
	ts := time.Now().Format("20060102-150405")
	filename := fmt.Sprintf("ggcode-debug-%s", ts)
	if category != "" {
		filename += "-" + category
	}
	if keyword != "" {
		filename += "-" + keyword
	}
	filename += ".log"

	tmpDir := os.TempDir()
	path := filepath.Join(tmpDir, filename)

	f, err := os.Create(path)
	if err != nil {
		return Result{IsError: true, Content: fmt.Sprintf("failed to create temp file: %v", err)}, nil
	}
	defer f.Close()

	// Write header
	header := fmt.Sprintf("# ggcode debug log export\n# Time: %s\n# Category filter: %q\n# Keyword filter: %q\n# Entries: %d (of %d in buffer, capacity %d)\n\n",
		time.Now().Format("2006-01-02 15:04:05 MST"),
		category, keyword, len(filtered), count, capacity)
	f.WriteString(header)

	for _, e := range filtered {
		f.WriteString(fmt.Sprintf("[%s] [%s] %s\n", e.Time, e.Category, e.Message))
	}

	return Result{Content: fmt.Sprintf("Exported %d log entries (category=%q keyword=%q) to:\n%s\n\nBuffer stats: %d/%d entries used.", len(filtered), category, keyword, path, count, capacity)}, nil
}
