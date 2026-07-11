package mcp

import "strings"

// writeKeywords are lowercase substrings that indicate a tool performs
// write/create/delete/modify operations. When an MCP server is configured
// with read_only: true, tools whose names contain any of these keywords
// will be blocked from execution.
var writeKeywords = []string{
	"write",
	"edit",
	"delete",
	"remove",
	"create",
	"update",
	"insert",
	"set",
	"put",
	"post",
	"patch",
	"execute",
	"run",
	"exec",
	"shell",
	"move",
	"rename",
	"upload",
	"install",
	"deploy",
}

// isWriteToolName checks whether the given MCP tool name indicates a
// write-type operation (create, update, delete, execute, etc.).
// Returns true if the tool should be blocked in read-only mode.
func isWriteToolName(name string) bool {
	lower := strings.ToLower(name)
	for _, kw := range writeKeywords {
		if strings.Contains(lower, kw) {
			return true
		}
	}
	return false
}
