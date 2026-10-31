package subagent

// externalSourceTools are the network/peer tool channels whose results carry
// content from OUTSIDE the local workspace. Source axis (not risk axis):
// aligned with the tui provenance table and the injection guard's r406 peer
// channels. Used to surface a sub-agent result's provenance at the
// delegation boundary (wait_agent) - the trust-cascade defense (EMNLP 2026:
// delegation erases provenance markers, letting external content flow into
// the parent's context without attribution).
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
