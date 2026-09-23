package tui

import (
	"strings"

	"github.com/topcheer/ggcode/internal/hooks"
	toolpkg "github.com/topcheer/ggcode/internal/tool"
	"github.com/topcheer/ggcode/internal/util"
)

type toolPresentation struct {
	DisplayName string
	Detail      string
	Activity    string
}

// spawnAgentModelResolver resolves the effective model shown in spawn_agent
// labels when the tool call omits the model param (the sub-agent then
// inherits the parent's runtime model — see spawn_agent.go's displayModel
// fallback). Wired by REPL.SetSubAgentManager; nil in tests and non-TUI
// frontends, preserving the explicit-model-only behavior there.
var spawnAgentModelResolver func() string

// namedAgentModelResolver resolves a named agent template's model override
// by name. Returns "" if the template has no model override. Wired by REPL.
var namedAgentModelResolver func(name string) string

type commandPreview struct {
	Title                  string
	CommandLines           []string
	CommandHiddenLineCount int
}

const maxPreviewLines = 5

// describeTool is the display dispatcher for tool calls. The universal
// priority block (description field, swarm subject) runs first, then routing
// per family: each describe<Family>FamilyTool in the tool_labels_*.go files
// owns one domain of the original switch and must return identical output
// for its tool names. Unlisted tools fall through to the generic renderer.
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

		// For spawn_agent, include the model name in brackets: "desc [model] (Spawn Agent)"
		if toolName == "spawn_agent" {
			model := argString(args, "model")
			if model == "" && spawnAgentModelResolver != nil {
				// No explicit override — the sub-agent inherits the parent's
				// runtime model, so show that instead of hiding the model.
				model = spawnAgentModelResolver()
			}
			if model != "" {
				displayName := desc + " [" + model + "] (" + pretty + ")"
				return toolPresentation{DisplayName: displayName, Detail: detail, Activity: desc + " [" + model + "]"}
			}
		}

		displayName := desc + " (" + pretty + ")"
		return toolPresentation{DisplayName: displayName, Detail: detail, Activity: desc}
	}

	switch toolName {
	case "read_file", "edit_file", "write_file", "glob", "grep", "search_files", "list_directory":
		return describeFileFamilyTool(lang, toolName, args, fileTarget)
	case "run_command", "bash", "powershell", "start_command":
		return describeCommandFamilyTool(lang, toolName, args, fileTarget)
	case "write_command_input", "read_command_output", "wait_command", "stop_command", "list_commands":
		return describeJobsFamilyTool(lang, toolName, args, fileTarget)
	case "web_fetch", "web_search":
		return describeWebFamilyTool(lang, toolName, args, fileTarget)
	case "git_status", "git_diff", "git_log", "git_show", "git_blame", "git_branch_list", "git_remote", "git_stash_list", "git_add", "git_commit", "git_stash":
		return describeGitFamilyTool(lang, toolName, args, fileTarget)
	case "sleep", "cron_create", "cron_delete", "cron_list", "cron_update", "cron_pause", "cron_resume", "cron_get":
		return describeCronFamilyTool(lang, toolName, args, fileTarget)
	case "enter_worktree", "exit_worktree", "save_memory", "delete_memory", "config", "send_message", "enter_plan_mode", "exit_plan_mode":
		return describeSessionFamilyTool(lang, toolName, args, fileTarget)
	case "todo_write", "task", "agent", "skill", "ask_user", "task_create", "task_update", "task_get", "task_list", "task_stop", "task_output":
		return describeTaskFamilyTool(lang, toolName, args, fileTarget)
	case "spawn_agent", "list_agents", "wait_agent", "use_namedagent", "create_namedagent", "delete_namedagent", "list_namedagent":
		return describeAgentFamilyTool(lang, toolName, args, fileTarget)
	case "team_create", "team_delete", "teammate_spawn", "teammate_list", "teammate_shutdown", "teammate_results", "swarm_task_create", "swarm_task_claim", "swarm_task_complete", "swarm_task_list":
		return describeTeamFamilyTool(lang, toolName, args, fileTarget)
	case "list_mcp_capabilities", "get_mcp_prompt", "read_mcp_resource":
		return describeMCPFamilyTool(lang, toolName, args, fileTarget)
	case "a2a_remote", "a2a_discover", "a2a_send_task", "a2a_get_task", "a2a_list_tasks", "a2a_cancel_task":
		return describeA2AFamilyTool(lang, toolName, args, fileTarget)
	default:
		return describeUnknownFamilyTool(lang, toolName, args, fileTarget)
	}
}

// describeUnknownFamilyTool renders tool names without a dedicated family
// case. Kept in this file as the dispatch terminal of describeTool.
func describeUnknownFamilyTool(lang Language, toolName string, args map[string]any, fileTarget string) toolPresentation {
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
