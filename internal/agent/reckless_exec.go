package agent

import (
	"encoding/json"
	"os"
)

import (
	"fmt"
	"path/filepath"
	"strconv"
	"strings"
)

const (
	recklessGraceIter = 8

	recklessUnexploredThreshold = 2

	recklessMaxWarnings = 2

	recklessMaxTracked = 40
)

type recklessExecState struct {
	readFiles  map[string]bool
	editFiles  map[string]bool
	unexplored int
	warnings   int
	iteration  int
}

func newRecklessExecState() *recklessExecState {
	return &recklessExecState{
		readFiles: make(map[string]bool),
		editFiles: make(map[string]bool),
	}
}

func (s *recklessExecState) reset() {
	s.readFiles = make(map[string]bool)
	s.editFiles = make(map[string]bool)
	s.unexplored = 0
	s.warnings = 0
	s.iteration = 0
}

func (s *recklessExecState) recordReadTool(toolName, args string) {
	if !recklessIsExplorationTool(toolName) {
		return
	}
	for _, path := range recklessExtractPaths(toolName, args) {
		key := recklessPathKey(path)
		if len(s.readFiles) < recklessMaxTracked {
			s.readFiles[key] = true
		}
	}
}

func (s *recklessExecState) recordEditTool(toolName, args string) bool {
	if !recklessIsEditTool(toolName) {
		return false
	}

	created := map[string]bool{}
	for _, p := range extractCreateFilePaths(toolName, json.RawMessage(args)) {
		created[recklessPathKey(p)] = true
	}

	for _, path := range recklessExtractPaths(toolName, args) {
		key := recklessPathKey(path)
		if s.editFiles[key] {
			continue
		}
		s.editFiles[key] = true

		if !s.wasExplored(key) && !created[key] {
			s.unexplored++
		}
	}

	if s.iteration > recklessGraceIter {
		return false
	}

	if s.unexplored >= recklessUnexploredThreshold && s.warnings < recklessMaxWarnings {
		s.warnings++
		return true
	}

	return false
}

func recklessWarning(unexplored int) string {
	return fmt.Sprintf("%s",
		"[reckless-exec] You have edited "+strconv.Itoa(unexplored)+
			" file(s) without reading or searching them first. "+
			"Editing files you haven't examined leads to incorrect "+
			"assumptions about content, missing imports, and breaking "+
			"changes. PAUSE and read the target files (read_file / "+
			"search_files) before making further edits.",
	)
}

func recklessIsExplorationTool(name string) bool {
	switch name {
	case "read_file", "multi_file_read", "search_files", "grep", "glob",
		"list_directory", "code_search", "lsp_definition", "lsp_references",
		"lsp_symbols", "lsp_hover", "lsp_workspace_symbols", "lsp_implementation",
		"lsp_incoming_calls", "lsp_outgoing_calls", "lsp_prepare_call_hierarchy",
		"git_show", "git_diff", "git_blame", "git_log":
		return true
	}
	return false
}

func recklessPathKey(p string) string {
	return filepath.Clean(strings.TrimSpace(p))
}

func (s *recklessExecState) wasExplored(path string) bool {
	if s.readFiles[path] {
		return true
	}
	match := ""
	for k := range s.readFiles {
		if recklessSuffixMatch(k, path) {
			if match != "" {
				return false
			}
			match = k
		}
	}
	return match != ""
}

func recklessSuffixMatch(a, b string) bool {
	if len(a) > len(b) {
		return strings.HasSuffix(a, b) && a[len(a)-len(b)-1] == os.PathSeparator
	}
	if len(b) > len(a) {
		return strings.HasSuffix(b, a) && b[len(b)-len(a)-1] == os.PathSeparator
	}
	return false
}

func recklessIsEditTool(name string) bool {
	return sourceMutatingTools[name]
}

func recklessExtractPaths(toolName, args string) []string {
	paths := recklessExtractFieldValues(args, []string{
		"file_path", "path", "notebook_path",
	})

	if toolName == "multi_edit_file" || toolName == "multi_file_write" ||
		toolName == "multi_file_read" || toolName == "batch_replace" {

		paths = append(paths, recklessExtractFieldValues(args, []string{"path"})...)
	}

	seen := make(map[string]bool, len(paths))
	result := make([]string, 0, len(paths))
	for _, p := range paths {
		p = strings.TrimSpace(p)
		if p == "" || seen[p] {
			continue
		}
		seen[p] = true
		result = append(result, p)
	}
	return result
}

func recklessExtractFieldValues(args string, fields []string) []string {
	var result []string
	for _, field := range fields {

		search := "\"" + field + "\":"
		idx := 0
		for {
			pos := strings.Index(args[idx:], search)
			if pos < 0 {
				break
			}
			pos += idx

			valStart := pos + len(search)

			for valStart < len(args) && (args[valStart] == ' ' || args[valStart] == '\t') {
				valStart++
			}
			if valStart >= len(args) {
				break
			}
			if args[valStart] == '"' {

				valStart++
				end := strings.Index(args[valStart:], "\"")
				if end < 0 {
					break
				}
				result = append(result, args[valStart:valStart+end])
				idx = valStart + end + 1
			} else {

				idx = valStart + 1
			}
		}
	}
	return result
}
