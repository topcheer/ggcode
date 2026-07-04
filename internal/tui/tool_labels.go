package tui

import (
	"fmt"
	"strconv"
	"strings"
	"time"

	"github.com/mattn/go-runewidth"
	"github.com/topcheer/ggcode/internal/hooks"
	toolpkg "github.com/topcheer/ggcode/internal/tool"
	"github.com/topcheer/ggcode/internal/util"
)

type toolPresentation struct {
	DisplayName string
	Detail      string
	Activity    string
}

type commandPreview struct {
	Title                  string
	CommandLines           []string
	CommandHiddenLineCount int
}

const maxPreviewLines = 5

func describeTool(lang Language, toolName, rawArgs string) toolPresentation {
	args := parseToolArgs(rawArgs)
	fileTarget := displayToolFileTarget(hooks.ExtractFilePath(toolName, rawArgs))

	if toolName == "swarm_task_create" {
		subject := toolpkg.SwarmTaskCreateSubject(rawArgs)
		if subject != "" {
			return toolPresentation{DisplayName: subject, Activity: subject}
		}
	}

	// Universal priority: if the LLM provided a description field, use it as DisplayName.
	// This mirrors GUI's toolDescription() and IM's DescribeTool() behavior.
	if desc := argString(args, "description"); desc != "" {
		detail := fileTarget
		if detail == "" {
			detail = argString(args, "command")
		}
		if detail == "" {
			detail = argString(args, "path")
		}
		if detail == "" {
			detail = argString(args, "file_path")
		}
		if detail == "" {
			detail = argString(args, "query")
		}
		if detail == "" {
			detail = argString(args, "pattern")
		}
		detail = displayToolTarget(detail)
		pretty := friendlyToolName(toolName)
		displayName := desc + " (" + pretty + ")"
		return toolPresentation{DisplayName: displayName, Detail: detail, Activity: desc}
	}

	switch toolName {
	case "read_file":
		return toolPresentationFor(lang, "read", fileTarget)
	case "edit_file":
		if strings.TrimSpace(argString(args, "old_text")) == "" && fileTarget != "" {
			return toolPresentationFor(lang, "create", fileTarget)
		}
		return toolPresentationFor(lang, "edit", fileTarget)
	case "write_file":
		return toolPresentationFor(lang, "write", fileTarget)
	case "glob":
		pattern := displayToolTarget(argString(args, "pattern"))
		return toolPresentationFor(lang, "find", pattern)
	case "grep", "search_files":
		searchTarget := displayToolTarget(util.FirstNonEmpty(
			argString(args, "pattern"),
			argString(args, "query"),
			argString(args, "path"),
		))
		return toolPresentationFor(lang, "search", searchTarget)
	case "list_directory":
		listTarget := displayToolFileTarget(util.FirstNonEmpty(
			argString(args, "path"),
			argString(args, "directory"),
		))
		return toolPresentationFor(lang, "list", listTarget)
	case "run_command", "bash", "powershell":
		if present, ok := commandToolPresentation(lang, rawCommandArg(args)); ok {
			return present
		}
		target := displayToolTarget(util.FirstNonEmpty(
			argString(args, "command"),
			argString(args, "cmd"),
		))
		return toolPresentationFor(lang, "run", target)
	case "start_command":
		if present, ok := commandToolPresentation(lang, rawCommandArg(args)); ok {
			return present
		}
		target := displayToolTarget(util.FirstNonEmpty(
			argString(args, "command"),
			argString(args, "cmd"),
		))
		return toolPresentationFor(lang, "run_in_background", target)
	case "write_command_input":
		// The input text being sent to the process is the most important detail
		inputText := argString(args, "input")
		jobID := argString(args, "job_id")
		if inputText != "" {
			// Truncate long input for display
			if runewidth.StringWidth(inputText) > 60 {
				runes := []rune(inputText)
				w := 0
				cut := len(runes)
				for i, r := range runes {
					rw := runewidth.RuneWidth(r)
					if w+rw > 57 {
						cut = i
						break
					}
					w += rw
				}
				inputText = string(runes[:cut]) + "…"
			}
			detail := fmt.Sprintf("→ %s", inputText)
			if jobID != "" {
				shortID := shortenJobID(jobID)
				detail = fmt.Sprintf("[%s] → %s", shortID, inputText)
			}
			return toolPresentationFor(lang, "input", detail)
		}
		return toolPresentationFor(lang, "input", displayToolTarget(util.FirstNonEmpty(
			jobID,
			"background command",
		)))
	case "read_command_output":
		jobID := argString(args, "job_id")
		return toolPresentationFor(lang, "output", displayToolTarget(shortenJobID(jobID)))
	case "wait_command":
		jobID := argString(args, "job_id")
		waitSec := argString(args, "wait_seconds")
		detail := shortenJobID(jobID)
		if waitSec != "" {
			detail = fmt.Sprintf("%s (%ss)", detail, waitSec)
		}
		return toolPresentationFor(lang, "wait", displayToolTarget(detail))
	case "stop_command":
		jobID := argString(args, "job_id")
		return toolPresentationFor(lang, "stop", displayToolTarget(shortenJobID(jobID)))
	case "list_commands":
		return toolPresentationFor(lang, "list_jobs", "")
	case "web_fetch":
		fetchTarget := displayToolTarget(argString(args, "url"))
		return toolPresentationFor(lang, "fetch", fetchTarget)
	case "web_search":
		searchTarget := displayToolTarget(argString(args, "query"))
		return toolPresentationFor(lang, "search", searchTarget)
	case "todo_write":
		return toolPresentationFor(lang, "todo", "")
	case "task", "agent":
		return toolPresentationFor(lang, "task", displayToolTarget(util.FirstNonEmpty(
			argString(args, "description"),
			argString(args, "prompt"),
			argString(args, "agent_type"),
		)))
	case "skill":
		skillTarget := displayToolTarget(argString(args, "skill"))
		return toolPresentationFor(lang, "skill", skillTarget)
	case "ask_user":
		return toolPresentationFor(lang, "ask", displayToolTarget(askUserToolTarget(args)))
	case "git_status":
		statusTarget := displayToolFileTarget(argString(args, "path"))
		return toolPresentationFor(lang, "inspect", statusTarget)
	case "git_diff":
		detail := ""
		if argString(args, "cached") == "true" {
			detail = "--cached"
		}
		if f := argString(args, "file"); f != "" {
			if detail != "" {
				detail += " "
			}
			detail += displayToolFileTarget(f)
		}
		return toolPresentationFor(lang, "diff", detail)
	case "git_log":
		return toolPresentationFor(lang, "log", "")
	case "git_show":
		showTarget := displayToolTarget(argString(args, "revision"))
		return toolPresentationFor(lang, "show", showTarget)
	case "git_blame":
		blameTarget := displayToolFileTarget(argString(args, "file"))
		return toolPresentationFor(lang, "blame", blameTarget)
	case "git_branch_list":
		detail := ""
		if argString(args, "remote") == "true" {
			detail = "--remote"
		}
		return toolPresentationFor(lang, "branches", detail)
	case "git_remote":
		return toolPresentationFor(lang, "remote", "")
	case "git_stash_list":
		return toolPresentationFor(lang, "stash", "list")
	case "git_add":
		files := parseStringSlice(args, "files")
		stageTarget := displayToolFileTarget(strings.Join(files, ", "))
		return toolPresentationFor(lang, "stage", stageTarget)
	case "git_commit":
		commitDetail := compactSingleLine(argString(args, "message"))
		return toolPresentationFor(lang, "commit", commitDetail)
	case "git_stash":
		action := argString(args, "action")
		if action == "" {
			action = "push"
		}
		return toolPresentationFor(lang, "stash", action)
	case "sleep":
		sec, _ := strconv.Atoi(argString(args, "seconds"))
		ms, _ := strconv.Atoi(argString(args, "milliseconds"))
		d := time.Duration(sec)*time.Second + time.Duration(ms)*time.Millisecond
		if d <= 0 {
			d = 0
		}
		return toolPresentation{
			DisplayName: localizedToolLabel(lang, "sleep"),
			Detail:      d.String(),
			Activity:    localizedToolActivity(lang, "sleep", d.String()),
		}
	case "cron_create":
		cronExpr := argString(args, "cron")
		return toolPresentation{
			DisplayName: localizedToolLabel(lang, "cron_create"),
			Detail:      cronExpr,
			Activity:    localizedToolActivity(lang, "cron_create", cronExpr),
		}
	case "cron_delete":
		return toolPresentationFor(lang, "delete", "cron job")
	case "cron_list":
		return toolPresentationFor(lang, "inspect", "cron jobs")
	case "cron_update":
		jobID := argString(args, "jobId")
		return toolPresentation{
			DisplayName: localizedToolLabel(lang, "cron_update"),
			Detail:      jobID,
			Activity:    localizedToolActivity(lang, "cron_update", jobID),
		}
	case "cron_pause":
		jobID := argString(args, "jobId")
		return toolPresentation{
			DisplayName: localizedToolLabel(lang, "cron_pause"),
			Detail:      jobID,
			Activity:    localizedToolActivity(lang, "cron_pause", jobID),
		}
	case "cron_resume":
		jobID := argString(args, "jobId")
		return toolPresentation{
			DisplayName: localizedToolLabel(lang, "cron_resume"),
			Detail:      jobID,
			Activity:    localizedToolActivity(lang, "cron_resume", jobID),
		}
	case "cron_get":
		jobID := argString(args, "jobId")
		return toolPresentation{
			DisplayName: localizedToolLabel(lang, "cron_get"),
			Detail:      jobID,
			Activity:    localizedToolActivity(lang, "cron_get", jobID),
		}
	case "enter_worktree":
		name := argString(args, "name")
		return toolPresentation{
			DisplayName: localizedToolLabel(lang, "enter_worktree"),
			Detail:      name,
			Activity:    localizedToolActivity(lang, "enter_worktree", name),
		}
	case "exit_worktree":
		action := argString(args, "action")
		return toolPresentation{
			DisplayName: localizedToolLabel(lang, "exit_worktree"),
			Detail:      action,
			Activity:    localizedToolActivity(lang, "exit_worktree", action),
		}
	case "save_memory":
		key := argString(args, "key")
		return toolPresentation{
			DisplayName: localizedToolLabel(lang, "save_memory"),
			Detail:      key,
			Activity:    localizedToolActivity(lang, "save_memory", key),
		}
	case "config":
		setting := argString(args, "setting")
		value := argString(args, "value")
		detail := setting
		if value != "" {
			detail = setting + " = " + value
		}
		return toolPresentation{
			DisplayName: localizedToolLabel(lang, "config"),
			Detail:      detail,
			Activity:    localizedToolActivity(lang, "config", detail),
		}
	case "send_message":
		to := argString(args, "to")
		msg := argString(args, "message")
		detail := to
		if msg != "" {
			if runewidth.StringWidth(msg) > 40 {
				runes := []rune(msg)
				w := 0
				cut := len(runes)
				for i, r := range runes {
					rw := runewidth.RuneWidth(r)
					if w+rw > 37 {
						cut = i
						break
					}
					w += rw
				}
				msg = string(runes[:cut]) + "…"
			}
			detail = to + ": " + msg
		}
		return toolPresentation{
			DisplayName: localizedToolLabel(lang, "send_message"),
			Detail:      detail,
			Activity:    localizedToolActivity(lang, "send_message", to),
		}
	case "enter_plan_mode":
		return toolPresentation{
			DisplayName: localizedToolLabel(lang, "enter_plan"),
			Detail:      "",
			Activity:    localizedToolActivity(lang, "enter_plan", ""),
		}
	case "exit_plan_mode":
		return toolPresentation{
			DisplayName: localizedToolLabel(lang, "exit_plan"),
			Detail:      "",
			Activity:    localizedToolActivity(lang, "exit_plan", ""),
		}
	case "task_create", "task_update", "task_get", "task_list", "task_stop", "task_output":
		return toolPresentationFor(lang, "task", displayToolTarget(util.FirstNonEmpty(
			argString(args, "subject"),
			argString(args, "description"),
			argString(args, "taskId"),
		)))
	case "spawn_agent":
		task := argString(args, "task")
		desc := argString(args, "description")
		name := desc
		if name == "" {
			name = toolLabelFor(lang, "spawn_agent")
		}
		return toolPresentation{
			DisplayName: name,
			Detail:      compactSingleLine(task),
			Activity:    name,
		}
	case "list_agents":
		return toolPresentation{
			DisplayName: toolLabelFor(lang, "list_agents"),
			Detail:      "",
			Activity:    toolLabelFor(lang, "list_agents"),
		}
	case "wait_agent":
		agentID := argString(args, "agent_id")
		return toolPresentation{
			DisplayName: toolLabelFor(lang, "wait_agent"),
			Detail:      shortenJobID(agentID),
			Activity:    toolLabelFor(lang, "wait_agent"),
		}
	case "team_create":
		name := argString(args, "name")
		return toolPresentation{
			DisplayName: localizedToolLabel(lang, "team_create"),
			Detail:      name,
			Activity:    localizedToolActivity(lang, "team_create", name),
		}
	case "team_delete":
		return toolPresentation{
			DisplayName: localizedToolLabel(lang, "team_delete"),
			Detail:      argString(args, "team_id"),
			Activity:    localizedToolActivity(lang, "team_delete", ""),
		}
	case "teammate_spawn":
		name := argString(args, "name")
		return toolPresentation{
			DisplayName: localizedToolLabel(lang, "teammate_spawn"),
			Detail:      name,
			Activity:    localizedToolActivity(lang, "teammate_spawn", name),
		}
	case "teammate_list":
		return toolPresentation{
			DisplayName: localizedToolLabel(lang, "teammate_list"),
			Detail:      argString(args, "team_id"),
			Activity:    localizedToolActivity(lang, "teammate_list", ""),
		}
	case "teammate_shutdown":
		id := argString(args, "teammate_id")
		return toolPresentation{
			DisplayName: localizedToolLabel(lang, "teammate_shutdown"),
			Detail:      id,
			Activity:    localizedToolActivity(lang, "teammate_shutdown", id),
		}
	case "teammate_results":
		id := argString(args, "teammate_id")
		return toolPresentation{
			DisplayName: localizedToolLabel(lang, "teammate_results"),
			Detail:      displayToolTarget(util.FirstNonEmpty(id, argString(args, "team_id"))),
			Activity:    localizedToolActivity(lang, "teammate_results", id),
		}
	case "swarm_task_create":
		subject := argString(args, "subject")
		assignee := argString(args, "assignee")
		detail := subject
		if assignee != "" {
			detail = subject + " → " + assignee
		}
		return toolPresentation{
			DisplayName: localizedToolLabel(lang, "swarm_task_create"),
			Detail:      detail,
			Activity:    localizedToolActivity(lang, "swarm_task_create", detail),
		}
	case "swarm_task_claim":
		return toolPresentation{
			DisplayName: localizedToolLabel(lang, "swarm_task_claim"),
			Detail:      displayToolTarget(util.FirstNonEmpty(argString(args, "subject"), argString(args, "task_id"))),
			Activity:    localizedToolActivity(lang, "swarm_task_claim", ""),
		}
	case "swarm_task_complete":
		return toolPresentation{
			DisplayName: localizedToolLabel(lang, "swarm_task_complete"),
			Detail:      argString(args, "task_id"),
			Activity:    localizedToolActivity(lang, "swarm_task_complete", ""),
		}
	case "swarm_task_list":
		return toolPresentation{
			DisplayName: localizedToolLabel(lang, "swarm_task_list"),
			Detail:      "",
			Activity:    localizedToolActivity(lang, "swarm_task_list", ""),
		}
	case "list_mcp_capabilities":
		server := argString(args, "server")
		return toolPresentation{
			DisplayName: localizedToolLabel(lang, "list_mcp_capabilities"),
			Detail:      server,
			Activity:    localizedToolActivity(lang, "list_mcp_capabilities", ""),
		}
	case "get_mcp_prompt":
		name := argString(args, "name")
		return toolPresentation{
			DisplayName: localizedToolLabel(lang, "get_mcp_prompt"),
			Detail:      name,
			Activity:    localizedToolActivity(lang, "get_mcp_prompt", name),
		}
	case "read_mcp_resource":
		uri := argString(args, "uri")
		return toolPresentation{
			DisplayName: localizedToolLabel(lang, "read_mcp_resource"),
			Detail:      uri,
			Activity:    localizedToolActivity(lang, "read_mcp_resource", ""),
		}
	case "a2a_remote":
		target := argString(args, "target")
		return toolPresentation{
			DisplayName: localizedToolLabel(lang, "a2a_remote"),
			Detail:      target,
			Activity:    localizedToolActivity(lang, "a2a_remote", target),
		}
	case "a2a_discover":
		return toolPresentation{
			DisplayName: localizedToolLabel(lang, "a2a_discover"),
			Detail:      "",
			Activity:    localizedToolActivity(lang, "a2a_discover", ""),
		}
	case "a2a_send_task":
		target := argString(args, "target")
		return toolPresentation{
			DisplayName: localizedToolLabel(lang, "a2a_send_task"),
			Detail:      target,
			Activity:    localizedToolActivity(lang, "a2a_send_task", target),
		}
	case "a2a_get_task":
		return toolPresentation{
			DisplayName: localizedToolLabel(lang, "a2a_get_task"),
			Detail:      "",
			Activity:    localizedToolActivity(lang, "a2a_get_task", ""),
		}
	case "a2a_list_tasks":
		return toolPresentation{
			DisplayName: localizedToolLabel(lang, "a2a_list_tasks"),
			Detail:      "",
			Activity:    localizedToolActivity(lang, "a2a_list_tasks", ""),
		}
	case "a2a_cancel_task":
		return toolPresentation{
			DisplayName: localizedToolLabel(lang, "a2a_cancel_task"),
			Detail:      "",
			Activity:    localizedToolActivity(lang, "a2a_cancel_task", ""),
		}
	default:
		// LSP tools share a common pattern: show file:line
		if strings.HasPrefix(toolName, "lsp_") {
			return lspToolPresentation(lang, toolName, args, fileTarget)
		}
		pretty := prettifyToolName(toolName)
		return toolPresentation{
			DisplayName: pretty,
			Detail: displayToolTarget(util.FirstNonEmpty(
				fileTarget,
				argString(args, "command"),
				argString(args, "cmd"),
				displayToolFileTarget(argString(args, "path")),
				displayToolFileTarget(argString(args, "file_path")),
				argString(args, "pattern"),
				argString(args, "query"),
				argString(args, "url"),
				argString(args, "prompt"),
				argString(args, "input"),
				argString(args, "description"),
			)),
			Activity: localizedGenericActivity(lang, pretty),
		}
	}
}
