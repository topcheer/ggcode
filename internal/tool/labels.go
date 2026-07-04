package tool

import (
	"encoding/json"
	"fmt"
	"strconv"
	"strings"
	"time"
)

// ToolPresentation holds the human-readable display info for a tool call.
type ToolPresentation struct {
	DisplayName string // e.g. "Read", "Edit", "go test ./..."
	Detail      string // e.g. "/tmp/test.go", ""
}

// ToolResultPresentation holds shared semantic fields for a rendered tool result.
type ToolResultPresentation struct {
	Summary     string // compact always-visible summary
	Payload     string // optional expanded detail payload
	PayloadMode string // "", "text", "task_fields", "task_list"
}

// DescribeTool returns a human-readable presentation for a tool call.
// It picks the key argument(s) for each tool and formats them compactly.
// This is the shared implementation used by TUI, daemon, IM, and ACP.
func DescribeTool(toolName, rawArgs string) ToolPresentation {
	args := parseToolArgs(rawArgs)
	fileTarget := extractFileTarget(toolName, rawArgs)

	// Tools with special label logic — use specific fields, not generic description
	// These tools either have no "description" param, or their "description" field
	// means something different (long requirements instead of brief label).
	specialLabelTools := map[string]bool{
		// Commands: use first-line comment as label
		"run_command": true, "start_command": true, "bash": true, "powershell": true,
		// Agent: description is brief label, but handled specially for truncation
		"spawn_agent": true,
		// Task: description = detailed requirements, subject = brief title
		"swarm_task_create": true, "task_create": true,
		// Plan mode: special rendering
		"enter_plan_mode": true, "exit_plan_mode": true,
		// Teammate: uses name field
		"teammate_spawn": true,
	}

	// Most tools have a "description" parameter (brief activity label from LLM).
	// Use it as displayName when available, with detail as fallback context.
	if !specialLabelTools[toolName] {
		if desc := strings.TrimSpace(argStr(args, "description")); desc != "" {
			detail := ""
			if fileTarget != "" {
				detail = fileTarget
			}
			return toolPres(desc, detail)
		}
	}

	switch toolName {
	case "read_file":
		return toolPres("Read", fileTarget)
	case "edit_file":
		if strings.TrimSpace(argStr(args, "old_text")) == "" && fileTarget != "" {
			return toolPres("Create", fileTarget)
		}
		return toolPres("Edit", fileTarget)
	case "write_file":
		return toolPres("Write", fileTarget)
	case "multi_edit":
		return toolPres("Edit", fileTarget)
	case "glob":
		return toolPres("Find", displayTarget(argStr(args, "pattern")))
	case "grep", "search_files":
		return toolPres("Search", displayTarget(firstNonEmpty(
			argStr(args, "pattern"),
			argStr(args, "query"),
			argStr(args, "path"),
		)))
	case "list_directory":
		return toolPres("List", displayFileTarget(firstNonEmpty(
			argStr(args, "path"),
			argStr(args, "directory"),
		)))
	case "run_command", "bash", "powershell", "start_command":
		cmd := firstNonEmpty(argStr(args, "command"), argStr(args, "cmd"))
		desc := argStr(args, "description")
		if cmd != "" {
			// Title priority: description param > first-line comment > command preview
			title := ""
			if desc != "" {
				title = desc
			} else {
				title = extractFirstLineComment(cmd)
			}
			if title == "" {
				title = compactSingleLineNoTruncate(cmd)
			}
			return toolPres(title, "")
		}
		return toolPres("Run", "")
	case "write_command_input":
		inputText := argStr(args, "input")
		jobID := argStr(args, "job_id")
		if inputText != "" {
			if len(inputText) > 60 {
				inputText = inputText[:57] + "…"
			}
			detail := "→ " + inputText
			if jobID != "" {
				detail = "[" + shortenID(jobID) + "] " + detail
			}
			return toolPres("Input", detail)
		}
		return toolPres("Input", firstNonEmpty(jobID, "background command"))
	case "read_command_output":
		return toolPres("Output", displayTarget(shortenID(argStr(args, "job_id"))))
	case "wait_command":
		jobID := argStr(args, "job_id")
		waitSec := argStr(args, "wait_seconds")
		detail := shortenID(jobID)
		if waitSec != "" {
			detail = fmt.Sprintf("%s (%ss)", detail, waitSec)
		}
		return toolPres("Wait", displayTarget(detail))
	case "stop_command":
		return toolPres("Stop", displayTarget(shortenID(argStr(args, "job_id"))))
	case "list_commands":
		return toolPres("Background Jobs", "")
	case "web_fetch":
		return toolPres("Fetch", displayTarget(argStr(args, "url")))
	case "web_search":
		return toolPres("Search", displayTarget(argStr(args, "query")))
	case "todo_write":
		return toolPres("Update Todos", "")
	case "ask_user":
		return toolPres("Ask", displayTarget(askUserTarget(args)))
	case "skill":
		return toolPres("Skill", displayTarget(argStr(args, "skill")))
	case "save_memory":
		return toolPres("Save Memory", argStr(args, "key"))
	case "git_status":
		return toolPres("Inspect", displayFileTarget(argStr(args, "path")))
	case "git_diff":
		detail := ""
		if argStr(args, "cached") == "true" {
			detail = "--cached"
		}
		if f := argStr(args, "file"); f != "" {
			if detail != "" {
				detail += " "
			}
			detail += displayFileTarget(f)
		}
		return toolPres("Diff", detail)
	case "git_log":
		return toolPres("Log", "")
	case "git_show":
		return toolPres("Show", displayTarget(argStr(args, "revision")))
	case "git_blame":
		return toolPres("Blame", displayFileTarget(argStr(args, "file")))
	case "git_branch_list":
		detail := ""
		if argStr(args, "remote") == "true" {
			detail = "--remote"
		}
		return toolPres("Branches", detail)
	case "git_remote":
		return toolPres("Remote", "")
	case "git_stash_list":
		return toolPres("Stash", "list")
	case "git_add":
		files := parseStringSlice(args, "files")
		return toolPres("Stage", displayFileTarget(strings.Join(files, ", ")))
	case "git_commit":
		return toolPres("Commit", compactSingleLine(argStr(args, "message")))
	case "git_stash":
		action := argStr(args, "action")
		if action == "" {
			action = "push"
		}
		return toolPres("Stash", action)
	case "sleep":
		sec, _ := strconv.Atoi(argStr(args, "seconds"))
		ms, _ := strconv.Atoi(argStr(args, "milliseconds"))
		d := time.Duration(sec)*time.Second + time.Duration(ms)*time.Millisecond
		return toolPres("Sleep", d.String())
	case "spawn_agent":
		desc := strings.TrimSpace(argStr(args, "description"))
		if len(desc) > 60 {
			desc = desc[:57] + "..."
		}
		if desc == "" {
			desc = "Spawn Agent"
		}
		return toolPres(desc, "")
	case "task_create":
		return toolPres("Task", displayTarget(firstNonEmpty(
			argStr(args, "subject"),
			argStr(args, "description"),
		)))
	case "task_get", "task_update", "task_stop":
		return toolPres("Task", displayTarget(argStr(args, "taskId")))
	case "task_list":
		return toolPres("Tasks", "")
	case "task_output":
		return toolPres("Task Output", displayTarget(firstNonEmpty(
			argStr(args, "task_id"),
			argStr(args, "taskId"),
		)))
	case "swarm_task_create":
		return toolPres(displayTarget(SwarmTaskCreateSubject(rawArgs)), "")
	case "wait_agent":
		return toolPres("Wait Agent", displayTarget(argStr(args, "task_id")))
	case "list_agents":
		return toolPres("List Agents", "")
	case "team_create":
		return toolPres("Create Team", displayTarget(argStr(args, "name")))
	case "team_delete":
		return toolPres("Delete Team", "")
	case "teammate_spawn":
		return toolPres("Spawn", displayTarget(firstNonEmpty(
			argStr(args, "name"),
			argStr(args, "task"),
		)))
	case "teammate_shutdown":
		return toolPres("Shutdown", displayTarget(argStr(args, "name")))
	case "swarm_task_claim":
		return toolPres("Claim Task", displayTarget(argStr(args, "task_id")))
	case "swarm_task_complete":
		return toolPres("Complete Task", displayTarget(argStr(args, "task_id")))
	case "swarm_task_list":
		return toolPres("Tasks", "list")
	case "delegate":
		return toolPres("Delegate", displayTarget(argStr(args, "agent")))
	case "send_message":
		return toolPres("Send", compactSingleLine(argStr(args, "message")))
	case "cron_create":
		return toolPres("Schedule", compactSingleLine(argStr(args, "prompt")))
	case "cron_list":
		return toolPres("Schedules", "list")
	case "cron_delete":
		return toolPres("Delete Schedule", displayTarget(argStr(args, "jobId")))
	case "cron_update":
		return toolPres("Update Schedule", displayTarget(argStr(args, "jobId")))
	case "cron_pause":
		return toolPres("Pause Schedule", displayTarget(argStr(args, "jobId")))
	case "cron_resume":
		return toolPres("Resume Schedule", displayTarget(argStr(args, "jobId")))
	case "cron_get":
		return toolPres("Schedule Details", displayTarget(argStr(args, "jobId")))
	case "enter_plan_mode":
		return toolPres("Planning...", "")
	case "exit_plan_mode":
		return toolPres("Execute Plan", "")
	case "enter_worktree":
		return toolPres("Worktree", displayTarget(argStr(args, "name")))
	case "exit_worktree":
		return toolPres("Exit Worktree", displayTarget(argStr(args, "action")))
	case "a2a_send_task":
		return toolPres("A2A", displayTarget(argStr(args, "target")))
	case "read_mcp_resource":
		return toolPres("MCP Resource", displayTarget(argStr(args, "uri")))
	case "ghostty":
		return ghosttyLabel(args)
	case "kitty":
		return kittyLabel(args)
	case "iterm2":
		return iterm2Label(args)
	default:
		// MCP tools or unknown — prettify the name
		return toolPres(prettifyToolName(toolName), compactArgsPreview(rawArgs))
	}
}

// DescribeExternalToolCall normalizes external/ACP tool calls into the shared
// display model while preserving richer CLI-provided titles when they add real
// information beyond the raw tool name.
func DescribeExternalToolCall(toolName, toolTitle, rawArgs string) ToolPresentation {
	base := DescribeTool(toolName, rawArgs)
	title := compactSingleLineNoTruncate(strings.TrimSpace(toolTitle))
	if title == "" || toolTitleLooksGeneric(toolName, title, base) {
		return base
	}
	detail := base.Detail
	if fileTarget := extractFileTarget(toolName, rawArgs); fileTarget != "" {
		detail = fileTarget
	} else if detail == "" {
		detail = compactArgsPreview(rawArgs)
	}
	if detail != "" && strings.Contains(strings.ToLower(title), strings.ToLower(detail)) {
		detail = ""
	}
	return toolPres(title, detail)
}

// FormatToolInline combines DisplayName and Detail into a single display string,
// matching the TUI format: "Read /tmp/test.go" or "go test ./...".
func FormatToolInline(name, detail string) string {
	if detail == "" {
		return name
	}
	return name + " " + detail
}

// prettifyToolName converts snake_case tool names to Title Case.
func prettifyToolName(name string) string {
	// Split on underscores and capitalize each part
	parts := strings.Split(name, "_")
	for i, p := range parts {
		if len(p) > 0 {
			parts[i] = strings.ToUpper(p[:1]) + p[1:]
		}
	}
	return strings.Join(parts, " ")
}

// --- internal helpers ---

func toolPres(name, detail string) ToolPresentation {
	return ToolPresentation{DisplayName: name, Detail: detail}
}

// extractFirstLineComment returns the first line's # comment text from a shell command.
// e.g. "# Run tests\ncd foo && make test" → "Run tests"
func extractFirstLineComment(cmd string) string {
	lines := strings.SplitN(cmd, "\n", 2)
	first := strings.TrimSpace(lines[0])
	if strings.HasPrefix(first, "# ") {
		return strings.TrimSpace(strings.TrimPrefix(first, "# "))
	}
	return ""
}

func toolTitleLooksGeneric(toolName, title string, base ToolPresentation) bool {
	for _, candidate := range []string{
		toolName,
		prettifyToolName(toolName),
		base.DisplayName,
		FormatToolInline(base.DisplayName, base.Detail),
	} {
		if strings.EqualFold(strings.TrimSpace(candidate), title) {
			return true
		}
	}
	return false
}

func parseToolArgs(raw string) map[string]any {
	var args map[string]any
	if err := json.Unmarshal([]byte(raw), &args); err != nil {
		return nil
	}
	return args
}

func argStr(args map[string]any, key string) string {
	if args == nil {
		return ""
	}
	v, ok := args[key]
	if !ok {
		return ""
	}
	switch vv := v.(type) {
	case string:
		return vv
	case float64:
		return strconv.FormatFloat(vv, 'f', -1, 64)
	case bool:
		return strconv.FormatBool(vv)
	default:
		b, err := json.Marshal(v)
		if err != nil {
			return ""
		}
		return string(b)
	}
}

func firstNonEmpty(ss ...string) string {
	for _, s := range ss {
		if strings.TrimSpace(s) != "" {
			return s
		}
	}
	return ""
}

func displayTarget(s string) string {
	return strings.TrimSpace(s)
}

func displayFileTarget(s string) string {
	s = strings.TrimSpace(s)
	if s == "" {
		return ""
	}
	return strings.TrimRight(s, `/\`)
}

// SwarmTaskCreateSubject returns the human-facing task subject for swarm_task_create.
func SwarmTaskCreateSubject(rawArgs string) string {
	args := parseToolArgs(rawArgs)
	return firstNonEmpty(
		displayTarget(argStr(args, "subject")),
		displayTarget(argStr(args, "description")),
	)
}

// SwarmTaskCreateResultMarkdown extracts the markdown description from a swarm_task_create result.
func SwarmTaskCreateResultMarkdown(result string) string {
	trimmed := strings.TrimSpace(result)
	if trimmed == "" {
		return result
	}
	var raw map[string]any
	if err := json.Unmarshal([]byte(trimmed), &raw); err != nil {
		return result
	}
	description, _ := raw["Description"].(string)
	if description == "" {
		description, _ = raw["description"].(string)
	}
	description = strings.TrimSpace(description)
	if description == "" {
		return result
	}
	return description
}

// DescribeToolResult returns shared summary/payload semantics for structured tool results.
func DescribeToolResult(toolName, rawArgs, result string, isError bool) (ToolResultPresentation, bool) {
	trimmed := strings.TrimSpace(result)
	if trimmed == "" {
		return ToolResultPresentation{}, true
	}
	if isError {
		return ToolResultPresentation{Summary: compactSingleLine(trimmed)}, true
	}

	switch toolName {
	case "task_create", "task_get", "task_update":
		if pres, ok := describeTaskObjectResult(toolName, rawArgs, trimmed); ok {
			return pres, true
		}
	case "task_list":
		return describeTaskListResult(trimmed), true
	case "task_stop":
		return describeTaskStopResult(rawArgs, trimmed), true
	case "task_output":
		return describeTaskOutputResult(rawArgs, trimmed), true
	case "cron_create":
		if pres, ok := describeCronCreateResult(trimmed); ok {
			return pres, true
		}
	case "cron_delete":
		return describeCronDeleteResult(trimmed), true
	case "cron_list":
		return describeCronListResult(trimmed), true
	case "lanchat":
		if pres, ok := describeLanchatResult(rawArgs, trimmed, isError); ok {
			return pres, true
		}
	}

	if pres, ok := describeExternalWrappedResult(trimmed); ok {
		return pres, true
	}

	if !isTaskTool(toolName) && toolName != "cron_create" && toolName != "cron_delete" && toolName != "cron_list" && toolName != "cron_update" && toolName != "cron_pause" && toolName != "cron_resume" && toolName != "cron_get" {
		return ToolResultPresentation{}, false
	}

	return ToolResultPresentation{
		Summary: compactSingleLine(trimmed),
		Payload: trimmed,
	}, true
}

// DescribeTaskToolResult returns shared summary/payload semantics for task_* tools.
func DescribeTaskToolResult(toolName, rawArgs, result string, isError bool) (ToolResultPresentation, bool) {
	if !isTaskTool(toolName) {
		return ToolResultPresentation{}, false
	}
	return DescribeToolResult(toolName, rawArgs, result, isError)
}

// describeLanchatResult formats the lanchat tool result into a human-readable
// presentation. The "list" action returns JSON that needs full formatting;
// the send/broadcast actions return a terse confirmation — we enrich it with
// the message content and recipients extracted from rawArgs.
func describeLanchatResult(rawArgs, trimmed string, isError bool) (ToolResultPresentation, bool) {
	args := parseToolArgs(rawArgs)
	action := argStr(args, "action")

	switch action {
	case "list":
		return describeLanchatListResult(trimmed)
	case "send", "broadcast", "broadcast_all", "send_team":
		return describeLanchatSendResult(args, action, trimmed, isError)
	case "history":
		return describeLanchatHistoryResult(trimmed)
	case "pending":
		return describeLanchatPendingResult(trimmed)
	default:
		return ToolResultPresentation{}, false
	}
}

// describeLanchatListResult formats the JSON participant list into a
// human-readable table.
func describeLanchatListResult(trimmed string) (ToolResultPresentation, bool) {
	// Handle empty results.
	if strings.Contains(trimmed, "No participants") || strings.Contains(trimmed, "No LAN Chat participants") {
		return ToolResultPresentation{
			Summary:     trimmed,
			Payload:     "",
			PayloadMode: "text",
		}, true
	}

	// Extract the JSON array from the result.
	// Result format: "Participants (N):\n[...JSON...]"
	jsonStart := strings.Index(trimmed, "[")
	if jsonStart < 0 {
		return ToolResultPresentation{}, false
	}
	jsonStr := trimmed[jsonStart:]

	var peers []struct {
		NodeID      string   `json:"node_id"`
		HumanNick   string   `json:"human_nick"`
		AgentNick   string   `json:"agent_nick"`
		Role        string   `json:"role"`
		Team        string   `json:"team"`
		Online      bool     `json:"online"`
		LastSeen    string   `json:"last_seen"`
		ProjectName string   `json:"project_name,omitempty"`
		Languages   []string `json:"languages,omitempty"`
		AgentBusy   bool     `json:"agent_busy"`
		Self        bool     `json:"self"`
	}
	if err := json.Unmarshal([]byte(jsonStr), &peers); err != nil || len(peers) == 0 {
		return ToolResultPresentation{}, false
	}

	// Build header line.
	header := fmt.Sprintf("Participants (%d)", len(peers))

	// Build human-readable lines.
	var lines []string
	lines = append(lines, header)
	for _, p := range peers {
		prefix := "  "
		name := p.HumanNick
		if name == "" {
			name = p.NodeID
		}
		if p.Self {
			name += " (you)"
		}

		status := "offline"
		if p.Online {
			if p.AgentBusy {
				status = "busy"
			} else {
				status = "online"
			}
		}

		role := p.Role
		if role == "" {
			role = "?"
		}
		line1 := fmt.Sprintf("%s%s — %s · %s · %s", prefix, name, role, p.Team, status)
		lines = append(lines, line1)

		detail := ""
		if p.ProjectName != "" {
			detail = p.ProjectName
		}
		if p.LastSeen != "" && p.LastSeen != "never" {
			if detail != "" {
				detail += " · "
			}
			detail += "seen " + p.LastSeen
		}
		if detail != "" {
			lines = append(lines, fmt.Sprintf("      %s", detail))
		}
	}

	return ToolResultPresentation{
		Summary:     header,
		Payload:     strings.Join(lines, "\n"),
		PayloadMode: "text",
	}, true
}

// describeLanchatSendResult enriches the terse send/broadcast confirmation
// with the message content and recipient list from rawArgs, producing a
// readable "chat log" style display.
func describeLanchatSendResult(args map[string]any, action, trimmed string, isError bool) (ToolResultPresentation, bool) {
	message := argStr(args, "message")

	// On error, just pass through the error text — no message to show.
	if isError {
		return ToolResultPresentation{
			Summary:     compactSingleLine(trimmed),
			Payload:     trimmed,
			PayloadMode: "text",
		}, true
	}

	// Build the recipient line from rawArgs.
	toField := argStr(args, "to")
	teamField := argStr(args, "team")

	asAgent := true
	if v, ok := args["as_agent"]; ok {
		if b, ok2 := v.(bool); ok2 {
			asAgent = b
		}
	}
	identity := "human"
	if asAgent {
		identity = "agent"
	}

	var toLine string
	switch action {
	case "send":
		toLine = toField
	case "broadcast":
		toLine = "your team"
	case "broadcast_all":
		toLine = "ALL participants"
	case "send_team":
		if teamField != "" {
			toLine = fmt.Sprintf("team %q", teamField)
		} else {
			toLine = "team"
		}
	}

	// Extract delivery stats from the result string if available.
	// Examples: "Sent to 3/3 members of team \"X\"." → "3/3 delivered"
	deliveryExtra := extractLanchatDeliveryInfo(trimmed)

	// Build header.
	headerParts := []string{fmt.Sprintf("To: %s", toLine)}
	if deliveryExtra != "" {
		headerParts = append(headerParts, deliveryExtra)
	}
	headerParts = append(headerParts, fmt.Sprintf("as %s", identity))
	header := strings.Join(headerParts, "  ·  ")

	// Summary is the compact one-liner.
	summary := fmt.Sprintf("To: %s", toLine)

	// If no message content in args (shouldn't happen for successful sends),
	// fall through to raw result.
	if message == "" {
		return ToolResultPresentation{}, false
	}

	// Build full payload: header + blank + message content.
	payload := header + "\n\n" + strings.TrimSpace(message)

	return ToolResultPresentation{
		Summary:     summary,
		Payload:     payload,
		PayloadMode: "text",
	}, true
}

// extractLanchatDeliveryInfo extracts delivery statistics from the result
// string returned by doSendTeam/doBroadcastTeam.
// Example: "Sent to 3/5 members of team \"dev-team\"." → "3/5 delivered"
func extractLanchatDeliveryInfo(result string) string {
	// "Sent to N/M members of team ..." pattern.
	if strings.HasPrefix(result, "Sent to ") {
		rest := strings.TrimPrefix(result, "Sent to ")
		// Extract the N/M part.
		spaceIdx := strings.Index(rest, " ")
		if spaceIdx > 0 {
			ratio := rest[:spaceIdx]
			if strings.Contains(ratio, "/") {
				return ratio + " delivered"
			}
		}
	}
	return ""
}

// describeLanchatHistoryResult formats the JSON message history into a
// compact list — one line per message with a short content preview.
func describeLanchatHistoryResult(trimmed string) (ToolResultPresentation, bool) {
	if strings.Contains(trimmed, "No messages in history") {
		return ToolResultPresentation{
			Summary:     trimmed,
			PayloadMode: "text",
		}, true
	}

	jsonStart := strings.Index(trimmed, "[")
	if jsonStart < 0 {
		return ToolResultPresentation{}, false
	}
	jsonStr := trimmed[jsonStart:]

	var msgs []struct {
		From   string `json:"from"`
		Role   string `json:"role"`
		To     string `json:"to"`
		Body   string `json:"content"`
		Time   string `json:"time"`
		Direct bool   `json:"direct"`
	}
	if err := json.Unmarshal([]byte(jsonStr), &msgs); err != nil || len(msgs) == 0 {
		return ToolResultPresentation{}, false
	}

	header := fmt.Sprintf("History (%d messages)", len(msgs))

	var lines []string
	lines = append(lines, header)
	for _, m := range msgs {
		// Compact content preview: single line, max ~50 chars.
		preview := compactSingleLine(m.Body)
		if len(preview) > 50 {
			preview = preview[:50] + "..."
		}

		// Format: "  HH:MM:SS  nick → target   preview..."
		target := m.To
		if target == "" || target == "all" {
			target = "all"
		}
		lines = append(lines, fmt.Sprintf("  %s  %s → %s   %s", m.Time, m.From, target, preview))
	}

	return ToolResultPresentation{
		Summary:     header,
		Payload:     strings.Join(lines, "\n"),
		PayloadMode: "text",
	}, true
}

// describeLanchatPendingResult formats the JSON pending approvals list into
// a human-readable display with message_id (needed for approve/reject).
func describeLanchatPendingResult(trimmed string) (ToolResultPresentation, bool) {
	if strings.Contains(trimmed, "No pending") {
		return ToolResultPresentation{
			Summary:     trimmed,
			PayloadMode: "text",
		}, true
	}

	jsonStart := strings.Index(trimmed, "[")
	if jsonStart < 0 {
		return ToolResultPresentation{}, false
	}
	jsonStr := trimmed[jsonStart:]

	var items []struct {
		ID       string `json:"message_id"`
		From     string `json:"from"`
		FromRole string `json:"from_role"`
		Content  string `json:"content"`
		Received string `json:"received"`
	}
	if err := json.Unmarshal([]byte(jsonStr), &items); err != nil || len(items) == 0 {
		return ToolResultPresentation{}, false
	}

	header := fmt.Sprintf("Pending approvals (%d)", len(items))

	var lines []string
	lines = append(lines, header)
	for _, p := range items {
		role := p.FromRole
		if role == "" {
			role = "?"
		}
		line1 := fmt.Sprintf("  %s (%s) — %s", p.From, role, p.Received)
		lines = append(lines, line1)
		lines = append(lines, fmt.Sprintf("       %s", p.Content))
		lines = append(lines, fmt.Sprintf("       id: %s", p.ID))
	}

	return ToolResultPresentation{
		Summary:     header,
		Payload:     strings.Join(lines, "\n"),
		PayloadMode: "text",
	}, true
}

// TeamCreateResultText extracts the created team name from a team_create result.
func TeamCreateResultText(result string) string {
	trimmed := strings.TrimSpace(result)
	if trimmed == "" {
		return result
	}
	var raw map[string]any
	if err := json.Unmarshal([]byte(trimmed), &raw); err != nil {
		return result
	}
	name, _ := raw["Name"].(string)
	if name == "" {
		name, _ = raw["name"].(string)
	}
	name = strings.TrimSpace(name)
	if name == "" {
		return result
	}
	return fmt.Sprintf("Team %s Created", name)
}

func isTaskTool(toolName string) bool {
	switch toolName {
	case "task_create", "task_get", "task_update", "task_list", "task_stop", "task_output":
		return true
	default:
		return false
	}
}

type cronJobResultView struct {
	ID        string `json:"ID"`
	CronExpr  string `json:"CronExpr"`
	Prompt    string `json:"Prompt"`
	Recurring bool   `json:"Recurring"`
	NextFire  string `json:"NextFire"`
}

func describeCronCreateResult(trimmed string) (ToolResultPresentation, bool) {
	var job cronJobResultView
	if err := json.Unmarshal([]byte(trimmed), &job); err != nil {
		return ToolResultPresentation{}, false
	}
	job.ID = strings.TrimSpace(job.ID)
	job.CronExpr = strings.TrimSpace(job.CronExpr)
	job.Prompt = strings.TrimSpace(job.Prompt)
	job.NextFire = strings.TrimSpace(job.NextFire)
	if job.CronExpr == "" {
		return ToolResultPresentation{}, false
	}

	summary := "Scheduled " + job.CronExpr
	if !job.Recurring {
		summary = "Scheduled one-shot " + job.CronExpr
	}
	if job.ID != "" {
		summary += " — " + job.ID
	}

	var payload []string
	if job.ID != "" {
		payload = append(payload, "Job ID: "+job.ID)
	}
	payload = append(payload, "Cron: "+job.CronExpr)
	if job.Recurring {
		payload = append(payload, "Mode: recurring")
	} else {
		payload = append(payload, "Mode: one-shot")
	}
	if job.NextFire != "" {
		payload = append(payload, "Next fire: "+job.NextFire)
	}
	if job.Prompt != "" {
		payload = append(payload, "Prompt: "+job.Prompt)
	}

	return ToolResultPresentation{
		Summary:     summary,
		Payload:     strings.Join(payload, "\n"),
		PayloadMode: "cron_job",
	}, true
}

func describeCronDeleteResult(trimmed string) ToolResultPresentation {
	if trimmed == "" {
		return ToolResultPresentation{}
	}
	if strings.HasPrefix(trimmed, "Job ") && strings.HasSuffix(trimmed, " deleted") {
		jobID := strings.TrimSuffix(strings.TrimPrefix(trimmed, "Job "), " deleted")
		jobID = strings.TrimSpace(jobID)
		if jobID != "" {
			return ToolResultPresentation{Summary: "Deleted " + jobID}
		}
	}
	return ToolResultPresentation{Summary: compactSingleLine(trimmed)}
}

func describeCronListResult(trimmed string) ToolResultPresentation {
	if trimmed == "" {
		return ToolResultPresentation{}
	}
	lines := strings.Split(trimmed, "\n")
	count := 0
	for _, line := range lines {
		if strings.HasPrefix(strings.TrimSpace(line), "- ") {
			count++
		}
	}
	if count == 0 {
		return ToolResultPresentation{Summary: compactSingleLine(trimmed)}
	}
	summary := fmt.Sprintf("%d scheduled jobs", count)
	if count == 1 {
		summary = "1 scheduled job"
	}
	return ToolResultPresentation{
		Summary:     summary,
		Payload:     trimmed,
		PayloadMode: "cron_list",
	}
}

type taskResultView struct {
	ID          string            `json:"id"`
	Subject     string            `json:"subject"`
	Description string            `json:"description"`
	Status      string            `json:"status"`
	Owner       string            `json:"owner"`
	ActiveForm  string            `json:"activeForm"`
	Blocks      []string          `json:"blocks"`
	BlockedBy   []string          `json:"blockedBy"`
	Metadata    map[string]string `json:"metadata"`
}

func describeTaskObjectResult(toolName, rawArgs, trimmed string) (ToolResultPresentation, bool) {
	var tk taskResultView
	if err := json.Unmarshal([]byte(trimmed), &tk); err != nil {
		return ToolResultPresentation{}, false
	}

	summary := taskIdentitySummary(tk.Subject, tk.Status, tk.ID)
	switch toolName {
	case "task_create":
		summary = prefixTaskSummary("Created", summary)
	case "task_update":
		if changeText := taskUpdateChangeSummary(rawArgs); changeText != "" {
			summary = prefixTaskSummary("Updated", summary) + " (" + changeText + ")"
		} else {
			summary = prefixTaskSummary("Updated", summary)
		}
	}

	return ToolResultPresentation{
		Summary:     summary,
		Payload:     formatTaskPayload(tk),
		PayloadMode: "task_fields",
	}, true
}

func describeTaskListResult(trimmed string) ToolResultPresentation {
	if strings.EqualFold(trimmed, "No tasks.") {
		return ToolResultPresentation{Summary: "0 tasks"}
	}
	lines := nonEmptyTrimmedLines(trimmed)
	return ToolResultPresentation{
		Summary:     fmt.Sprintf("%d tasks", len(lines)),
		Payload:     strings.Join(lines, "\n"),
		PayloadMode: "task_list",
	}
}

func describeTaskStopResult(rawArgs, trimmed string) ToolResultPresentation {
	args := parseToolArgs(rawArgs)
	taskID := firstNonEmpty(argStr(args, "taskId"), argStr(args, "task_id"))
	summary := "Stopped task"
	if taskID != "" {
		summary = "Stopped " + taskID
	}
	payload := trimmed
	if compactSingleLine(trimmed) == summary {
		payload = ""
	}
	return ToolResultPresentation{
		Summary:     summary,
		Payload:     payload,
		PayloadMode: "text",
	}
}

func describeTaskOutputResult(rawArgs, trimmed string) ToolResultPresentation {
	args := parseToolArgs(rawArgs)
	taskID := firstNonEmpty(argStr(args, "task_id"), argStr(args, "taskId"))
	lineCount := len(nonEmptyTrimmedLines(trimmed))
	summary := "Fetched task output"
	if taskID != "" {
		summary = "Fetched output for " + taskID
	}
	if lineCount > 0 {
		summary = fmt.Sprintf("%s (%d lines)", summary, lineCount)
	}
	return ToolResultPresentation{
		Summary:     summary,
		Payload:     trimmed,
		PayloadMode: "text",
	}
}

func taskIdentitySummary(subject, status, id string) string {
	subject = strings.TrimSpace(subject)
	id = strings.TrimSpace(id)
	status = humanTaskStatus(status)
	switch {
	case subject != "" && status != "" && id != "":
		return fmt.Sprintf("%s [%s] — %s", subject, status, id)
	case subject != "" && status != "":
		return fmt.Sprintf("%s [%s]", subject, status)
	case subject != "" && id != "":
		return fmt.Sprintf("%s — %s", subject, id)
	case subject != "":
		return subject
	case id != "" && status != "":
		return fmt.Sprintf("%s [%s]", id, status)
	case id != "":
		return id
	default:
		return firstNonEmpty(status, "task")
	}
}

func prefixTaskSummary(prefix, summary string) string {
	if summary == "" {
		return prefix
	}
	return prefix + " " + summary
}

func humanTaskStatus(status string) string {
	switch strings.TrimSpace(status) {
	case "in_progress":
		return "in progress"
	case "completed":
		return "completed"
	case "pending":
		return "pending"
	default:
		return strings.TrimSpace(status)
	}
}

func taskUpdateChangeSummary(rawArgs string) string {
	args := parseToolArgs(rawArgs)
	if args == nil {
		return ""
	}
	var changes []string
	for _, key := range []string{"status", "subject", "owner", "activeForm", "metadata", "addBlocks", "addBlockedBy"} {
		if _, ok := args[key]; !ok {
			continue
		}
		switch key {
		case "activeForm":
			changes = append(changes, "activity")
		case "addBlocks":
			changes = append(changes, "blocks")
		case "addBlockedBy":
			changes = append(changes, "blocked by")
		default:
			changes = append(changes, key)
		}
		if len(changes) >= 3 {
			break
		}
	}
	return strings.Join(changes, ", ")
}

func formatTaskPayload(tk taskResultView) string {
	var lines []string
	if tk.ID != "" {
		lines = append(lines, "Task ID: "+tk.ID)
	}
	if status := humanTaskStatus(tk.Status); status != "" {
		lines = append(lines, "Status: "+status)
	}
	if tk.Subject != "" {
		lines = append(lines, "Subject: "+tk.Subject)
	}
	if tk.Owner != "" {
		lines = append(lines, "Owner: "+tk.Owner)
	}
	if tk.ActiveForm != "" {
		lines = append(lines, "Activity: "+tk.ActiveForm)
	}
	if tk.Description != "" {
		lines = append(lines, "Description: "+tk.Description)
	}
	if len(tk.BlockedBy) > 0 {
		lines = append(lines, "Blocked By: "+strings.Join(tk.BlockedBy, ", "))
	}
	if len(tk.Blocks) > 0 {
		lines = append(lines, "Blocks: "+strings.Join(tk.Blocks, ", "))
	}
	if len(tk.Metadata) > 0 {
		meta, err := json.Marshal(tk.Metadata)
		if err == nil {
			lines = append(lines, "Metadata: "+string(meta))
		}
	}
	return strings.Join(lines, "\n")
}

func nonEmptyTrimmedLines(s string) []string {
	lines := strings.Split(s, "\n")
	out := make([]string, 0, len(lines))
	for _, line := range lines {
		line = strings.TrimSpace(line)
		if line != "" {
			out = append(out, line)
		}
	}
	return out
}

// StartCommandResultText normalizes start_command results to a simple status label.
func StartCommandResultText(result string, isError bool) string {
	if isError {
		return "Failed"
	}
	trimmed := strings.TrimSpace(result)
	if trimmed == "" {
		return "Started"
	}
	for _, line := range strings.Split(trimmed, "\n") {
		line = strings.TrimSpace(line)
		if !strings.HasPrefix(line, "Status:") {
			continue
		}
		status := strings.ToLower(strings.TrimSpace(strings.TrimPrefix(line, "Status:")))
		switch status {
		case "failed", "error", "cancelled", "timed_out", "timeout":
			return "Failed"
		default:
			return "Started"
		}
	}
	return "Started"
}

func extractFileTarget(toolName, rawArgs string) string {
	// Try known file-path fields first
	args := parseToolArgs(rawArgs)
	for _, key := range []string{"file_path", "filePath", "path", "directory", "file", "filename"} {
		if v, ok := args[key].(string); ok && v != "" {
			return displayFileTarget(v)
		}
	}
	return ""
}

func compactArgsPreview(raw string) string {
	raw = strings.TrimSpace(raw)
	if isTrivialDetail(raw) {
		return ""
	}
	args := parseToolArgs(raw)
	if args == nil {
		return compactSingleLine(raw)
	}
	if len(args) == 0 {
		return ""
	}
	// Shorten file paths
	for _, key := range []string{"file_path", "path", "directory", "file", "filename"} {
		if v, ok := args[key].(string); ok {
			args[key] = displayFileTarget(v)
		}
	}
	b, err := json.Marshal(args)
	if err != nil {
		return compactSingleLine(raw)
	}
	return compactSingleLine(string(b))
}

func describeExternalWrappedResult(trimmed string) (ToolResultPresentation, bool) {
	var raw map[string]any
	if err := json.Unmarshal([]byte(trimmed), &raw); err != nil {
		return ToolResultPresentation{}, false
	}
	if pres, ok := describeCopilotWrappedResult(raw); ok {
		return pres, true
	}
	if pres, ok := describeOpenCodeWrappedResult(raw); ok {
		return pres, true
	}
	return ToolResultPresentation{}, false
}

func describeCopilotWrappedResult(raw map[string]any) (ToolResultPresentation, bool) {
	content, _ := raw["content"].(string)
	detailed, _ := raw["detailedContent"].(string)
	content = strings.TrimSpace(content)
	detailed = strings.TrimSpace(detailed)
	if content == "" && detailed == "" {
		return ToolResultPresentation{}, false
	}
	if detailed == "" && !onlyKnownKeys(raw, "content", "metadata") {
		return ToolResultPresentation{}, false
	}
	if detailed != "" && !onlyKnownKeys(raw, "content", "detailedContent", "metadata") {
		return ToolResultPresentation{}, false
	}
	return wrappedTextResult(firstNonEmpty(content, detailed), firstNonEmpty(detailed, content)), true
}

func describeOpenCodeWrappedResult(raw map[string]any) (ToolResultPresentation, bool) {
	output, _ := raw["output"].(string)
	output = strings.TrimSpace(output)
	metadata, _ := raw["metadata"].(map[string]any)
	preview := strings.TrimSpace(argStr(metadata, "preview"))
	if output == "" && preview == "" {
		return ToolResultPresentation{}, false
	}
	if preview == "" && !onlyKnownKeys(raw, "output", "metadata", "success", "status", "exitCode") {
		return ToolResultPresentation{}, false
	}
	if preview != "" && !onlyKnownKeys(raw, "output", "metadata", "success", "status", "exitCode") {
		return ToolResultPresentation{}, false
	}
	return wrappedTextResult(firstNonEmpty(preview, output), firstNonEmpty(output, preview)), true
}

func wrappedTextResult(summaryText, payloadText string) ToolResultPresentation {
	summaryText = strings.TrimSpace(summaryText)
	payloadText = strings.TrimSpace(payloadText)
	pres := ToolResultPresentation{Summary: compactSingleLine(summaryText)}
	if payloadText != "" && payloadText != summaryText {
		pres.Payload = payloadText
		pres.PayloadMode = "text"
	}
	return pres
}

func onlyKnownKeys(raw map[string]any, allowed ...string) bool {
	allowedSet := make(map[string]struct{}, len(allowed))
	for _, key := range allowed {
		allowedSet[key] = struct{}{}
	}
	for key := range raw {
		if _, ok := allowedSet[key]; !ok {
			return false
		}
	}
	return true
}

func compactSingleLine(s string) string {
	s = strings.ReplaceAll(s, "\n", " ")
	s = strings.Join(strings.Fields(s), " ")
	if len(s) > 120 {
		return s[:120] + "..."
	}
	return s
}

// compactSingleLineNoTruncate compacts but never truncates (for command display).
func compactSingleLineNoTruncate(s string) string {
	s = strings.ReplaceAll(s, "\n", " ")
	return strings.Join(strings.Fields(s), " ")
}

func isTrivialDetail(value string) bool {
	switch strings.TrimSpace(value) {
	case "", "{}", "[]", "null":
		return true
	default:
		return false
	}
}

func shortenID(id string) string {
	if len(id) > 8 {
		return id[:8]
	}
	return id
}

func parseStringSlice(args map[string]any, key string) []string {
	if args == nil {
		return nil
	}
	v, ok := args[key]
	if !ok {
		return nil
	}
	switch vv := v.(type) {
	case []string:
		return vv
	case []any:
		var result []string
		for _, item := range vv {
			if s, ok := item.(string); ok {
				result = append(result, s)
			}
		}
		return result
	}
	return nil
}

func askUserTarget(args map[string]any) string {
	if args == nil {
		return ""
	}
	// Try to extract a short prompt summary
	if prompt, ok := args["prompt"].(string); ok && prompt != "" {
		if len(prompt) > 60 {
			return prompt[:57] + "…"
		}
		return prompt
	}
	if questions, ok := args["questions"].([]any); ok && len(questions) > 0 {
		if first, ok := questions[0].(map[string]any); ok {
			if title, ok := first["title"].(string); ok {
				return title
			}
			if prompt, ok := first["prompt"].(string); ok {
				if len(prompt) > 60 {
					return prompt[:57] + "…"
				}
				return prompt
			}
		}
	}
	return ""
}

// RelativizePath is kept for API compatibility but no longer performs
// path replacement. Absolute paths are shown as-is.
func RelativizePath(path, _ string) string {
	return path
}

// ghosttyLabel produces a display label for ghostty tool calls.
func ghosttyLabel(args map[string]any) ToolPresentation {
	action := argStr(args, "action")
	switch action {
	case "status":
		return toolPres("Ghostty Status", "")
	case "list":
		return toolPres("Ghostty Surfaces", "")
	case "split":
		dir := argStr(args, "direction")
		if dir == "" {
			dir = "right"
		}
		cmd := argStr(args, "command")
		if cmd != "" {
			return toolPres("Ghostty Split "+dir, cmd)
		}
		return toolPres("Ghostty Split "+dir, "")
	case "new_tab":
		return toolPres("Ghostty New Tab", argStr(args, "command"))
	case "new_window":
		return toolPres("Ghostty New Window", argStr(args, "command"))
	case "focus":
		return toolPres("Ghostty Focus", shortenID(argStr(args, "terminal_id")))
	case "close":
		return toolPres("Ghostty Close", shortenID(argStr(args, "terminal_id")))
	case "input":
		return toolPres("Ghostty Input", compactPreview(argStr(args, "text")))
	case "send_key":
		key := argStr(args, "key")
		if mods := argStr(args, "modifiers"); mods != "" {
			key = mods + "+" + key
		}
		return toolPres("Ghostty Key", key)
	case "action":
		return toolPres("Ghostty Action", argStr(args, "text"))
	case "zoom":
		return toolPres("Ghostty Zoom", "")
	case "equalize":
		return toolPres("Ghostty Equalize", "")
	case "select_tab":
		idx := 0
		if v, ok := args["tab_index"]; ok {
			switch vv := v.(type) {
			case float64:
				idx = int(vv)
			case int:
				idx = vv
			}
		}
		return toolPres("Ghostty Select Tab", fmt.Sprintf("tab %d", idx))
	case "reload_config":
		return toolPres("Ghostty Reload Config", "")
	default:
		return toolPres("Ghostty", action)
	}
}

// kittyLabel produces a display label for kitty tool calls.
func kittyLabel(args map[string]any) ToolPresentation {
	action := argStr(args, "action")
	switch action {
	case "status":
		return toolPres("Kitty Status", "")
	case "list":
		return toolPres("Kitty Windows", "")
	case "split":
		dir := argStr(args, "direction")
		if dir == "" {
			dir = "right"
		}
		return toolPres("Kitty Split "+dir, argStr(args, "command"))
	case "new_tab":
		return toolPres("Kitty New Tab", argStr(args, "command"))
	case "new_window":
		return toolPres("Kitty New Window", argStr(args, "command"))
	case "focus":
		return toolPres("Kitty Focus", fmt.Sprintf("window %v", args["window_id"]))
	case "close":
		return toolPres("Kitty Close", fmt.Sprintf("window %v", args["window_id"]))
	case "close_tab":
		return toolPres("Kitty Close Tab", "")
	case "select_tab":
		idx := 0
		if v, ok := args["tab_index"]; ok {
			switch vv := v.(type) {
			case float64:
				idx = int(vv)
			case int:
				idx = vv
			}
		}
		return toolPres("Kitty Select Tab", fmt.Sprintf("tab %d", idx))
	case "input":
		return toolPres("Kitty Input", compactPreview(argStr(args, "text")))
	case "send_key":
		key := argStr(args, "key")
		if mods := argStr(args, "modifiers"); mods != "" {
			key = mods + "+" + key
		}
		return toolPres("Kitty Key", key)
	case "resize":
		axis := argStr(args, "axis")
		inc := 0
		if v, ok := args["increment"]; ok {
			switch vv := v.(type) {
			case float64:
				inc = int(vv)
			case int:
				inc = vv
			}
		}
		return toolPres("Kitty Resize", fmt.Sprintf("%s %+d", axis, inc))
	case "get_text":
		return toolPres("Kitty Get Text", fmt.Sprintf("window %v", args["window_id"]))
	case "zoom":
		return toolPres("Kitty Zoom", "")
	case "set_tab_title":
		return toolPres("Kitty Tab Title", argStr(args, "text"))
	case "action":
		return toolPres("Kitty Action", argStr(args, "text"))
	case "reload_config":
		return toolPres("Kitty Reload Config", "")
	default:
		return toolPres("Kitty", action)
	}
}

// iterm2Label produces a display label for iterm2 tool calls.
func iterm2Label(args map[string]any) ToolPresentation {
	action := argStr(args, "action")
	switch action {
	case "status":
		return toolPres("iTerm2 Status", "")
	case "list":
		return toolPres("iTerm2 Sessions", "")
	case "split":
		dir := argStr(args, "direction")
		if dir == "" {
			dir = "right"
		}
		return toolPres("iTerm2 Split "+dir, argStr(args, "command"))
	case "new_tab":
		return toolPres("iTerm2 New Tab", argStr(args, "command"))
	case "new_window":
		return toolPres("iTerm2 New Window", argStr(args, "command"))
	case "focus":
		return toolPres("iTerm2 Focus", argStr(args, "session_id"))
	case "close":
		return toolPres("iTerm2 Close", argStr(args, "session_id"))
	case "select_tab":
		idx := 0
		if v, ok := args["tab_index"]; ok {
			switch vv := v.(type) {
			case float64:
				idx = int(vv)
			case int:
				idx = vv
			}
		}
		return toolPres("iTerm2 Select Tab", fmt.Sprintf("tab %d", idx))
	case "input":
		return toolPres("iTerm2 Input", compactPreview(argStr(args, "text")))
	case "send_key":
		key := argStr(args, "key")
		if mods := argStr(args, "modifiers"); mods != "" {
			key = mods + "+" + key
		}
		return toolPres("iTerm2 Key", key)
	case "resize":
		axis := argStr(args, "axis")
		inc := 0
		if v, ok := args["increment"]; ok {
			switch vv := v.(type) {
			case float64:
				inc = int(vv)
			case int:
				inc = vv
			}
		}
		return toolPres("iTerm2 Resize", fmt.Sprintf("%s %+d", axis, inc))
	case "get_text":
		return toolPres("iTerm2 Get Text", argStr(args, "session_id"))
	case "set_title":
		return toolPres("iTerm2 Set Title", argStr(args, "text"))
	case "profile":
		return toolPres("iTerm2 Profile", argStr(args, "text"))
	case "badge":
		return toolPres("iTerm2 Badge", compactPreview(argStr(args, "text")))
	case "broadcast":
		return toolPres("iTerm2 Broadcast", argStr(args, "text"))
	case "mark":
		return toolPres("iTerm2 Mark", argStr(args, "text"))
	case "clear":
		return toolPres("iTerm2 Clear", "")
	case "action":
		return toolPres("iTerm2 Action", argStr(args, "text"))
	case "reload_config":
		return toolPres("iTerm2 Reload Config", "")
	default:
		return toolPres("iTerm2", action)
	}
}

func compactPreview(s string) string {
	s = strings.TrimSpace(s)
	if len(s) > 60 {
		return s[:57] + "..."
	}
	return s
}
