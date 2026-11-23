package agent

// Shared helpers that used to live in guidance-detector files removed in the
// #5940 follow-up trim. They are generic (path extraction, tag parsing, tool
// sets) and still have non-guidance consumers.

import (
	"encoding/json"
	"regexp"
	"strings"
)

func extractStringField(args json.RawMessage, field string) string {
	var m map[string]interface{}
	if err := json.Unmarshal(args, &m); err != nil {
		return ""
	}
	v, ok := m[field]
	if !ok {
		return ""
	}
	str, ok := v.(string)
	if !ok {
		return ""
	}
	return str
}

func extractCreateFilePaths(toolName string, args json.RawMessage) []string {
	if len(args) == 0 {
		return nil
	}
	var m map[string]any
	if json.Unmarshal(args, &m) != nil {
		return nil
	}
	m = normalizeApplyPatchArgs(toolName, m) // #5321: nested apply_patch create_file must register too
	switch toolName {
	case "write_file":
		if p, ok := m["path"].(string); ok {
			return []string{p}
		}
	case "apply_patch": // #5249: create_file targets a brand-new file - exempt from the
		// unread-edit nag exactly like write_file (recordCreated parity); update_file
		// and delete_file keep returning nothing so the guard still applies to them.
		if t, _ := m["type"].(string); t == "create_file" {
			if p, ok := m["path"].(string); ok {
				return []string{p}
			}
		}
	case "multi_file_write":
		if files, ok := m["files"].([]any); ok {
			var paths []string
			for _, f := range files {
				if fm, ok := f.(map[string]any); ok {
					if p, ok := fm["path"].(string); ok {
						paths = append(paths, p)
					}
				}
			}
			return paths
		}

	}
	return nil
}

func extractTrackedEditPaths(toolName string, args json.RawMessage) []string {
	return extractEditPaths(toolName, args, true)
}

func extractNewCommitHash(content string) string {
	m := newCommitHashRegex.FindStringSubmatch(content)
	if m == nil {
		return ""
	}
	return m[1]
}

func extractReadFilePaths(toolName string, args json.RawMessage) []string {
	if len(args) == 0 {
		return nil
	}
	var m map[string]any
	if json.Unmarshal(args, &m) != nil {
		return nil
	}
	switch toolName {
	case "read_file":
		if p, ok := m["path"].(string); ok {
			return []string{p}
		}
	case "multi_file_read":
		if files, ok := m["files"].([]any); ok {
			var paths []string
			for _, f := range files {
				if fm, ok := f.(map[string]any); ok {
					if p, ok := fm["path"].(string); ok {
						paths = append(paths, p)
					}
				}
			}
			return paths
		}
	}
	return nil
}

func itoaPivot(n int) string {
	if n == 0 {
		return "0"
	}
	neg := n < 0
	if neg {
		n = -n
	}
	var b [20]byte
	i := len(b)
	for n > 0 {
		i--
		b[i] = byte('0' + n%10)
		n /= 10
	}
	if neg {
		i--
		b[i] = '-'
	}
	return string(b[i:])
}

func dirSignature(dir string) string {

	wasAbs := strings.HasPrefix(dir, "/") || strings.HasPrefix(dir, "\\") || (len(dir) >= 2 && dir[1] == ':')
	dir = strings.TrimLeft(dir, "/\\")
	if len(dir) >= 2 && dir[1] == ':' {
		dir = dir[2:]
	}
	parts := strings.Split(dir, "/")

	clean := parts[:0]
	for _, p := range parts {
		if p != "" {
			clean = append(clean, p)
		}
	}
	if len(clean) <= 1 {
		return dir
	}
	if wasAbs && len(clean) > 2 {
		return clean[len(clean)-2] + "/" + clean[len(clean)-1]
	}
	return clean[0] + "/" + clean[1]
}

var productiveEditTools = sourceMutatingTools

var mutatingToolNamesFrag = derivedEditTools(map[string]bool{
	"run_command":   true,
	"start_command": true,
	"git_commit":    true,
	"git_add":       true,
	"git_checkout":  true,
})

var qcActionTools = derivedEditTools(map[string]bool{
	"run_command": true,
	"git_commit":  true,
})

func extractHintTag(hint string) string {
	m := hintTagRe.FindStringSubmatch(hint)
	if len(m) < 2 {
		return ""
	}
	return strings.TrimSpace(m[1])
}

func isCriticalTag(tag string) bool {
	if tag == "" {
		return false
	}
	return criticalHintTagsLower[strings.ToLower(tag)]
}

func stripCoalescedSummaries(hints []string) []string {
	filtered := make([]string, 0, len(hints))
	for _, h := range hints {
		if strings.ToLower(extractHintTag(h)) == "guidance-coalesced" {
			continue
		}
		filtered = append(filtered, h)
	}
	return filtered
}

func normalizeErrorLine(line string) string {
	s := line

	s = lineColPattern.ReplaceAllString(s, ":N")

	s = lineNumberPattern.ReplaceAllString(s, ":N")

	s = lineWordPattern.ReplaceAllString(s, "line N")

	s = stripPathToBasename(s)

	s = wsPattern.ReplaceAllString(s, " ")
	s = strings.TrimSpace(s)

	return s
}

var (
	errorLineMarkers = []string{
		"error", "error[", "error:", "error e",
		"fail", "fail:", "--- fail", "panic:",
		"undefined", "cannot find", "not defined",
		"expected", "mismatch", "incompatible",
		"fatal", "traceback", "exception",
		"nameerror", "typeerror", "valueerror",
		"syntaxerror", "attributeerror",
	}

	lineColPattern    = regexp.MustCompile(`:\d+:\d+`)
	lineNumberPattern = regexp.MustCompile(`:\d+(?::\d+)?`)
	lineWordPattern   = regexp.MustCompile(`(?i)\bline\s+\d+`)
	pathPrefixPattern = regexp.MustCompile(`^(?:\.\./+|\./+|/+)+`)
	wsPattern         = regexp.MustCompile(`\s+`)
)

func strategyFixationIsMutation(toolName string) bool {
	return sourceMutatingTools[toolName]
}

func foresightResultFailureSignal(content string, isError bool) bool {
	if isError {
		return true
	}
	first := firstContentLine(content)
	if first == "" {
		return false
	}
	return foresightResultFailureRe.MatchString(first)
}

func foresightResultEmpty(content string) bool {
	trimmed := strings.TrimSpace(content)
	if trimmed == "" {
		return true
	}
	// Line-aware (#5023): with (?m) anchors, any line that is an explicit
	// empty-signal ("No matches found", "0 results", ...) marks the result as
	// empty even inside multi-line output (e.g. "searched 12 files\nNo matches
	// found"), so a declared >=1_match no longer passes with zero matches.
	return foresightResultEmptyRe.MatchString(trimmed)
}

func extractEditPaths(toolName string, args json.RawMessage, includeFileOps bool) []string {
	if len(args) == 0 {
		return nil
	}
	var m map[string]any
	if json.Unmarshal(args, &m) != nil {
		return nil
	}
	m = normalizeApplyPatchArgs(toolName, m) // #5321: nested {"operation":{...}} must not bypass guards
	if includeFileOps && toolName == "file_ops" {
		return extractFileOpsPaths(m)
	}
	switch toolName {
	case "apply_patch": // #5244 top-level path; #5321 nested form normalized above
		return extractSinglePath(m, "path")
	case "edit_file":
		return extractSinglePath(m, "file_path", "path")
	case "multi_edit_file":
		return extractSinglePath(m, "file_path")
	case "multi_file_edit":
		return extractObjectArrayPaths(m)
	case "batch_replace":
		return extractStringArrayPaths(m)
	case "write_file":
		return extractSinglePath(m, "path")
	case "multi_file_write":
		return extractObjectArrayPaths(m)
	case "lsp_rename":
		return extractSinglePath(m, "path")
	case "notebook_edit":
		return extractSinglePath(m, "notebook_path")
	}
	// file_ops reaches here only in the guard view (includeFileOps=false):
	// its delete/move/mkdir operations are not content edits, so the
	// unread-edit guard ("you are editing without reading") does not apply
	// - it was misfiring twice on every batch delete (delete paths are
	// never previously read). The tracker view handled it above.

	return nil
}

func firstContentLine(content string) string {
	for _, ln := range strings.Split(content, "\n") {
		if t := strings.TrimSpace(ln); t != "" {
			return t
		}
	}
	return ""
}

func stripPathToBasename(s string) string {
	return pathPrefixPattern.ReplaceAllString(s, "")
}

func extractFileOpsPaths(m map[string]any) []string {
	ops, ok := m["operations"].([]any)
	if !ok {
		return nil
	}
	var out []string
	for _, op := range ops {
		o, ok := op.(map[string]any)
		if !ok {
			continue
		}
		action, _ := o["action"].(string)
		switch action {
		case "move":
			if d, _ := o["destination"].(string); d != "" {
				out = append(out, d)
			}
			if src, _ := o["source"].(string); src != "" {
				out = append(out, src)
			}
		case "delete":
			if src, _ := o["source"].(string); src != "" {
				out = append(out, src)
			}
		}
	}
	return out
}

func extractObjectArrayPaths(m map[string]any) []string {
	files, ok := m["files"].([]any)
	if !ok {
		return nil
	}
	var paths []string
	for _, f := range files {
		if fm, ok := f.(map[string]any); ok {
			if p, ok := fm["path"].(string); ok {
				paths = append(paths, p)
			}
		}
	}
	return paths
}

func extractSinglePath(m map[string]any, keys ...string) []string {
	for _, k := range keys {
		if p, ok := m[k].(string); ok {
			return []string{p}
		}
	}
	return nil
}

func extractStringArrayPaths(m map[string]any) []string {
	files, ok := m["files"].([]any)
	if !ok {
		return nil
	}
	var paths []string
	for _, f := range files {
		if s, ok := f.(string); ok {
			paths = append(paths, s)
		}
	}
	return paths
}

var newCommitHashRegex = regexp.MustCompile(`\[[^\]]*?([0-9a-f]{7,40})\]`)

var hintTagRe = regexp.MustCompile(`^\s*\[([A-Za-z][A-Za-z0-9 _:-]*?)\]`)

var criticalHintTagsLower = func() map[string]bool {
	m := make(map[string]bool, len(criticalHintTags))
	for k := range criticalHintTags {
		m[strings.ToLower(k)] = true
	}
	return m
}()

var foresightResultFailureRe = regexp.MustCompile(`(?im)^(?:exit\s+code\s+[1-9]|exit\s+status\s+[1-9]|status[:\s]+(?:4\d\d|5\d\d)|panic:|fatal:|traceback\s+\(|compilation\s+failed|tool_result_error)`)

var foresightResultEmptyRe = regexp.MustCompile(`(?im)^(?:no\s+(?:results?|matches?|content|output|data)\s+found$|(?:is\s+)?empty$|0\s+(?:bytes|lines|matches|results)$|nothing\s+(?:found|returned|matched)$)`)

var criticalHintTags = map[string]bool{
	"CRITICAL":              true,
	"PERMISSION":            true,
	"IRREVERSIBLE":          true,
	"DESTRUCTIVE":           true,
	"SAFETY":                true,
	"SECURITY":              true,
	"BLOCKED":               true,
	"FATAL":                 true,
	"pre-commit-build-gate": true,
	"hardcoded-secret":      true,
	"path-traversal":        true,
	"git-destructive":       true,
	"non-atomic":            true,
	"read-only":             true,
}
