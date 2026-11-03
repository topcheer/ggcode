package agent

import (
	"fmt"
	"strings"
	"sync"
)

type FailureMode int

const (
	FailureModeNone FailureMode = iota
	FailureModeTransient
	FailureModeStructural
	FailureModeSystemic
)

func (m FailureMode) String() string {
	switch m {
	case FailureModeTransient:
		return "transient"
	case FailureModeStructural:
		return "structural"
	case FailureModeSystemic:
		return "systemic"
	default:
		return "none"
	}
}

type failureModeState struct {
	mu sync.Mutex

	transientCount  int
	structuralCount int
	systemicCount   int

	fired map[FailureMode]bool

	totalCalls int

	structuralStreakStart int
}

func newFailureModeState() *failureModeState {
	return &failureModeState{
		fired: make(map[FailureMode]bool),
	}
}

func (s *failureModeState) reset() {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.transientCount = 0
	s.structuralCount = 0
	s.systemicCount = 0
	s.fired = make(map[FailureMode]bool)
	s.totalCalls = 0
}

func classifyFailureMode(toolName, errorContent string) FailureMode {
	c := strings.ToLower(errorContent)

	systemicPatterns := []string{
		"permission denied",
		"access denied",
		"operation not permitted",
		"command not found",
		"no such file or directory",
		"executable file not found",
		"not enough space",
		"disk full",
		"no space left",
		"too many open files",
		"connection refused",
		"authentication failed",
		"unauthorized",
		"forbidden",
		"api key",
		"invalid api key",
		"quota exceeded",
		"billing",
		"payment required",
	}
	for _, p := range systemicPatterns {
		if strings.Contains(c, p) {

			if p == "no such file or directory" {

				if strings.Contains(c, "executable file not found") || strings.Contains(c, "command not found") ||
					strings.Contains(c, "exec:") {
					return FailureModeSystemic
				}

				if isShellSpawnEnoentContent(c) {
					return FailureModeSystemic
				}

				continue
			}
			return FailureModeSystemic
		}
	}

	transientPatterns := []string{
		"timeout",
		"timed out",
		"deadline exceeded",
		"context deadline",
		"rate limit",
		"rate_limit",
		"429",
		"503",
		"502",
		"service unavailable",
		"bad gateway",
		"connection reset",
		"connection closed",
		"eof",
		"i/o timeout",
		"temporary failure",
		"try again",
		"overloaded",
		"capacity",
	}
	for _, p := range transientPatterns {

		if isAllDigits1561(p) {
			if containsToken1561(c, p) {
				return FailureModeTransient
			}
			continue
		}
		if strings.Contains(c, p) {
			return FailureModeTransient
		}
	}

	return FailureModeStructural
}

var shellBinaryNames = map[string]bool{
	"sh": true, "bash": true, "zsh": true, "dash": true, "ksh": true,
	"pwsh": true, "powershell": true,
}

func isShellSpawnEnoentContent(lowercased string) bool {
	const prefix = "fork/exec "
	idx := strings.Index(lowercased, prefix)
	if idx < 0 {
		return false
	}
	rest := lowercased[idx+len(prefix):]

	end := strings.Index(rest, ": ")
	if end < 0 {
		return false
	}
	bin := rest[:end]
	if i := strings.LastIndexAny(bin, `/\`); i >= 0 {
		bin = bin[i+1:]
	}
	bin = strings.TrimSuffix(bin, ".exe")
	return shellBinaryNames[bin]
}

func (s *failureModeState) recordResult(toolName string, isError bool, errorContent string) string {
	s.mu.Lock()
	defer s.mu.Unlock()

	s.totalCalls++
	if !isError {

		s.structuralCount = 0
		delete(s.fired, FailureModeStructural)

		s.transientCount = 0
		delete(s.fired, FailureModeTransient)
		return ""
	}

	mode := classifyFailureMode(toolName, errorContent)
	switch mode {
	case FailureModeTransient:
		s.transientCount++
	case FailureModeStructural:
		if s.structuralCount == 0 {

			s.structuralStreakStart = s.totalCalls
		}
		s.structuralCount++
	case FailureModeSystemic:
		s.systemicCount++
	default:
		return ""
	}

	return s.checkDominantMode()
}

func (s *failureModeState) checkDominantMode() string {

	if s.systemicCount >= 1 && !s.fired[FailureModeSystemic] {
		s.fired[FailureModeSystemic] = true
		return "[Failure Mode: SYSTEMIC] An environment-level error is blocking progress " +
			"(permission denied, missing binary, auth failure, or resource exhaustion). " +
			"Verify the environment before retrying (which <cmd>, ls the path, check " +
			"permissions/disk/api key). If the environment is genuinely broken, report it " +
			"to the user instead of iterating on workarounds."
	}

	if s.transientCount >= 3 && !s.fired[FailureModeTransient] {
		s.fired[FailureModeTransient] = true
		return fmt.Sprintf("%s",
			"[Failure Mode: TRANSIENT] "+fmt.Sprintf("%d", s.transientCount)+
				" failures appear to be transient (timeouts, rate limits). "+
				"Consider adding longer delays between retries, reducing request frequency, "+
				"or switching to a lighter approach that makes fewer API calls.")
	}

	streakWindow := s.totalCalls - s.structuralStreakStart + 1
	if s.structuralStreakStart == 0 {
		streakWindow = s.totalCalls
	}
	structuralRatio := 0.0
	if streakWindow > 0 {
		structuralRatio = float64(s.structuralCount) / float64(streakWindow)
	}
	if s.structuralCount >= 4 && structuralRatio >= 0.30 && !s.fired[FailureModeStructural] {
		s.fired[FailureModeStructural] = true
		return "[Failure Mode: STRUCTURAL] " + fmt.Sprintf("%d", s.structuralCount) +
			" failures suggest the current approach is fundamentally wrong " +
			"(wrong paths, incorrect parameters, type mismatches). " +
			"Do NOT retry the same approach. Step back, re-read the relevant code, " +
			"verify assumptions (file existence, types, import paths), and try a different strategy."
	}

	return ""
}

func isAllDigits1561(p string) bool {
	if p == "" {
		return false
	}
	for i := 0; i < len(p); i++ {
		if p[i] < '0' || p[i] > '9' {
			return false
		}
	}
	return true
}

func containsToken1561(s, needle string) bool {
	for i := 0; i+len(needle) <= len(s); i++ {
		if s[i:i+len(needle)] != needle {
			continue
		}
		leftOK := i == 0 || (!isDigitByte1561(s[i-1]) && s[i-1] != ':' && s[i-1] != '/')
		j := i + len(needle)
		rightOK := j == len(s) || (!isDigitByte1561(s[j]) && s[j] != ':')
		if leftOK && rightOK {
			return true
		}
	}
	return false
}

func isDigitByte1561(b byte) bool { return b >= '0' && b <= '9' }
