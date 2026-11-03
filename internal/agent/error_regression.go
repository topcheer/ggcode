package agent

import (
	"regexp"
	"strconv"
	"strings"

	"github.com/topcheer/ggcode/internal/debug"
)

func (a *Agent) errorRegressionCheckCommand(toolName string, args []byte, content string, isError bool) string {
	if a.errRegression == nil {
		return ""
	}
	if toolName != "run_command" {
		return ""
	}
	cmd := extractCommandFromArgs(args)
	if cmd == "" || !isVerifyCommand(cmd) {
		return ""
	}
	return a.errRegression.recordVerify(cmd, content, isError)
}

const (
	regressionWarnThreshold = 3

	maxRegressionWarnings = 2
)

var errorLinesRe = regexp.MustCompile(`(?im)^.*(?:\b(?:error|ERROR|Error)[\s:]|--- FAIL:|---fail:|undefined:|\bpanic:|\bfatal error:|error\[[A-Z]?\d+\]|\bAssertionError\b|\bExitError\b).*$`)

func countVerifyErrors(content string) int {
	matches := errorLinesRe.FindAllString(content, -1) //nolint:all
	count := 0
	for _, m := range matches {

		lower := strings.ToLower(m)
		if strings.Contains(lower, "0 error") || strings.Contains(lower, "no error") {
			continue
		}
		count++
	}
	return count
}

type errRegressionState struct {
	lastErrorCount int

	lastCommandKind string

	hadEdits bool

	warningCount int
}

func newErrRegressionState() *errRegressionState {
	return &errRegressionState{lastErrorCount: -1}
}

func (e *errRegressionState) reset() {
	e.lastErrorCount = -1
	e.hadEdits = false
	e.warningCount = 0
}

func (e *errRegressionState) recordEdit() {
	e.hadEdits = true
}

func (e *errRegressionState) recordVerify(cmd, content string, failed bool) string {
	currentErrors := countVerifyErrors(content)
	prevErrors := e.lastErrorCount
	hadEdits := e.hadEdits

	kind := verifyCommandKind(cmd)
	sameCommand := kind != "" && kind == e.lastCommandKind

	e.lastErrorCount = currentErrors
	e.lastCommandKind = kind
	e.hadEdits = false

	if prevErrors < 0 || !sameCommand || !hadEdits || !failed {
		return ""
	}

	increase := currentErrors - prevErrors
	if increase < regressionWarnThreshold {
		return ""
	}
	if e.warningCount >= maxRegressionWarnings {
		return ""
	}

	e.warningCount++
	debug.Log("error-regression",
		"error count increased from %d to %d after edits -- negative progress",
		prevErrors, currentErrors)
	return regressionGuidance(prevErrors, currentErrors, increase)
}

func regressionGuidance(prevErrors, currentErrors, increase int) string {
	return "[NEGATIVE PROGRESS WARNING] " +
		"Error count INCREASED from " + strconv.Itoa(prevErrors) + " to " +
		strconv.Itoa(currentErrors) + " (+" + strconv.Itoa(increase) +
		" new errors) after your edits. Your change likely introduced regressions " +
		"or unmasked previously hidden errors.\n\n" +
		"Before continuing to add more fixes:\n" +
		"1. Review the NEW errors carefully -- are they caused by your edit?\n" +
		"2. If so, your edit may have side effects you didn't consider. " +
		"Consider reverting and taking a different approach.\n" +
		"3. If the new errors were MASKED by previous errors (common in Go compilation), " +
		"fix them one at a time -- do not treat them as regressions.\n" +
		"4. Do NOT proceed with more edits until you understand why the error count went up."
}

func verifyCommandKind(cmd string) string {

	cmd = strings.TrimSpace(stripEnvAssignments(cmd))
	segs := splitCompoundSegments(cmd)
	if len(segs) > 0 {
		cmd = strings.TrimSpace(segs[len(segs)-1])
	}
	fields := strings.Fields(cmd)
	if len(fields) == 0 {
		return ""
	}
	kind := fields[0]
	if len(fields) > 1 {
		switch {
		case fields[0] == "go" && (fields[1] == "build" || fields[1] == "test" || fields[1] == "vet" || fields[1] == "lint"):
			kind = fields[0] + " " + fields[1]
		case fields[0] == "make" || fields[0] == "npm" || fields[0] == "yarn" || fields[0] == "pnpm" || fields[0] == "cargo":
			kind = fields[0] + " " + fields[1]
		}
	}

	if fields[0] == "go" && len(fields) > 2 {
		var scope, run, verbose string
		for i := 2; i < len(fields); i++ {
			f := fields[i]
			switch {
			case f == "-run" && i+1 < len(fields):
				run = "run=" + fields[i+1]
				i++
			case f == "-v":
				verbose = "v"
			case !strings.HasPrefix(f, "-") && scope == "":
				scope = f
			}
		}
		for _, part := range []string{scope, run, verbose} {
			if part != "" {
				kind += " " + part
			}
		}
	}
	return kind
}

func splitCompoundSegments(cmd string) []string {
	var segs []string
	for _, part := range splitCompoundCommand(cmd) {
		segs = append(segs, strings.Split(part, "&&")...)
	}
	return segs
}
