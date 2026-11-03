package agent

import (
	"encoding/json"
	"path/filepath"
	"regexp"
	"strings"
)

func extractFilePathsFromArgs(args json.RawMessage, _ string) []string {
	var raw map[string]interface{}
	if err := json.Unmarshal(args, &raw); err != nil {
		return nil
	}
	paths := extractStringKeys(raw, "path", "file_path", "file", "notebook_path")
	paths = append(paths, extractArrayPaths(raw)...)
	return dedupPaths(paths)
}

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

		if (key == "path" || key == "directory") && (str == "" || str == ".") {
			continue
		}
		paths = append(paths, str)
	}
	return paths
}

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

func weNormalizePath(p string) string {
	if p == "" {
		return ""
	}
	p = strings.TrimPrefix(p, "./")
	p = filepath.Clean(p)
	p = filepath.ToSlash(p)
	return p
}

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

	parts := strings.Split(s, ".")
	if len(parts) >= 2 && len(parts[0]) >= 1 {
		if strings.Contains(parts[0], "/") {
			return true
		}

	}
	return false
}

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

var emptyResultPatterns = []string{
	"no matches found",
	"no files found",
	"no results",
	"no commits found",
	"nothing found",
	"no matching",

	"found 0",

	"no files matched",
	"no differences found.",
	"no output.",
	"no changes",
	"nothing to show",
}

func isEmptyResult(content string) bool {
	if strings.TrimSpace(content) == "" {
		return true
	}

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

var listNumPrefixRe = regexp.MustCompile(`^\s*\d+\.\s+`)
