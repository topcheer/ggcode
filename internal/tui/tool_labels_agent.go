package tui

import "fmt"

// describeAgentFamilyTool renders sub-agent and named agent template tools.
func describeAgentFamilyTool(lang Language, toolName string, args map[string]any, fileTarget string) toolPresentation {
	switch toolName {
	case "spawn_agent":
		task := argString(args, "task")
		desc := argString(args, "description")
		model := argString(args, "model")
		name := desc
		if name == "" {
			name = toolLabelFor(lang, "spawn_agent")
		}
		if model != "" {
			name = name + " [" + model + "]"
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
	case "use_namedagent":
		name := argString(args, "name")
		task := argString(args, "task")
		label := localizedToolLabel(lang, "use_namedagent")
		if name != "" {
			label = fmt.Sprintf("%s: %s", label, name)
		}
		// Resolve model: try template's configured model first, then parent agent's model.
		if namedAgentModelResolver != nil {
			if m := namedAgentModelResolver(name); m != "" {
				label += " [" + m + "]"
			}
		}
		return toolPresentation{
			DisplayName: label,
			Detail:      compactSingleLine(task),
			Activity:    label,
		}
	case "create_namedagent":
		name := argString(args, "name")
		label := localizedToolLabel(lang, "create_namedagent")
		if name != "" {
			label = fmt.Sprintf("%s: %s", label, name)
		}
		return toolPresentation{
			DisplayName: label,
			Detail:      "",
			Activity:    label,
		}
	case "delete_namedagent":
		name := argString(args, "name")
		label := localizedToolLabel(lang, "delete_namedagent")
		if name != "" {
			label = fmt.Sprintf("%s: %s", label, name)
		}
		return toolPresentation{
			DisplayName: label,
			Detail:      "",
			Activity:    label,
		}
	case "list_namedagent":
		label := localizedToolLabel(lang, "list_namedagent")
		return toolPresentation{
			DisplayName: label,
			Detail:      "",
			Activity:    label,
		}
	default:
		return describeUnknownFamilyTool(lang, toolName, args, fileTarget)
	}
}
