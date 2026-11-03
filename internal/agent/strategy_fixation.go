package agent

import (
	"fmt"
	"path/filepath"
	"strings"
)

type strategyFixationState struct {
	fileEdits    map[string]int
	fileFailures map[string]int
	lastFile     string
	warnedFiles  map[string]bool
	warnCount    int
}

const (
	strategyFixationEditThreshold = 3
	strategyFixationFailThreshold = 2
	strategyFixationMaxTotalWarns = 1
)

func newStrategyFixationState() *strategyFixationState {
	return &strategyFixationState{
		fileEdits:    make(map[string]int),
		fileFailures: make(map[string]int),
		warnedFiles:  make(map[string]bool),
	}
}

func (s *strategyFixationState) reset() {
	s.fileEdits = make(map[string]int)
	s.fileFailures = make(map[string]int)
	s.lastFile = ""
	s.warnedFiles = make(map[string]bool)
	s.warnCount = 0
}

func (s *strategyFixationState) recordEdit(filePath string) {
	if filePath == "" {
		return
	}
	s.fileEdits[filePath]++
	s.lastFile = filePath
}

func sfCommandArg(args map[string]interface{}) string {
	if args == nil {
		return ""
	}
	if s, ok := args["command"].(string); ok {
		return s
	}
	return ""
}

func (s *strategyFixationState) recordVerification(toolName string, output string, isError bool) {
	if !isError {

		s.fileEdits = make(map[string]int)
		s.fileFailures = make(map[string]int)
		s.lastFile = ""
		return
	}

	if s.lastFile != "" {
		if sfOutputNamesFile(output, s.lastFile) {
			s.fileFailures[s.lastFile]++
		}
	}
}

func sfOutputNamesFile(output, filePath string) bool {
	fname := shortFileName(filePath)
	if fname == "" || output == "" {
		return false
	}
	dirBase := shortFileName(strings.TrimSuffix(filepath.ToSlash(filepath.Dir(filePath)), "/"))
	if dirBase == "." || dirBase == "/" {
		dirBase = ""
	}
	for idx := 0; ; {
		i := strings.Index(output[idx:], fname)
		if i < 0 {
			return false
		}
		i += idx

		start := i
		for start > 0 {
			c := output[start-1]
			if c == ' ' || c == '\t' || c == '\n' || c == '(' || c == '"' || c == '\'' || c == '`' {
				break
			}
			start--
		}
		seg := output[start:i]
		if seg == "" || seg == "." || seg == "./" {

			return true
		}

		trimmed := strings.TrimRight(seg, "/\\")
		dir := shortFileName(trimmed)
		if dir == "." || dir == "" {
			return true
		}
		if dirBase != "" && strings.EqualFold(dir, dirBase) {
			return true
		}

		idx = i + len(fname)
	}
}

func (s *strategyFixationState) check() string {
	if s.warnCount >= strategyFixationMaxTotalWarns {
		return ""
	}

	for file, editCount := range s.fileEdits {
		if editCount < strategyFixationEditThreshold {
			continue
		}
		if s.warnedFiles[file] {
			continue
		}
		failCount := s.fileFailures[file]
		if failCount < strategyFixationFailThreshold {
			continue
		}

		s.warnedFiles[file] = true
		s.warnCount++
		fname := shortFileName(file)
		return fmt.Sprintf(
			"[strategy-fixation] Edited %s %d times with %d failures. Approach not converging - re-read file or try different strategy.",
			fname, editCount, failCount,
		)
	}
	return ""
}

func shortFileName(path string) string {
	if path == "" {
		return ""
	}
	for i := len(path) - 1; i >= 0; i-- {
		if path[i] == '/' || path[i] == '\\' {
			return path[i+1:]
		}
	}
	return path
}

func strategyFixationIsMutation(toolName string) bool {
	return sourceMutatingTools[toolName]
}

func strategyFixationIsVerification(toolName string) bool {
	switch {
	case toolName == "run_command",
		toolName == "bash",
		toolName == "powershell",
		toolName == "wait_command",
		toolName == "read_command_output",
		toolName == "code_health",
		toolName == "review_changes",
		toolName == "verify",
		toolName == "lsp_diagnostics":
		return true
	default:
		return false
	}
}
