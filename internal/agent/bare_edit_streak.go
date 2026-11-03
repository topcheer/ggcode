package agent

import (
	"encoding/json"
	"fmt"
	"strings"
)

type bareEditStreakState struct {
	streak           int
	warnCount        int
	lastWarnedAt     int
	prevLastWarnedAt int
	canRevert        bool
}

const (
	bareEditStreakThreshold = 5
	bareEditStreakMaxWarns  = 1
	bareEditStreakRewarnGap = 3
)

func newBareEditState() *bareEditStreakState {
	return &bareEditStreakState{}
}

func (s *bareEditStreakState) reset() {
	s.streak = 0
	s.warnCount = 0
	s.lastWarnedAt = 0
}

func (s *bareEditStreakState) recordToolCall(toolName string, toolInput string) {
	switch {
	case bareStreakIsMutation(toolName):
		s.streak++
	case bareStreakIsVerification(toolName, toolInput):
		s.streak = 0
	}
}

func (s *bareEditStreakState) markUndelivered() {
	if !s.canRevert || s.warnCount == 0 {
		return
	}
	s.canRevert = false
	s.warnCount--
	s.lastWarnedAt = s.prevLastWarnedAt
}

func (s *bareEditStreakState) maybeWarn(_ int) string {
	if s.warnCount >= bareEditStreakMaxWarns {
		return ""
	}
	if s.streak < bareEditStreakThreshold {
		return ""
	}

	if s.warnCount > 0 && s.streak < s.lastWarnedAt+bareEditStreakRewarnGap {
		return ""
	}
	s.warnCount++

	s.prevLastWarnedAt = s.lastWarnedAt
	s.canRevert = true
	s.lastWarnedAt = s.streak
	return fmt.Sprintf(
		"[feedback-loop] You have made %d consecutive file edits without any "+
			"verification step (build/test/run). Errors may have accumulated. "+
			"Run `go build -tags goolm ./...` or `go test -tags goolm ./...` "+
			"NOW to catch issues before they compound into a larger debugging session.",
		s.streak,
	)
}

func bareStreakIsMutation(toolName string) bool {
	return sourceMutatingTools[toolName]
}

func bareStreakIsVerification(toolName string, toolInput string) bool {
	switch {
	case toolName == "run_command", toolName == "start_command":

		return commandIsVerification(toolInput)
	case toolName == "lsp_diagnostics",
		toolName == "lsp_references",
		toolName == "lsp_definition",
		toolName == "code_health",
		toolName == "review_changes",
		toolName == "verify":
		return true

	case toolName == "git_diff", toolName == "git_status":
		return true
	default:
		return false
	}
}

func commandIsVerification(toolInput string) bool {
	if toolInput == "" {
		return false
	}
	var args struct {
		Command string `json:"command"`
	}
	if err := json.Unmarshal([]byte(toolInput), &args); err != nil {
		return false
	}
	return psIsVerifyCommand(stripCommandPrefixes(args.Command))
}

func stripCommandPrefixes(cmd string) string {
	var real []string
	for _, line := range strings.Split(cmd, "\n") {
		t := strings.TrimSpace(line)
		if t == "" || strings.HasPrefix(t, "#") {
			continue
		}
		fields := strings.Fields(t)
		for len(fields) > 0 && isEnvAssign(fields[0]) {
			fields = fields[1:]
		}
		real = append(real, fields...)
	}
	return strings.Join(real, " ")
}

func isEnvAssign(tok string) bool {
	i := strings.Index(tok, "=")
	if i <= 0 {
		return false
	}
	for _, r := range tok[:i] {
		switch {
		case r == '_' || (r >= 'a' && r <= 'z') || (r >= 'A' && r <= 'Z'):
		case r >= '0' && r <= '9':
		default:
			return false
		}
	}
	return true
}
