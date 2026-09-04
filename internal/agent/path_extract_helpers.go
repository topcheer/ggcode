package agent

import (
	"encoding/json"
	"path/filepath"
	"regexp"
	"strings"
)

// Path-extraction helpers preserved from wasted_explore.go (detector removed
// in guidance-noise cleanup batch 1). These remain referenced by verify_hint.go
// and wt_invalidation_check.go.

// extractFilePathsFromArgs extracts file paths from tool call arguments.
func extractFilePathsFromArgs(args json.RawMessage, _ string) []string {
	var raw map[string]interface{}
	if err := json.Unmarshal(args, &raw); err != nil {
		return nil
	}
	paths := extractStringKeys(raw, "path", "file_path", "file", "directory", "source", "notebook_path", "url")
	paths = append(paths, extractArrayPaths(raw)...)
	return dedupPaths(paths)
}

// extractStringKeys collects string values for the given keys from a map.
func extractStringKeys(raw map[string]interface{}, keys ...string) []string {
	var paths []string
	for _, key := range keys {
		val, ok := raw[key]
		if !ok {
			continue
		}
		str, ok := val.(string)
		if !ok {
			continue
		}
		// Skip trivial path values that don't represent a specific file
		if (key == "path" || key == "directory") && (str == "" || str == ".") {
			continue
		}
		paths = append(paths, str)
	}
	return paths
}

// extractArrayPaths collects paths from "files" and "paths" array parameters.
func extractArrayPaths(raw map[string]interface{}) []string {
	var paths []string
	for _, key := range []string{"files", "paths"} {
		val, ok := raw[key]
		if !ok {
			continue
		}
		arr, ok := val.([]interface{})
		if !ok {
			continue
		}
		paths = append(paths, extractItemsFromArray(arr)...)
	}
	return paths
}

// extractItemsFromArray extracts path strings from array items (maps or strings).
func extractItemsFromArray(arr []interface{}) []string {
	var paths []string
	for _, item := range arr {
		if m, ok := item.(map[string]interface{}); ok {
			for _, pk := range []string{"path", "file_path"} {
				if p, ok := m[pk].(string); ok {
					paths = append(paths, p)
				}
			}
		}
		if s, ok := item.(string); ok {
			paths = append(paths, s)
		}
	}
	return paths
}

// dedupPaths removes duplicate entries while preserving order.
func dedupPaths(paths []string) []string {
	seen := make(map[string]bool)
	var result []string
	for _, p := range paths {
		if !seen[p] {
			seen[p] = true
			result = append(result, p)
		}
	}
	return result
}

// weNormalizePath normalizes a path for comparison: strips "./" prefixes,
// Cleans, and folds separators. Preserved from wasted_explore.go (used by
// patch_exhaust.go and premature_leftover_test.go).
func weNormalizePath(p string) string {
	if p == "" {
		return ""
	}
	p = strings.TrimPrefix(p, "./")
	p = filepath.Clean(p)
	p = filepath.ToSlash(p)
	return p
}

// looksLikeFilePath reports whether s plausibly refers to a file path
// (has a known code-file extension, no spaces, not a URL). Preserved from
// wasted_explore.go; used by info_scent.go.
func looksLikeFilePath(s string) bool {
	if len(s) < 3 || len(s) > 500 {
		return false
	}
	if strings.HasPrefix(s, "http://") || strings.HasPrefix(s, "https://") {
		return false
	}
	if strings.Contains(s, " ") {
		return false
	}
	lower := strings.ToLower(s)
	exts := []string{".go", ".py", ".ts", ".js", ".tsx", ".jsx", ".rs", ".java",
		".c", ".cpp", ".h", ".hpp", ".rb", ".php", ".swift", ".kt", ".scala",
		".json", ".yaml", ".yml", ".toml", ".xml", ".html", ".css", ".scss",
		".md", ".txt", ".sh", ".sql", ".proto", ".dart", ".vue", ".svelte"}
	for _, ext := range exts {
		if strings.HasSuffix(lower, ext) {
			return true
		}
	}
	// Module-like dotted path relative path returned by search tools
	// #1467-A: the bare len(parts)>=2 fallback classified ANY dotted token
	// as a path - fmt.Println, os.Getenv, method calls, field selectors -
	// and read_file full-text feeds this pipeline, so healthy deep reads
	// inflated allSeenPaths with stdlib symbols and tripped 'stop
	// exploring' guidance on the 3rd NEW file. A dotted path must now
	// have a real path shape: a slash in the first segment (dir/file.go
	// style) or a lowercase-single-dotted module path with a known file
	// extension.
	parts := strings.Split(s, ".")
	if len(parts) >= 2 && len(parts[0]) >= 1 {
		if strings.Contains(parts[0], "/") {
			return true
		}
		// #1577-B: the extension sub-loop here was dead code - any token
		// ending in one of the 9 whitelist extensions was already matched
		// by the 32-extension HasSuffix loop above, so this branch could
		// never fire. Removed; the only live rule is the first-segment
		// slash (dir/file.go module paths).
	}
	return false
}

// searchTools are tools whose results may be "empty" (no matches, no files).
var searchTools = map[string]bool{
	"grep":         true,
	"glob":         true,
	"search_files": true,
	"code_search":  true,
	"git_log":      true,
	"git_show":     true,
	"git_blame":    true,
	"git_diff":     true,
}

// emptyResultPatterns are substrings that indicate a tool returned no useful data.
var emptyResultPatterns = []string{
	"no matches found",
	"no files found",
	"no results",
	"no commits found",
	"nothing found",
	"no matching",
	"0 matches",
	"0 results",
	"0 files",
	"no changes",
	"nothing to show",
}

// isEmptyResult checks if a tool result content indicates an empty/no-data
// response. Only called for search/query tools.
func isEmptyResult(content string) bool {
	if strings.TrimSpace(content) == "" {
		return true
	}
	// Short results (< 200 chars) are likely single-line summaries like
	// "No matches found." Long results with actual data won't match.
	if len(content) > 500 {
		return false
	}
	lower := strings.ToLower(content)
	for _, pat := range emptyResultPatterns {
		if strings.Contains(lower, pat) {
			return true
		}
	}
	return false
}

// wePathsMatch reports whether a consumption path covers a found path,
// accepting the full normalized form or the base name when the base is
// distinctive. Preserved from wasted_explore.go (premature_leftover_test).
func wePathsMatch(consumed, found string) bool {
	cn, fn := weNormalizePath(consumed), weNormalizePath(found)
	if cn == fn {
		return true
	}
	cb, fb := filepath.Base(cn), filepath.Base(fn)
	if cb == fb && cb != "/" && cb != "." && len(cb) > 4 && strings.Contains(cb, ".") {
		return true
	}
	return false
}

// extractPathFromLine extracts a file path from a single search-output line
// (e.g. "path/to/file.go:42: content"). Preserved from wasted_explore.go.
func extractPathFromLine(line string) string {
	line = strings.TrimPrefix(line, "File: ")
	line = strings.TrimPrefix(line, "→ ")
	line = strings.TrimPrefix(line, "- ")
	line = listNumPrefixRe.ReplaceAllString(line, "")

	idx := 0
	for idx < len(line) {
		ch := line[idx]
		if ch == ' ' || ch == '\t' {
			break
		}
		if ch == ':' && idx+1 < len(line) && line[idx+1] >= '0' && line[idx+1] <= '9' {
			break
		}
		idx++
	}

	path := strings.TrimSuffix(line[:idx], ":")
	return path
}

// listNumPrefixRe matches the "N. " enumerator prefix of code_search
// results (preserved from wasted_explore.go).
var listNumPrefixRe = regexp.MustCompile(`^\s*\d+\.\s+`)
