package tui

// describeMCPFamilyTool renders MCP capability/prompt/resource tools.
func describeMCPFamilyTool(lang Language, toolName string, args map[string]any, fileTarget string) toolPresentation {
	switch toolName {
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
	default:
		return describeUnknownFamilyTool(lang, toolName, args, fileTarget)
	}
}
