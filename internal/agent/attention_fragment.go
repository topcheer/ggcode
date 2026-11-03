package agent

import (
	"fmt"
	"path/filepath"
	"strings"

	"github.com/topcheer/ggcode/internal/debug"
)

type attentionFragmentState struct {
	recentDirs  []string
	switchCount int
	windowSize  int
	warnCount   int
	totalCalls  int
}

const (
	afWindowSize      = 10
	afSwitchThreshold = 0.70
	afMinUniqueDirs   = 4
	afMaxWarnings     = 1
)

func newAttentionFragmentState() *attentionFragmentState {
	return &attentionFragmentState{
		windowSize: afWindowSize,
	}
}

func extractAFDir(pathStr string) string {
	if pathStr == "" {
		return ""
	}

	normalized := strings.ReplaceAll(pathStr, "\\", "/")

	dir := filepath.Dir(normalized)
	parts := strings.Split(dir, "/")
	var meaningful []string
	for _, p := range parts {
		if p == "" || p == "." {
			continue
		}
		meaningful = append(meaningful, p)
	}
	if len(meaningful) <= 2 {
		return strings.Join(meaningful, "/")
	}

	return strings.Join(meaningful[len(meaningful)-2:], "/")
}

func extractPathFromToolCall(_ string, args map[string]interface{}) string {

	pathKeys := []string{"file_path", "path", "source", "directory", "glob"}
	for _, key := range pathKeys {
		if val, ok := args[key]; ok {
			if s, ok := val.(string); ok && s != "" {
				return s
			}
		}
	}

	if files, ok := args["files"]; ok {
		switch v := files.(type) {
		case []interface{}:
			if len(v) > 0 {
				if item, ok := v[0].(map[string]interface{}); ok {
					if p, ok := item["path"].(string); ok {
						return p
					}
				}
			}
		case string:
			return v
		}
	}

	return ""
}

func isAFRelevantTool(name string) bool {
	switch name {
	case "read_file", "multi_file_read", "edit_file", "multi_edit_file",
		"write_file", "glob", "list_directory", "grep", "search_files",
		"code_search", "batch_replace",
		"lsp_definition", "lsp_references", "lsp_hover", "lsp_symbols",
		"lsp_diagnostics", "lsp_implementation", "lsp_document_highlights",
		"lsp_rename", "lsp_code_actions",
		"file_ops", "git_add", "git_diff", "git_show", "git_blame",
		"test_impact", "code_health", "dep_graph":
		return true
	default:
		return false
	}
}

func (s *attentionFragmentState) recordToolCall(name string, args map[string]interface{}) {
	if !isAFRelevantTool(name) {
		return
	}
	pathStr := extractPathFromToolCall(name, args)
	dir := extractAFDir(pathStr)
	if dir == "" {

		return
	}
	s.totalCalls++
	s.recentDirs = append(s.recentDirs, dir)
	if len(s.recentDirs) > s.windowSize {
		s.recentDirs = s.recentDirs[len(s.recentDirs)-s.windowSize:]
	}
}

func (s *attentionFragmentState) analyze() string {
	if s.warnCount >= afMaxWarnings {
		return ""
	}
	if len(s.recentDirs) < s.windowSize {
		return ""
	}

	switches := 0
	uniqueDirs := map[string]bool{}
	for i, d := range s.recentDirs {
		uniqueDirs[d] = true
		if i > 0 && d != s.recentDirs[i-1] {
			switches++
		}
	}
	pairs := len(s.recentDirs) - 1
	if pairs == 0 {
		return ""
	}
	switchDensity := float64(switches) / float64(pairs)

	if switchDensity < afSwitchThreshold {
		return ""
	}
	if len(uniqueDirs) < afMinUniqueDirs {
		return ""
	}

	s.warnCount++

	debug.Log("agent", "attention fragmentation detected: density=%.2f, unique_dirs=%d", switchDensity, len(uniqueDirs))

	return fmt.Sprintf(
		"[attention-fragment] Switching across %d directories in last %d calls. Group related operations before switching.",
		len(uniqueDirs), len(s.recentDirs),
	)
}

func (s *attentionFragmentState) reset() {
	s.recentDirs = s.recentDirs[:0]
	s.switchCount = 0
	s.warnCount = 0
}
