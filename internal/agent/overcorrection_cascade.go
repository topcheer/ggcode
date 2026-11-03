package agent

import (
	"encoding/json"
	"fmt"
	"regexp"
	"strings"
	"sync"

	"github.com/topcheer/ggcode/internal/debug"
	"github.com/topcheer/ggcode/internal/util"
)

const (
	overcorrectionWindowSize = 8

	overcorrectionMaxWarn = 2

	overcorrectionTrivialErrorBytes  = 80
	overcorrectionModerateErrorBytes = 300

	overcorrectionTrivialExpectedFix  = 100
	overcorrectionModerateExpectedFix = 400
	overcorrectionSevereExpectedFix   = 1000

	overcorrectionTrivialRatio  = 50.0
	overcorrectionModerateRatio = 15.0
	overcorrectionSevereRatio   = 8.0

	overcorrectionMinEditBytes = 500

	overcorrectionMaxErrorAge = 10

	overcorrectionCascadeMin = 2
)

type errorSeverity int

const (
	severityNone errorSeverity = iota
	severityTrivial
	severityModerate
	severitySevere
)

type overcorrectionEntry struct {
	errorSeverity errorSeverity
	editSize      int
	overcorrected bool
	filePath      string
}

type overcorrectionState struct {
	mu              sync.Mutex
	entries         []overcorrectionEntry
	pendingErr      errorSeverity
	stepsSinceError int
	warnCount       int
}

func newOvercorrectionState() *overcorrectionState {
	return &overcorrectionState{}
}

func (s *overcorrectionState) reset() {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.entries = nil
	s.pendingErr = severityNone
	s.stepsSinceError = 0
	s.warnCount = 0
}

func (s *overcorrectionState) recordErrorSignal(toolName string, resultContent string, isError bool) {
	s.mu.Lock()
	defer s.mu.Unlock()

	if isError {
		newSev := classifyErrorSeverity(toolName, resultContent)

		if newSev > s.pendingErr {
			s.pendingErr = newSev
		}
		s.stepsSinceError = 0
		return
	}

	if sev := classifyDiagnosticSeverity(toolName, resultContent); sev > s.pendingErr {
		s.pendingErr = sev
		s.stepsSinceError = 0
	}
}

func (s *overcorrectionState) recordEdit(size int, filePath string) string {
	s.mu.Lock()
	defer s.mu.Unlock()

	if s.pendingErr == severityNone {
		return ""
	}

	if s.stepsSinceError >= overcorrectionMaxErrorAge {
		s.pendingErr = severityNone
		return ""
	}

	errSev := s.pendingErr

	if size < overcorrectionMinEditBytes {
		s.entries = append(s.entries, overcorrectionEntry{
			errorSeverity: errSev,
			editSize:      size,
			overcorrected: false,
			filePath:      filePath,
		})
		s.trimWindow()
		return ""
	}

	s.pendingErr = severityNone

	overcorrected := isOvercorrection(errSev, size)

	s.entries = append(s.entries, overcorrectionEntry{
		errorSeverity: errSev,
		editSize:      size,
		overcorrected: overcorrected,
		filePath:      filePath,
	})
	s.trimWindow()

	if !overcorrected {
		return ""
	}

	consecutiveOver := 0
	for i := len(s.entries) - 1; i >= 0; i-- {
		if s.entries[i].overcorrected {
			consecutiveOver++
			continue
		}
		if s.entries[i].editSize < overcorrectionMinEditBytes {
			continue
		}
		break
	}

	if s.warnCount >= overcorrectionMaxWarn {
		return ""
	}

	if consecutiveOver >= overcorrectionCascadeMin {
		s.warnCount++
		return overcorrectionCascadeWarning(errSev, size, consecutiveOver)
	}

	if errSev == severityTrivial {
		s.warnCount++
		return overcorrectionSingleWarning(errSev, size)
	}

	return ""
}

func (s *overcorrectionState) trimWindow() {
	if len(s.entries) > overcorrectionWindowSize {
		s.entries = s.entries[len(s.entries)-overcorrectionWindowSize:]
	}
}

func isOvercorrection(errSev errorSeverity, editSize int) bool {
	expectedFix := overcorrectionSevereExpectedFix
	ratio := overcorrectionSevereRatio

	switch errSev {
	case severityTrivial:
		expectedFix = overcorrectionTrivialExpectedFix
		ratio = overcorrectionTrivialRatio
	case severityModerate:
		expectedFix = overcorrectionModerateExpectedFix
		ratio = overcorrectionModerateRatio
	case severitySevere:
		expectedFix = overcorrectionSevereExpectedFix
		ratio = overcorrectionSevereRatio
	default:
		return false
	}

	if expectedFix == 0 {
		return editSize > overcorrectionMinEditBytes*10
	}

	actualRatio := float64(editSize) / float64(expectedFix)
	return actualRatio > ratio
}

func classifyErrorSeverity(toolName string, content string) errorSeverity {
	c := strings.ToLower(content)
	l := len(c)

	switch {
	case toolName == "run_command" || toolName == "start_command":

		if strings.Contains(c, "panic:") || strings.Contains(c, "fatal error:") ||
			strings.Contains(c, "signal:") || strings.Contains(c, "segfault") {
			return severitySevere
		}

		if strings.Contains(c, "build failed") || strings.Contains(c, "compilation error") ||
			strings.Contains(c, "undefined:") || strings.Contains(c, "cannot find") ||
			strings.Contains(c, "syntax error") || strings.Contains(c, "type mismatch") {
			return severityModerate
		}

		if l < overcorrectionTrivialErrorBytes {
			return severityTrivial
		}
		return severityModerate

	case strings.HasPrefix(toolName, "lsp_"):

		if l < overcorrectionTrivialErrorBytes {
			return severityTrivial
		}
		return severityModerate

	case toolName == "edit_file" || toolName == "multi_edit_file" || toolName == "multi_file_edit":

		return severityTrivial

	case toolName == "git_commit" || toolName == "git_add":
		return severityTrivial

	default:
		if l > overcorrectionModerateErrorBytes {
			return severityModerate
		}
		return severityTrivial
	}
}

var diagnosticWarningRe = regexp.MustCompile(
	`(?im)(?:[^\s]+\.[a-zA-Z][a-zA-Z0-9]{0,7}:\d+:\d+:[ \t]+|^[ \t]*)warning:`,
)

func classifyDiagnosticSeverity(toolName string, content string) errorSeverity {
	if toolName != "run_command" && toolName != "start_command" {
		return severityNone
	}
	c := strings.ToLower(content)

	for _, pattern := range []string{
		"unused import", "unused variable", "declared but not used",
		"declared and not used",
		"missing newline", "format specifier", "ineffectual assignment",
		"deprecated:", "lint:", "gosimple", "staticcheck",
	} {
		if strings.Contains(c, pattern) {
			return severityTrivial
		}
	}
	if diagnosticWarningRe.MatchString(content) {
		return severityTrivial
	}
	return severityNone
}

func overcorrectionSingleWarning(errSev errorSeverity, editSize int) string {
	sevName := severityName(errSev)
	return fmt.Sprintf(
		"[overcorrection-cascade] Fix for %s error was %d bytes - too large. Use minimal targeted change.",
		sevName, editSize,
	)
}

func overcorrectionCascadeWarning(errSev errorSeverity, editSize int, consecutive int) string {
	_ = errSev
	_ = editSize
	return fmt.Sprintf(
		"[overcorrection-cascade] %d overcorrections - fixes far larger than errors warranted. Apply minimal surgical changes.",
		consecutive,
	)
}

func severityName(s errorSeverity) string {
	switch s {
	case severityTrivial:
		return "trivial"
	case severityModerate:
		return "moderate"
	case severitySevere:
		return "severe"
	default:
		return "unknown"
	}
}

func (a *Agent) overcorrectionRecordError(toolName string, resultContent string, isError bool) {
	if a.overcorrection == nil {
		return
	}
	a.overcorrection.recordErrorSignal(toolName, resultContent, isError)
}

func (s *overcorrectionState) recordNonEditStep() {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.pendingErr != severityNone {
		s.stepsSinceError++
	}
}

func (a *Agent) overcorrectionRecordEdit(toolName string, args json.RawMessage) string {
	if a.overcorrection == nil {
		return ""
	}
	if !productiveEditTools[toolName] {
		return ""
	}
	size := measureEditSize(toolName, args)
	filePath := firstEditFilePath(toolName, args)
	hint := a.overcorrection.recordEdit(size, filePath)
	if hint != "" {
		debug.Log("agent", "overcorrection cascade detected: %s", util.Truncate(hint, 120))
	}
	return hint
}

func (a *Agent) resetOvercorrection() {
	if a.overcorrection != nil {
		a.overcorrection.reset()
	}
}
