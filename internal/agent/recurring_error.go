package agent

import (
	"crypto/sha256"
	"encoding/hex"
	"regexp"
	"strconv"
	"strings"
	"sync"

	"github.com/topcheer/ggcode/internal/debug"
)

const (
	recurringSoftThreshold = 2

	recurringHardThreshold = 3

	maxFingerprintLines = 5
)

type recurringErrorState struct {
	mu sync.Mutex

	fingerprintCounts map[string]int

	editsSinceLastError int

	firedLevels map[string]int

	lastFingerprint string
}

func newRecurringErrorState() *recurringErrorState {
	return &recurringErrorState{
		fingerprintCounts: make(map[string]int),
		firedLevels:       make(map[string]int),
	}
}

func (r *recurringErrorState) reset() {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.fingerprintCounts = make(map[string]int)
	r.editsSinceLastError = 0
	r.firedLevels = make(map[string]int)
	r.lastFingerprint = ""
}

func (r *recurringErrorState) recordEdit() {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.editsSinceLastError++
}

func (r *recurringErrorState) recordBuildError(output string) string {
	fp := fingerprintBuildError(output)
	if fp == "" {
		return ""
	}

	r.mu.Lock()
	defer r.mu.Unlock()

	hadEdits := r.editsSinceLastError > 0
	r.editsSinceLastError = 0

	isNewError := fp != r.lastFingerprint
	if isNewError {
		r.lastFingerprint = fp
	}

	if hadEdits || isNewError {
		r.fingerprintCounts[fp]++
	}
	count := r.fingerprintCounts[fp]

	if !hadEdits {

		return ""
	}

	fired := r.firedLevels[fp]
	var guidance string

	if count >= recurringHardThreshold && fired < 2 {
		r.firedLevels[fp] = 2
		guidance = recurringHardGuidance(count)
		debug.Log("recurring-error", "SAME error persisted across %d build cycles despite edits — injecting hard guidance", count)
	} else if count >= recurringSoftThreshold && fired < 1 {
		r.firedLevels[fp] = 1
		guidance = recurringSoftGuidance(count)
		debug.Log("recurring-error", "SAME error recurred across %d build cycles with edits between — injecting soft guidance", count)
	}

	return guidance
}

var (
	errorLineMarkers = []string{
		"error", "error[", "error:", "error e",
		"fail", "fail:", "--- fail", "panic:",
		"undefined", "cannot find", "not defined",
		"expected", "mismatch", "incompatible",
		"fatal", "traceback", "exception",
		"nameerror", "typeerror", "valueerror",
		"syntaxerror", "attributeerror",
	}

	lineColPattern    = regexp.MustCompile(`:\d+:\d+`)
	lineNumberPattern = regexp.MustCompile(`:\d+(?::\d+)?`)
	lineWordPattern   = regexp.MustCompile(`(?i)\bline\s+\d+`)
	pathPrefixPattern = regexp.MustCompile(`^(?:\.\./+|\./+|/+)+`)
	wsPattern         = regexp.MustCompile(`\s+`)
)

func fingerprintBuildError(output string) string {
	if output == "" {
		return ""
	}

	lines := strings.Split(output, "\n")
	var sigLines []string

	for _, line := range lines {
		trimmed := strings.TrimSpace(line)
		if trimmed == "" || len(trimmed) < 5 {
			continue
		}
		lower := strings.ToLower(trimmed)

		if strings.HasPrefix(lower, "--- pass") || strings.HasPrefix(lower, "ok  ") ||
			strings.HasPrefix(lower, "=== run") || lower == "pass" {
			continue
		}
		if !errorContainsAny(lower, errorLineMarkers...) {
			continue
		}
		normalized := normalizeErrorLine(trimmed)
		if normalized != "" {
			sigLines = append(sigLines, normalized)
		}
		if len(sigLines) >= maxFingerprintLines {
			break
		}
	}

	if len(sigLines) == 0 {
		return ""
	}

	joined := strings.Join(sigLines, "\n")
	h := sha256.Sum256([]byte(joined))
	return hex.EncodeToString(h[:8])
}

func normalizeErrorLine(line string) string {
	s := line

	s = lineColPattern.ReplaceAllString(s, ":N")

	s = lineNumberPattern.ReplaceAllString(s, ":N")

	s = lineWordPattern.ReplaceAllString(s, "line N")

	s = stripPathToBasename(s)

	s = wsPattern.ReplaceAllString(s, " ")
	s = strings.TrimSpace(s)

	return s
}

func stripPathToBasename(s string) string {
	return pathPrefixPattern.ReplaceAllString(s, "")
}

func recurringSoftGuidance(count int) string {
	return "[Recurring error: the SAME build/test error has appeared " +
		strconv.Itoa(count) + " times despite your edits. " +
		"Your changes are not addressing the root cause. " +
		"Re-read the FULL error output, identify the exact failing line and its cause, " +
		"and make a targeted fix rather than incremental tweaks.]"
}

func recurringHardGuidance(count int) string {
	return "[CRITICAL: the same error has persisted across " +
		strconv.Itoa(count) + " edit-and-rebuild cycles. Incremental edits will not fix this. " +
		"STOP and analyze: (1) read the complete error message word by word, " +
		"(2) trace the root cause — is it a missing import, wrong type, naming mismatch, or architectural issue? " +
		"(3) make ONE decisive fix that directly addresses the error, not a workaround.]"
}

func (a *Agent) recurringErrorRecordEdit() {
	if a.recurringError != nil {
		a.recurringError.recordEdit()
	}
}

func (a *Agent) recurringErrorCheckCommand(toolName string, args []byte, resultContent string, isError bool) string {
	if a.recurringError == nil {
		return ""
	}

	if toolName != "run_command" {
		return ""
	}
	cmd := extractCommandFromArgs(args)
	if cmd == "" || !isVerifyCommand(cmd) {
		return ""
	}

	if !isError && !hasErrorMarkers(resultContent) {
		return ""
	}
	return a.recurringError.recordBuildError(resultContent)
}

func (a *Agent) resetRecurringError() {
	if a.recurringError != nil {
		a.recurringError.reset()
	}
}

func hasErrorMarkers(output string) bool {
	if output == "" {
		return false
	}
	lower := strings.ToLower(output)
	var kept []string
	for _, line := range strings.Split(lower, "\n") {
		t := strings.TrimSpace(line)
		if strings.HasPrefix(t, "--- pass") || strings.HasPrefix(t, "ok  ") ||
			strings.HasPrefix(t, "=== run") || t == "pass" {
			continue
		}
		kept = append(kept, line)
	}
	return errorContainsAny(strings.Join(kept, "\n"), errorLineMarkers...)
}
