package tui

// describeWebFamilyTool renders web fetch/search tools.
func describeWebFamilyTool(lang Language, toolName string, args map[string]any, fileTarget string) toolPresentation {
	switch toolName {
	case "web_fetch":
		fetchTarget := displayToolTarget(argString(args, "url"))
		return toolPresentationFor(lang, "fetch", fetchTarget)
	case "web_search":
		searchTarget := displayToolTarget(argString(args, "query"))
		return toolPresentationFor(lang, "search", searchTarget)
	default:
		return describeUnknownFamilyTool(lang, toolName, args, fileTarget)
	}
}
