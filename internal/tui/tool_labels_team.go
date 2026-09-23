package tui

import (
	"fmt"

	"github.com/topcheer/ggcode/internal/util"
)

// describeTeamFamilyTool renders team/teammate/swarm task tools.
func describeTeamFamilyTool(lang Language, toolName string, args map[string]any, fileTarget string) toolPresentation {
	switch toolName {
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
		label := localizedToolLabel(lang, "teammate_spawn")
		if name != "" {
			label = fmt.Sprintf("%s: %s", label, name)
		}
		return toolPresentation{
			DisplayName: label,
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
	default:
		return describeUnknownFamilyTool(lang, toolName, args, fileTarget)
	}
}
