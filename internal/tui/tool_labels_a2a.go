package tui

// describeA2AFamilyTool renders agent-to-agent protocol tools.
func describeA2AFamilyTool(lang Language, toolName string, args map[string]any, fileTarget string) toolPresentation {
	switch toolName {
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
		return describeUnknownFamilyTool(lang, toolName, args, fileTarget)
	}
}
