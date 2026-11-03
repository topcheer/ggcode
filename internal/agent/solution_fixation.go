package agent

import (
	"encoding/json"
	"fmt"
	"sort"
	"strings"
)

const (
	fixationThreshold   = 3
	fixationWindow      = 12
	maxFixationWarnings = 2
)

type fixationEntry struct {
	filePaths []string
	isError   bool
}

type solutionFixationState struct {
	recentCalls  []fixationEntry
	failedByFile map[string]int
	warningCount int
	firedFor     map[string]bool
}

func newSolutionFixationState() *solutionFixationState {
	return &solutionFixationState{
		recentCalls:  make([]fixationEntry, 0, fixationWindow+1),
		failedByFile: make(map[string]int),
		firedFor:     make(map[string]bool),
	}
}

func (s *solutionFixationState) reset() {
	s.recentCalls = s.recentCalls[:0]
	s.failedByFile = make(map[string]int)
	s.warningCount = 0
	s.firedFor = make(map[string]bool)
}

var agentMutationEditTools = map[string]bool{
	"edit_file":        true,
	"write_file":       true,
	"multi_edit_file":  true,
	"multi_file_edit":  true,
	"multi_file_write": true,
	"notebook_edit":    true,
	"batch_replace":    true,
	"lsp_rename":       true,

	"file_ops": true,
}

func isAgentMutationEditTool(name string) bool {
	return agentMutationEditTools[name]
}

func extractFilePathsFromEditArgs(args string) []string {
	args = strings.TrimSpace(args)
	if args == "" {
		return nil
	}

	type editArgs struct {
		Path     string `json:"path"`
		FilePath string `json:"file_path"`
		Notebook string `json:"notebook_path"`
		Files    []struct {
			Path string `json:"path"`
		} `json:"files"`
		Operations []struct {
			Action      string `json:"action"`
			Source      string `json:"source"`
			Destination string `json:"destination"`
		} `json:"operations"`
	}

	var paths []string
	add := func(p string) {
		if np := normalizePathFixation(p); np != "" {
			paths = append(paths, np)
		}
	}

	var parsed editArgs
	if err := json.Unmarshal([]byte(args), &parsed); err == nil {
		add(parsed.FilePath)
		add(parsed.Path)
		add(parsed.Notebook)
		for _, f := range parsed.Files {
			add(f.Path)
		}

		for _, op := range parsed.Operations {
			add(op.Source)
			add(op.Destination)
		}
		if len(paths) > 0 {
			return dedupePathsFixation(paths)
		}
	}

	for _, field := range []string{"file_path", "path", "notebook_path"} {
		if v := extractJSONStringFieldFixation(args, field); v != "" {
			add(v)
		}
	}
	return dedupePathsFixation(paths)
}

func dedupePathsFixation(paths []string) []string {
	if len(paths) <= 1 {
		return paths
	}
	seen := make(map[string]bool, len(paths))
	out := paths[:0]
	for _, p := range paths {
		if seen[p] {
			continue
		}
		seen[p] = true
		out = append(out, p)
	}
	return out
}

func extractJSONStringFieldFixation(jsonStr, field string) string {
	needle := `"` + field + `"`
	idx := strings.Index(jsonStr, needle)
	if idx < 0 {
		return ""
	}
	rest := jsonStr[idx+len(needle):]
	for len(rest) > 0 && (rest[0] == ' ' || rest[0] == ':' || rest[0] == '\t' || rest[0] == '\n') {
		rest = rest[1:]
	}
	if len(rest) == 0 || rest[0] != '"' {
		return ""
	}
	rest = rest[1:]
	end := strings.IndexByte(rest, '"')
	if end < 0 {
		return ""
	}
	return rest[:end]
}

func normalizePathFixation(p string) string {
	p = strings.TrimSpace(p)
	if p == "" {
		return ""
	}

	p = strings.ReplaceAll(p, "\\", "/")

	for strings.HasPrefix(p, "./") {
		p = p[2:]
	}
	for strings.Contains(p, "//") {
		p = strings.ReplaceAll(p, "//", "/")
	}
	p = strings.TrimRight(p, "/")
	if p == "" || p == "." {
		return ""
	}

	if p == "/" {
		return ""
	}
	return p
}

func (s *solutionFixationState) recordToolCall(toolName, args string, isError bool) {
	var paths []string
	if isAgentMutationEditTool(toolName) {
		paths = extractFilePathsFromEditArgs(args)
	}

	entry := fixationEntry{filePaths: paths, isError: isError}
	s.recentCalls = append(s.recentCalls, entry)

	if isError {
		for _, p := range paths {
			s.failedByFile[p]++
		}
	}

	if len(s.recentCalls) > fixationWindow {
		old := s.recentCalls[0]
		s.recentCalls = s.recentCalls[1:]
		if old.isError {
			for _, p := range old.filePaths {
				s.failedByFile[p]--
				if s.failedByFile[p] <= 0 {
					delete(s.failedByFile, p)
				}
			}
		}
	}
}

func (s *solutionFixationState) checkAndWarn() string {
	if s.warningCount >= maxFixationWarnings {
		return ""
	}

	var worstFiles []string
	worstCount := 0
	for f, c := range s.failedByFile {
		if s.firedFor[f] {
			continue
		}
		if c > worstCount {
			worstCount = c
			worstFiles = []string{f}
		} else if c == worstCount && worstCount > 0 {

			worstFiles = append(worstFiles, f)
		}
	}

	if worstCount < fixationThreshold {
		return ""
	}

	sort.Strings(worstFiles)
	for _, f := range worstFiles {
		s.firedFor[f] = true
	}
	s.warningCount++

	quoted := make([]string, len(worstFiles))
	for i, f := range worstFiles {
		quoted[i] = fmt.Sprintf("%q", f)
	}
	targetList := strings.Join(quoted, ", ")

	msg := "[Solution Fixation Alert] %d failed edit attempts have targeted %s. " +
		"You appear anchored on the hypothesis that the root cause is in this file, " +
		"but repeated failures strongly suggest the diagnosis is wrong. " +
		"STEP BACK: reconsider the root-cause analysis from scratch. " +
		"Ask: (1) Is the bug actually triggered from a different caller or upstream module? " +
		"(2) Could the error be environmental (config, dependency, env var) rather than code? " +
		"(3) Should you add diagnostic logging or print the actual error to verify your assumption? " +
		"(4) If the failures are 'old_text not found' style, the file changed since your last read - " +
		"re-read the file and refresh your edit anchor instead of abandoning the hypothesis. " +
		"Do not make another edit to %s until you have gathered new evidence."

	return fmt.Sprintf(msg, worstCount, targetList, strings.Join(worstFiles, " or "))
}
