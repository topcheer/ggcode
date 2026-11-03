package agent

import (
	"encoding/json"
	"strconv"
	"strings"

	"github.com/topcheer/ggcode/internal/debug"
)

var strictVerifyCommands = map[string]bool{
	"go build": true, "go test": true, "go vet": true,
	"cargo build": true, "cargo test": true, "cargo clippy": true,
	"npm run build": true, "npm test": true, "npm run test": true,
	"pytest": true, "flutter test": true, "ctest": true, "rake test": true,
	"golangci-lint": true, "eslint": true, "ruff": true,
}

func isStrictVerifyCommand(cmd string) bool {
	for _, seg := range strings.Split(cmd, "&&") {
		for _, semi := range strings.Split(seg, ";") {
			for _, pipe := range strings.Split(semi, "|") {
				c := strings.ToLower(strings.TrimSpace(stripEnvAssignments(pipe)))
				if c == "" {
					continue
				}
				for prefix := range strictVerifyCommands {
					if strings.HasPrefix(c, prefix+" ") || c == prefix {
						return true
					}
				}
			}
		}
	}
	return false
}

func (a *Agent) fixCascadeCheckCommand(toolName string, args []byte, isError bool, content string) string {
	if a.fixCascade == nil {
		return ""
	}
	if toolName != "run_command" {
		return a.fixCascadeCheckAsync(toolName, args, content)
	}
	cmd := extractCommandFromArgs(args)

	if cmd == "" || !isStrictVerifyCommand(cmd) {
		return ""
	}

	if !isError && strings.Contains(content, "Automatically moved to background") {
		return ""
	}

	hadEdits := a.fixCascade.editCount > 0
	return a.fixCascade.recordVerify(hadEdits, isError)
}

func (a *Agent) fixCascadeCheckAsync(toolName string, args []byte, content string) string {
	switch toolName {
	case "wait_command", "read_command_output", "task_output":
	default:
		return ""
	}
	var m map[string]any
	if len(args) > 0 {
		_ = json.Unmarshal(args, &m)
	}
	jobID, _ := m["job_id"].(string)
	if jobID == "" {
		jobID, _ = m["task_id"].(string)
	}
	if jobID == "" || !a.prematureSuccess.psJobIsVerify(jobID) {
		return ""
	}
	terminal, passed := psTerminalVerifyOutcome(psParseJobStatus(content))
	if !terminal {
		return ""
	}

	return a.fixCascade.recordVerify(a.fixCascade.editCount > 0, !passed)
}

const (
	cascadeThreshold = 3
)

type fixCascadeState struct {
	editCount int

	failedVerifyCount int

	guidanceFired bool
}

func newFixCascadeState() *fixCascadeState {
	return &fixCascadeState{}
}

func (f *fixCascadeState) reset() {
	f.editCount = 0
	f.failedVerifyCount = 0
	f.guidanceFired = false
}

func (f *fixCascadeState) recordEdit() {
	f.editCount++
}

func (f *fixCascadeState) recordVerify(hadEdits bool, failed bool) string {
	if !failed {

		f.failedVerifyCount = 0
		f.editCount = 0
		return ""
	}

	if hadEdits {

		f.failedVerifyCount++
	}

	f.editCount = 0

	if f.failedVerifyCount >= cascadeThreshold && !f.guidanceFired {
		f.guidanceFired = true
		debug.Log("fix-cascade", "detected %d edit-verify-fail cycles -- likely wrong-hypothesis lock-in", f.failedVerifyCount)
		return cascadeGuidance(f.failedVerifyCount)
	}

	return ""
}

func cascadeGuidance(failedCount int) string {
	return "[HYPOTHESIS LOCK-IN WARNING] " +
		"You have completed " + strconv.Itoa(failedCount) + " edit->build->fail cycles, " +
		"each ending in verification failure. This pattern strongly suggests your underlying " +
		"hypothesis is WRONG -- incremental fixes will continue to produce new errors " +
		"without solving the problem.\n\n" +
		"STOP making incremental edits. Instead:\n" +
		"1. Re-read the ORIGINAL task requirements -- are you solving the right problem?\n" +
		"2. Re-examine the ROOT CAUSE, not the latest error symptom. " +
		"Trace the issue from first principles.\n" +
		"3. Consider that your approach may need a fundamentally different direction. " +
		"Discard the current hypothesis and explore alternatives.\n" +
		"4. If stuck after reconsidering, clearly explain the problem and ask for guidance " +
		"rather than continuing to make changes that don't work."
}
