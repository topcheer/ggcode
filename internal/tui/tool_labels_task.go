package tui

import "github.com/topcheer/ggcode/internal/util"

// describeTaskFamilyTool renders todo/task tracking and interaction tools.
func describeTaskFamilyTool(lang Language, toolName string, args map[string]any, fileTarget string) toolPresentation {
	switch toolName {
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
	case "task_create", "task_update", "task_get", "task_list", "task_stop", "task_output":
		return toolPresentationFor(lang, "task", displayToolTarget(util.FirstNonEmpty(
			argString(args, "subject"),
			argString(args, "description"),
			argString(args, "taskId"),
		)))
	default:
		return describeUnknownFamilyTool(lang, toolName, args, fileTarget)
	}
}
