package tui

// Session Resume Recap — Contextual summary shown when resuming a session.
//
// Research basis: When users resume a past session in AI coding agents, they
// often have no idea what was discussed or accomplished. Claude Code shows a
// brief recap of the session state. Cursor restores the full chat but gives
// no quick summary. Aider shows a diff summary from git.
//
// Gap: ggcode rebuilt the full conversation view on resume, but users had to
// scroll through potentially hundreds of messages to understand context. This
// is especially painful for sessions resumed days or weeks later.
//
// Solution: Generate a zero-cost (no LLM call) recap from session metadata:
//   - Session title and age (when it was last active)
//   - Message count (user turns + total)
//   - Files touched (extracted from tool calls in message history)
//   - Cumulative token usage and estimated cost
//   - Last user message preview (truncated)
//
// The recap appears as a system message at the top of the restored chat,
// giving users immediate context without scrolling.

import (
	"encoding/json"
	"fmt"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/topcheer/ggcode/internal/cost"

	"github.com/topcheer/ggcode/internal/session"
)

// sessionRecap builds a concise summary string for a resumed session.
// Returns empty string if the session has no meaningful content to summarize.
func sessionRecap(ses *session.Session, now time.Time) string {
	if ses == nil || len(ses.Messages) == 0 {
		return ""
	}

	// Count user vs assistant messages.
	userMsgs := 0
	var fileSet map[string]bool
	for _, msg := range ses.Messages {
		if msg.Role == "user" {
			userMsgs++
		}
		// Extract file paths from tool_use blocks in content to list touched files.
		for _, block := range msg.Content {
			if block.Type != "tool_use" {
				continue
			}
			if fileSet == nil {
				fileSet = make(map[string]bool)
			}
			for _, f := range extractFilePathFromInput(block.Input) {
				fileSet[f] = true
			}
		}
	}

	if userMsgs == 0 {
		return ""
	}

	var parts []string

	// Session age.
	age := formatSessionAge(ses.UpdatedAt, now)
	title := ses.Title
	if title == "" {
		title = "Untitled"
	}
	parts = append(parts, fmt.Sprintf("**%s** (last active %s)", title, age))

	// Message count.
	totalMsgs := len(ses.Messages)
	parts = append(parts, fmt.Sprintf("%d messages (%d user turns)", totalMsgs, userMsgs))

	// Token usage.
	usage := ses.TokenUsage
	if usage.InputTokens > 0 || usage.OutputTokens > 0 {
		totalTok := usage.InputTokens + usage.OutputTokens
		tokStr := fmt.Sprintf("%s tokens (in: %s, out: %s)",
			formatK(totalTok), formatK(usage.InputTokens), formatK(usage.OutputTokens))

		// Try to get cost from session's CostJSON.
		costStr := sessionCostString(ses)
		if costStr != "" {
			tokStr += ", cost: " + costStr
		}
		parts = append(parts, tokStr)
	}

	// Files touched (deduplicated, sorted, limited to 5).
	if len(fileSet) > 0 {
		files := make([]string, 0, len(fileSet))
		for f := range fileSet {
			files = append(files, f)
		}
		sortStrings(files)
		displayCount := len(files)
		if displayCount > 5 {
			files = files[:5]
		}
		fileSummary := fmt.Sprintf("%d files touched", displayCount)
		if displayCount > 5 {
			fileSummary = fmt.Sprintf("%d files touched (showing first 5)", displayCount)
		}
		// Shorten each path.
		workingDir := ""
		if ses.Workspace != "" {
			workingDir = ses.Workspace
		}
		var shortFiles []string
		for _, f := range files {
			shortFiles = append(shortFiles, shortenPath(f, workingDir))
		}
		parts = append(parts, fileSummary+": "+strings.Join(shortFiles, ", "))
	}

	// Last user message preview.
	if ses.Preview != "" {
		preview := ses.Preview
		// #913: byte-slicing at 120 split CJK runes mid-character and
		// rendered mojibake; truncate on rune boundaries.
		if utf8.RuneCountInString(preview) > 120 {
			preview = string([]rune(preview)[:120]) + "..."
		}
		preview = strings.ReplaceAll(preview, "\n", " ")
		parts = append(parts, "Last: \""+preview+"\"")
	}

	return "Session resumed — " + strings.Join(parts, " · ") + "\n" +
		"(Type /search to find specific content in past sessions, or /sessions to switch.)"
}

// formatSessionAge returns a human-readable relative time string.
func formatSessionAge(updatedAt, now time.Time) string {
	if updatedAt.IsZero() {
		return "unknown"
	}
	d := now.Sub(updatedAt)
	switch {
	case d < time.Minute:
		return "just now"
	case d < time.Hour:
		return fmt.Sprintf("%dm ago", int(d.Minutes()))
	case d < 24*time.Hour:
		return fmt.Sprintf("%dh ago", int(d.Hours()))
	case d < 7*24*time.Hour:
		return fmt.Sprintf("%dd ago", int(d.Hours()/24))
	case d < 365*24*time.Hour:
		return fmt.Sprintf("%d weeks ago", int(d.Hours()/(24*7)))
	default:
		return updatedAt.Format("Jan 2, 2006")
	}
}

// formatK formats a number with K/M suffixes for compact display.
func formatK(n int) string {
	if n >= 1_000_000 {
		return fmt.Sprintf("%.1fM", float64(n)/1_000_000)
	}
	if n >= 1_000 {
		return fmt.Sprintf("%.1fK", float64(n)/1_000)
	}
	return fmt.Sprintf("%d", n)
}

// sessionCostString tries to extract a formatted cost string from the session's
// stored CostJSON. Returns empty string if no cost data is available.
func sessionCostString(ses *session.Session) string {
	if len(ses.CostJSON) == 0 {
		return ""
	}
	// CostJSON stores cost.SessionCostData as opaque JSON.
	var data struct {
		Sessions []struct {
			TotalCostUSD float64 `json:"total_cost_usd"`
		} `json:"sessions"`
		TotalCostUSD float64 `json:"total_cost_usd"`
	}
	if err := unmarshalCostJSON(ses.CostJSON, &data); err != nil {
		return ""
	}
	total := data.TotalCostUSD
	for _, s := range data.Sessions {
		total += s.TotalCostUSD
	}
	if total > 0 {
		return cost.FormatCost(total)
	}
	return ""
}

// unmarshalCostJSON is a thin wrapper to keep the recap function readable.
func unmarshalCostJSON(raw []byte, v interface{}) error {
	return json.Unmarshal(raw, v)
}

// extractFilePathFromInput looks for file path arguments in a tool_use
// block's input JSON (provider.ContentBlock.Input).
// Returns paths found in the input fields.
func extractFilePathFromInput(raw json.RawMessage) []string {
	var paths []string
	if len(raw) == 0 {
		return paths
	}

	// Parse input JSON to look for path-like fields.
	var args map[string]interface{}
	if err := json.Unmarshal(raw, &args); err != nil {
		return paths
	}

	// Common field names that contain file paths across our tool set.
	pathFields := []string{
		"file_path", "path", "filepath", "src",
		"dest", "destination", "target", "old_text",
	}
	for _, field := range pathFields {
		if val, ok := args[field]; ok {
			if s, ok := val.(string); ok && looksLikeFilePath(s) {
				paths = append(paths, s)
			}
		}
	}

	// For files arrays (used by multi-file tools).
	if files, ok := args["files"]; ok {
		if arr, ok := files.([]interface{}); ok {
			for _, item := range arr {
				if m, ok := item.(map[string]interface{}); ok {
					if p, ok := m["path"].(string); ok && looksLikeFilePath(p) {
						paths = append(paths, p)
					}
				}
			}
		}
	}

	return paths
}

// looksLikeFilePath checks if a string looks like a file path rather than a
// URL, command, or search pattern.
func looksLikeFilePath(s string) bool {
	if s == "" || len(s) > 1024 {
		return false
	}
	// Reject URLs.
	if strings.HasPrefix(s, "http://") || strings.HasPrefix(s, "https://") {
		return false
	}
	// Reject search patterns (contain wildcards without / separators).
	if strings.ContainsAny(s, "*?") && !strings.Contains(s, "/") {
		return false
	}
	// Must contain a path separator or a file extension to look like a file.
	return strings.Contains(s, "/") || strings.Contains(s, ".")
}
