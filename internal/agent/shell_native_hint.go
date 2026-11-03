package agent

import (
	"regexp"
	"strings"
	"sync"
)

var shellCommandToolNames = map[string]bool{
	"run_command":   true,
	"start_command": true,
}

type shellNativePattern struct {
	regex      *regexp.Regexp
	suggestion string
}

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
