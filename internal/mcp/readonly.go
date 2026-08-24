package mcp

import "strings"

// writeKeywords are lowercase markers that indicate a tool performs
// write/create/delete/modify operations. When an MCP server is configured
// with read_only: true, tools whose names match any of these markers will
// be blocked from execution (#996).
//
// Matching is by underscore-delimited segment (see isWriteToolName):
// short roots like "set"/"put"/"run" must match a WHOLE segment, never a
// substring - "get_dataset", "asset_search", "offset_query", "output_stream",
// "truncate_rows" contain those roots inside unrelated words and are
// read-only queries that the old substring match wrongly blocked.
// Multi-word keywords without underscores (e.g. "upsert", "execute") match
// inside a segment, since real tool names rarely break them apart.
var writeKeywords = []string{
	"write",
	"edit",
	"delete",
	"remove",
	"create",
	"update",
	"insert",
	"upsert",
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

// containsShortRoot reports whether the keyword is a short root (<= 4
// chars) prone to false positives inside unrelated words ("set" in
// "dataset", "run" in "truncate", "put" in "output").
func containsShortRoot(kw string) bool { return len(kw) <= 4 }

// isWriteToolName checks whether the given MCP tool name indicates a
// write-type operation (create, update, delete, execute, etc.).
// Returns true if the tool should be blocked in read-only mode.
//
// The name is split on underscores; short-root keywords (<= 4 chars) match
// only a complete segment ("set_value" matches, "get_dataset" does not),
// longer keywords match inside segments ("upsert_value", "execute_query").
func isWriteToolName(name string) bool {
	segments := strings.Split(strings.ToLower(name), "_")
	for _, kw := range writeKeywords {
		if containsShortRoot(kw) {
			for _, seg := range segments {
				if seg == kw {
					return true
				}
			}
		} else if strings.Contains(strings.Join(segments, "_"), kw) {
			return true
		}
	}
	return false
}
