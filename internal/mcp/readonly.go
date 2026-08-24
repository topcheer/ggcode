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

// camelToSnake inserts underscores at uppercase boundaries so camelCase
// write names ("setValue") segment-match the same roots as their snake_case
// twins ("set_value") - #997. Lowercases as it goes. Read names split
// harmlessly ("getDataset" -> "get_dataset", "PostgresQuery" ->
// "postgres_query" - no short-root segment), so this does not reintroduce
// the #996 false-positive class. ALL-CAPS names fragment per letter
// ("DELETE_FILE" -> "d_e_l_e_t_e_..."), which is why isWriteToolName
// additionally substring-checks long keywords against the plain-lowercased
// name (#998) - the fragmenting alone would let "DELETE_FILE" through
// while "delete_file" stays blocked.
func camelToSnake(s string) string {
	var b strings.Builder
	for i, r := range s {
		if r >= 'A' && r <= 'Z' {
			if i > 0 {
				b.WriteByte('_')
			}
			b.WriteRune(r + ('a' - 'A'))
		} else {
			b.WriteRune(r)
		}
	}
	return b.String()
}

// isWriteToolName checks whether the given MCP tool name indicates a
// write-type operation (create, update, delete, execute, etc.).
// Returns true if the tool should be blocked in read-only mode.
//
// The name is camel-normalized then split on underscores; short-root
// keywords (<= 4 chars) match only a complete segment in EITHER the
// camel-normalized segments or the plain-lowercased segments: camel
// catches "setValue", plain catches underscored ALL-CAPS twins like
// "SET_VALUE" (-> "set_value" -> [set value]) that camelToSnake would
// fragment per letter, while "GET_DATASET" ([get dataset]) stays allowed
// - the #996 root-collision class is immune because the match is still
// whole-segment, never substring. Long keywords match inside segments
// AND against the plain-lowercased name, catching ALL-CAPS REST-style
// tools ("DELETE_FILE", "EXECUTE_QUERY") that fragment under
// camelToSnake (#998).
func isWriteToolName(name string) bool {
	plain := strings.ToLower(name)
	segments := strings.Split(camelToSnake(name), "_")
	plainSegments := strings.Split(plain, "_")
	for _, kw := range writeKeywords {
		if containsShortRoot(kw) {
			for _, seg := range segments {
				if seg == kw {
					return true
				}
			}
			for _, seg := range plainSegments {
				if seg == kw {
					return true
				}
			}
		} else if strings.Contains(strings.Join(segments, "_"), kw) || strings.Contains(plain, kw) {
			return true
		}
	}
	return false
}
