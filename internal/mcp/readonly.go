package mcp

import "strings"

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

func isWriteToolName(name string) bool {
	plain := strings.ToLower(name)
	segments := strings.Split(camelToSnake(name), "_")

	plainSegments := strings.FieldsFunc(plain, func(r rune) bool {
		return r == '_' || r == '-' || r == '.'
	})
	for _, kw := range writeKeywords {
		for i, seg := range segments {
			if seg == kw && !nounUseAllowed(kw, i, segments) {
				return true
			}
		}
		for i, seg := range plainSegments {
			if seg == kw && !nounUseAllowed(kw, i, plainSegments) {
				return true
			}
		}
	}
	return false
}

var readVerbRoots = map[string]bool{
	"get": true, "read": true, "list": true, "query": true, "fetch": true,
	"find": true, "describe": true, "show": true, "view": true,
	"search": true, "check": true, "status": true, "select": true,
	"lookup": true, "head": true, "count": true, "inspect": true,
}

func nounUseAllowed(kw string, idx int, segs []string) bool {
	if (kw != "post" && kw != "run") || idx == 0 {
		return false
	}
	return readVerbRoots[segs[idx-1]]
}
