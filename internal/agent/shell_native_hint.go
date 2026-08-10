package agent

// Shell-to-Native Tool Suggestions - Smart Tool Selection
//
// Trend: Claude Code, Cursor, and Cline all steer agents toward using native
// tools instead of raw shell commands when a better tool exists. When an agent
// uses run_command to do something a native tool does better (e.g., cat file
// instead of read_file, grep in shell instead of the grep tool, git log
// instead of git_log), the output lacks line numbers, structured metadata,
// deduplication, memoization, and other tool-specific enhancements.
//
// Gap in ggcode: the agent could use cat foo.go via run_command and get raw
// text without line numbers - making subsequent edit_file calls harder because
// the agent can't reference line numbers. Or use grep -rn pattern in shell and
// lose the structured output, glob filtering, and context lines the grep tool
// provides.
//
// This module inspects run_command arguments and, when it detects a shell command
// that duplicates a native tool, appends a short hint suggesting the native tool.
// The hint fires at most once per pattern per run to avoid nagging.
//
// Design:
//   - Only triggers on run_command / start_command tool calls
//   - Pattern matching on the command string, not on output
//   - Hints are brief (1 line) and suggest a concrete native tool
//   - Each hint pattern fires at most once per run
//   - Non-blocking: the command still executes

import (
	"regexp"
	"strings"
	"sync"
)

// shellCommandToolNames are the tools that execute raw shell commands.
var shellCommandToolNames = map[string]bool{
	"run_command":   true,
	"start_command": true,
}

// shellNativePattern defines a shell command pattern and its native tool suggestion.
type shellNativePattern struct {
	regex      *regexp.Regexp
	suggestion string
}

// shellNativePatterns maps shell command patterns to native tool suggestions.
// Patterns are checked in order; first match wins.
var shellNativePatterns = []shellNativePattern{
	{
		regex:      regexp.MustCompile(`^\s*(cat|head|tail)\s+`),
		suggestion: "[shell-hint] Use read_file tool instead.",
	},
	{
		regex:      regexp.MustCompile(`^\s*(grep|rg|ack)\s+`),
		suggestion: "[shell-hint] Use grep tool instead.",
	},
	{
		regex:      regexp.MustCompile(`^\s*find\s+`),
		suggestion: "[shell-hint] Use glob tool instead.",
	},
	{
		regex:      regexp.MustCompile(`^\s*git\s+log\b`),
		suggestion: "[shell-hint] Use git_log tool instead.",
	},
	{
		regex:      regexp.MustCompile(`^\s*git\s+diff\b`),
		suggestion: "[shell-hint] Use git_diff tool instead.",
	},
	{
		regex:      regexp.MustCompile(`^\s*git\s+status\b`),
		suggestion: "[shell-hint] Use git_status tool instead.",
	},
	{
		regex:      regexp.MustCompile(`^\s*git\s+show\b`),
		suggestion: "[shell-hint] Use git_show tool instead.",
	},
	{
		regex:      regexp.MustCompile(`^\s*(ls|lla)\s+`),
		suggestion: "[shell-hint] Use list_directory tool instead.",
	},
	{
		regex:      regexp.MustCompile(`^\s*git\s+branch\b`),
		suggestion: "[shell-hint] Use git_branch_list tool instead.",
	},
	{
		regex:      regexp.MustCompile(`^\s*git\s+add\b`),
		suggestion: "[shell-hint] Use git_add tool instead.",
	},
	{
		regex:      regexp.MustCompile(`^\s*git\s+commit\b`),
		suggestion: "[shell-hint] Use git_commit tool instead.",
	},
}

// shellNativeHintState tracks which patterns have already fired to avoid repetition.
type shellNativeHintState struct {
	mu       sync.Mutex
	firedSet map[string]bool
}

func newShellNativeHintState() *shellNativeHintState {
	return &shellNativeHintState{
		firedSet: make(map[string]bool),
	}
}

func (s *shellNativeHintState) reset() {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.firedSet = make(map[string]bool)
}

// extractCommandFromArgs parses the tool arguments JSON and returns the command string.
func extractShellCommandFromArgs(args []byte) string {
	s := string(args)
	idx := strings.Index(s, `"command"`)
	if idx < 0 {
		return ""
	}
	rest := s[idx:]
	colonIdx := strings.Index(rest, ":")
	if colonIdx < 0 {
		return ""
	}
	rest = rest[colonIdx+1:]
	rest = strings.TrimLeft(rest, " \t\n\r")
	if len(rest) == 0 || rest[0] != '"' {
		return ""
	}
	rest = rest[1:]
	var b strings.Builder
	for i := 0; i < len(rest); i++ {
		if rest[i] == '\\' && i+1 < len(rest) {
			b.WriteByte(rest[i+1])
			i++
			continue
		}
		if rest[i] == '"' {
			break
		}
		b.WriteByte(rest[i])
	}
	return b.String()
}

// maybeShellNativeHint checks if a run_command call duplicates a native tool
// and returns a suggestion hint. Returns empty string if no suggestion applies
// or if this pattern already fired in this run.
func (s *shellNativeHintState) maybeShellNativeHint(toolName string, args []byte) string {
	if !shellCommandToolNames[toolName] {
		return ""
	}

	cmd := extractShellCommandFromArgs(args)
	if cmd == "" {
		return ""
	}

	for _, p := range shellNativePatterns {
		if p.regex.MatchString(cmd) {
			s.mu.Lock()
			if s.firedSet[p.suggestion] {
				s.mu.Unlock()
				return ""
			}
			s.firedSet[p.suggestion] = true
			s.mu.Unlock()
			return p.suggestion
		}
	}

	return ""
}
