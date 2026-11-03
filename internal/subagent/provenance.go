package subagent

var externalSourceTools = map[string]bool{
	"web_fetch":           true,
	"web_search":          true,
	"browser":             true,
	"read_mcp_resource":   true,
	"lanchat":             true,
	"send_message":        true,
	"a2a_remote":          true,
	"send_file_to_mobile": true,
}

func isExternalSourceTool(name string) bool {
	return externalSourceTools[name]
}
