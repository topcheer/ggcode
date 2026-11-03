package tui

import (
	"encoding/json"
	"fmt"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/topcheer/ggcode/internal/cost"

	"github.com/topcheer/ggcode/internal/session"
)

func sessionRecap(ses *session.Session, now time.Time) string {
	if ses == nil || len(ses.Messages) == 0 {
		return ""
	}

	userMsgs := 0
	var fileSet map[string]bool
	for _, msg := range ses.Messages {
		if msg.Role == "user" {
			userMsgs++
		}

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

	age := formatSessionAge(ses.UpdatedAt, now)
	title := ses.Title
	if title == "" {
		title = "Untitled"
	}
	parts = append(parts, fmt.Sprintf("**%s** (last active %s)", title, age))

	totalMsgs := len(ses.Messages)
	parts = append(parts, fmt.Sprintf("%d messages (%d user turns)", totalMsgs, userMsgs))

	usage := ses.TokenUsage
	if usage.InputTokens > 0 || usage.OutputTokens > 0 {
		totalTok := usage.InputTokens + usage.OutputTokens
		tokStr := fmt.Sprintf("%s tokens (in: %s, out: %s)",
			formatK(totalTok), formatK(usage.InputTokens), formatK(usage.OutputTokens))

		costStr := sessionCostString(ses)
		if costStr != "" {
			tokStr += ", cost: " + costStr
		}
		parts = append(parts, tokStr)
	}

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

	if ses.Preview != "" {
		preview := ses.Preview

		if utf8.RuneCountInString(preview) > 120 {
			preview = string([]rune(preview)[:120]) + "..."
		}
		preview = strings.ReplaceAll(preview, "\n", " ")
		parts = append(parts, "Last: \""+preview+"\"")
	}

	return "Session resumed — " + strings.Join(parts, " · ") + "\n" +
		"(Type /search to find specific content in past sessions, or /sessions to switch.)"
}

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

func formatK(n int) string {
	if n >= 1_000_000 {
		return fmt.Sprintf("%.1fM", float64(n)/1_000_000)
	}
	if n >= 1_000 {
		return fmt.Sprintf("%.1fK", float64(n)/1_000)
	}
	return fmt.Sprintf("%d", n)
}

func sessionCostString(ses *session.Session) string {
	if len(ses.CostJSON) == 0 {
		return ""
	}

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

func unmarshalCostJSON(raw []byte, v interface{}) error {
	return json.Unmarshal(raw, v)
}

func extractFilePathFromInput(raw json.RawMessage) []string {
	var paths []string
	if len(raw) == 0 {
		return paths
	}

	var args map[string]interface{}
	if err := json.Unmarshal(raw, &args); err != nil {
		return paths
	}

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

func looksLikeFilePath(s string) bool {
	if s == "" || len(s) > 1024 {
		return false
	}

	if strings.HasPrefix(s, "http://") || strings.HasPrefix(s, "https://") {
		return false
	}

	if strings.ContainsAny(s, "*?") && !strings.Contains(s, "/") {
		return false
	}

	return strings.Contains(s, "/") || strings.Contains(s, ".")
}
