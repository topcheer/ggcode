package tool

import (
	"encoding/json"
	"fmt"
	"path/filepath"
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
		if cmd != "" {
			return toolPres(compactSingleLineNoTruncate(cmd), "")
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
		return toolPres("Spawn Agent", displayTarget(firstNonEmpty(
			argStr(args, "description"),
			argStr(args, "prompt"),
		)))
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
	}

	if pres, ok := describeExternalWrappedResult(trimmed); ok {
		return pres, true
	}

	if !isTaskTool(toolName) && toolName != "cron_create" && toolName != "cron_delete" && toolName != "cron_list" {
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

// RelativizePath tries to make an absolute path relative to workingDir.
func RelativizePath(path, workingDir string) string {
	if workingDir == "" {
		return path
	}
	rel, err := filepath.Rel(workingDir, path)
	if err != nil {
		return path
	}
	if strings.HasPrefix(rel, "../") || strings.HasPrefix(rel, ".."+string(filepath.Separator)) {
		return path
	}
	return rel
}
