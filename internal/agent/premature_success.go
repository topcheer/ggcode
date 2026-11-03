package agent

import (
	"regexp"
	"strings"
	"sync"

	"github.com/topcheer/ggcode/internal/debug"
)

const (
	prematureSuccessMaxFires = 1
)

var verifyCmdPatterns = []string{
	"build", "test", "lint", "vet", "verify", "check", "compile",
	"go test", "go build", "go vet", "npm test",
	"cargo test", "cargo build", "pytest", "jest", "rspec",
	"dotnet test", "dotnet build",
}

var makeVerifyTargets = map[string]bool{
	"test": true, "tests": true, "verify": true, "ci": true, "build": true,
	"lint": true, "check": true, "e2e": true, "validate": true, "integration": true,
	"verify-ci": true,
}

var npmVerifyScripts = map[string]bool{
	"test": true, "tests": true, "build": true, "lint": true, "check": true,
	"verify": true, "e2e": true, "ci": true, "typecheck": true, "validate": true,
}

var mvnVerifyPhases = map[string]bool{
	"test": true, "verify": true, "validate": true, "check": true, "integration-test": true,
}

var gradleVerifyTasks = map[string]bool{
	"test": true, "check": true, "verify": true, "build": true, "lint": true, "validate": true,
	"connectedCheck": true, "integrationTest": true,
}

var cmakeVerifyTargets = map[string]bool{
	"--build": true, "--target": true,
}

var successClaimPatterns = []*regexp.Regexp{
	regexp.MustCompile(`(?i)\b(?:all|the)?\s*tests?\s+(?:pass|passed|passing|succeed|succeeded|succeeding)\b`),
	regexp.MustCompile(`(?i)\bbuild\s+(?:pass|passes|passed|passing|succeed|succeeds|succeeded)\b`),
	regexp.MustCompile(`(?i)\b(?:task|job|work)\s+(?:is\s+)?(?:complete|completed|done|finished)\b`),
	regexp.MustCompile(`(?i)\b(?:fixed|resolved|solved|corrected|repaired)\s+(?:the\s+)?(?:issue|bug|problem|error)\b`),
	regexp.MustCompile(`(?i)\b(?:the\s+)?(?:issue|bug|problem|error)\s+(?:is|was|has been)\s+(?:fixed|resolved|solved|corrected|repaired)\b`),
	regexp.MustCompile(`(?i)\bnow\s+(?:works?|working|passes?|passing)\s+(?:correctly|as expected|properly)?\b`),
	regexp.MustCompile(`(?i)\b(?:everything|all)\s+(?:is|looks|seems|appears)\s+(?:good|correct|working|fine|passing)\b`),
	regexp.MustCompile(`(?i)\bdone\s*[!.]\s*$`),
	regexp.MustCompile(`(?i)\b(?:implementation|fix|change)\s+(?:is\s+)?(?:complete|finished|done|ready)\b`),

	regexp.MustCompile(`(?i)\beverything\s+(?:passes|passed|works|working|succeeds|succeeded)\b`),
	regexp.MustCompile(`(?i)\b(?:fix|patch|change)\s+(?:resolves|resolved|fixes|fixed)\b`),
}

var conditionalGuardWords = []string{
	"if ", "when ", "should ", "would ", "could ", "might ", "may ",
	"need to ", "want to ", "going to ", "about to ",
	"once ", "after ", "before ",

	"no ", "zero ", "none ", "0 ",
	"make ",
}

type ciStatusVerdict int

const (
	ciVerdictNone ciStatusVerdict = iota
	ciVerdictSuccess
	ciVerdictFailure
)

func ciStatusConclusion(content string) ciStatusVerdict {
	head := content
	if len(head) > 512 {
		head = head[:512]
	}
	if strings.Contains(head, "CI PASSED") || strings.Contains(head, "✓") && strings.Contains(head, "succeeded") {
		return ciVerdictSuccess
	}
	if strings.Contains(head, "CI FAILED") || strings.Contains(head, "CI CANCELLED") ||
		strings.Contains(head, "(FAILED)") || strings.Contains(head, "Step #") && strings.Contains(head, "FAILED") {
		return ciVerdictFailure
	}
	return ciVerdictNone
}

var verifyTools = map[string]bool{
	"run_command":         true,
	"start_command":       true,
	"wait_command":        true,
	"task_output":         true,
	"read_command_output": true,
	"ci_status":           true,
}

var psEditTools = sourceMutatingTools

type psJobRecord struct {
	isVerify bool
	cmd      string
	subAgent bool
}

const psMaxTrackedJobs = 64

type prematureSuccessState struct {
	mu                  sync.Mutex
	backgroundJobs      map[string]psJobRecord
	editsSinceVerify    int
	everVerified        bool
	lastVerifyFailed    bool
	lastVerifyFailedCmd string
	guidanceFired       int
}

func newPrematureSuccessState() *prematureSuccessState {
	return &prematureSuccessState{backgroundJobs: make(map[string]psJobRecord)}
}

func psExtractJobID(content string) string {
	for _, ln := range strings.Split(content, "\n") {
		ln = strings.TrimSpace(ln)
		if v, ok := strings.CutPrefix(ln, "Job ID: "); ok && strings.TrimSpace(v) != "" {
			return strings.TrimSpace(v)
		}
	}
	return ""
}

func psExtractAgentID(content string) string {
	for _, ln := range strings.Split(content, "\n") {
		ln = strings.TrimSpace(ln)
		if v, ok := strings.CutPrefix(ln, "Sub-agent spawned with ID: "); ok && strings.TrimSpace(v) != "" {
			return strings.TrimSpace(v)
		}
	}
	return ""
}

func psParseJobStatus(content string) string {
	for _, ln := range strings.Split(content, "\n") {
		ln = strings.TrimSpace(ln)
		if !strings.HasPrefix(ln, "Status: ") {
			continue
		}
		st := strings.ToLower(strings.TrimSpace(strings.TrimPrefix(ln, "Status: ")))
		if idx := strings.IndexAny(st, " \t"); idx >= 0 {
			st = st[:idx]
		}
		return st
	}
	return ""
}

func psTerminalVerifyOutcome(status string) (terminal bool, passed bool) {
	switch status {
	case "completed":
		return true, true
	case "failed", "cancelled", "timed_out", "timeout", "timedout":
		return true, false
	default:
		return false, false
	}
}

func (p *prematureSuccessState) psMarkVerifiedLocked() {
	p.editsSinceVerify = 0
	p.everVerified = true
	p.lastVerifyFailed = false
	p.lastVerifyFailedCmd = ""
}

func (p *prematureSuccessState) psJobIsVerify(jobID string) bool {
	p.mu.Lock()
	defer p.mu.Unlock()
	rec, ok := p.backgroundJobs[jobID]
	return ok && rec.isVerify
}

func (p *prematureSuccessState) psJobCommand(jobID string) (string, bool) {
	p.mu.Lock()
	defer p.mu.Unlock()
	rec, ok := p.backgroundJobs[jobID]
	return rec.cmd, ok
}

func (p *prematureSuccessState) psRegisterJobLocked(jobID, cmd string) {
	if p.backgroundJobs == nil {
		p.backgroundJobs = make(map[string]psJobRecord)
	}
	p.backgroundJobs[jobID] = psJobRecord{isVerify: psIsVerifyCommand(cmd), cmd: cmd}
	for len(p.backgroundJobs) > psMaxTrackedJobs {
		for k := range p.backgroundJobs {
			delete(p.backgroundJobs, k)
			break
		}
	}
}

func (p *prematureSuccessState) reset() {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.editsSinceVerify = 0
	p.everVerified = false
	p.lastVerifyFailed = false
	p.lastVerifyFailedCmd = ""
	p.guidanceFired = 0
}

func (p *prematureSuccessState) recordToolCall(toolName string, args map[string]interface{}, isError bool, resultContent string) {
	p.mu.Lock()
	defer p.mu.Unlock()

	if psEditTools[toolName] {
		p.editsSinceVerify++
		return
	}

	switch {
	case toolName == "run_command":
		cmd, _ := args["command"].(string)

		if shellMutatesSources(cmd) {
			p.editsSinceVerify++
		}
		if !psIsVerifyCommand(cmd) {
			return
		}

		if isError {
			p.lastVerifyFailed = true
			p.lastVerifyFailedCmd = cmd
			return
		}
		p.psMarkVerifiedLocked()

	case toolName == "start_command":

		if isError {
			return
		}
		jobID := psExtractJobID(resultContent)
		if jobID == "" {
			return
		}
		cmd, _ := args["command"].(string)
		p.psRegisterJobLocked(jobID, cmd)

	case toolName == "spawn_agent":

		if isError {
			return
		}
		agentID := psExtractAgentID(resultContent)
		if agentID == "" {
			return
		}
		taskDesc, _ := args["task"].(string)
		if !psIsVerifyCommand(taskDesc) {
			return
		}
		p.psRegisterJobLocked(agentID, taskDesc)

		if rec, ok := p.backgroundJobs[agentID]; ok {
			rec.subAgent = true
			p.backgroundJobs[agentID] = rec
		}

	case toolName == "wait_command" || toolName == "read_command_output" || toolName == "task_output":
		jobID, _ := args["job_id"].(string)
		if jobID == "" {
			jobID, _ = args["task_id"].(string)
		}
		rec, tracked := p.backgroundJobs[jobID]
		if !tracked || !rec.isVerify {

			return
		}
		terminal, passed := psTerminalVerifyOutcome(psParseJobStatus(resultContent))
		if !terminal {

			if rec.subAgent {
				delete(p.backgroundJobs, jobID)
				if isError {
					p.lastVerifyFailed = true
					if rec.cmd != "" {
						p.lastVerifyFailedCmd = rec.cmd
					} else {
						p.lastVerifyFailedCmd = "sub-agent verify " + jobID
					}
					return
				}
				p.psMarkVerifiedLocked()
			}

			return
		}

		delete(p.backgroundJobs, jobID)
		if passed {
			p.psMarkVerifiedLocked()
			return
		}
		p.lastVerifyFailed = true
		if rec.cmd != "" {
			p.lastVerifyFailedCmd = rec.cmd
		} else {
			p.lastVerifyFailedCmd = "background job " + jobID
		}

	case verifyTools[toolName]:
		if isError {
			p.lastVerifyFailed = true
			p.lastVerifyFailedCmd = ""
			return
		}

		if toolName == "ci_status" {
			switch ciStatusConclusion(resultContent) {
			case ciVerdictSuccess:
				p.psMarkVerifiedLocked()
			case ciVerdictFailure:
				p.lastVerifyFailed = true
				p.lastVerifyFailedCmd = toolName
			}
			return
		}
		p.psMarkVerifiedLocked()
	}
}

func psStripCdPrefix(tokens []string) []string {
	for len(tokens) > 0 && tokens[0] == "cd" {
		tokens = tokens[1:]

		if len(tokens) > 0 && tokens[0] != "&&" {
			tokens = tokens[1:]
		}

		if len(tokens) > 0 && tokens[0] == "&&" {
			tokens = tokens[1:]
		}
	}
	return tokens
}

func psIsVerifyCommand(cmd string) bool {
	if cmd == "" {
		return false
	}
	lower := strings.ToLower(cmd)
	tokens := strings.Fields(lower)
	tokens = psStripCdPrefix(tokens)
	if len(tokens) == 0 {
		return false
	}

	if isVerify, handled := psBuildSystemVerify(tokens); handled {
		return isVerify
	}

	for _, pat := range verifyCmdPatterns {
		if strings.Contains(pat, " ") {

			cmdPosTokens := psCommandPositionTokens(tokens)
			for t := range cmdPosTokens {

				if i := indexOfToken(tokens, t); i >= 0 {
					if i+len(strings.Fields(pat)) <= len(tokens) {
						substr := strings.Join(tokens[i:i+len(strings.Fields(pat))], " ")
						if substr == pat {
							return true
						}
					}
				}
			}
			continue
		}

		for t := range psCommandPositionTokens(tokens) {
			if t == pat {
				return true
			}

			if psSegmentFirstTokens(tokens)[t] && (strings.HasPrefix(t, pat+"-") || strings.HasPrefix(t, pat+"_")) {
				return true
			}
		}
	}
	return false
}

func psBuildSystemVerify(tokens []string) (isVerify, handled bool) {
	switch tokens[0] {
	case "make", "gmake", "mingw32-make":

		for _, t := range tokens[1:] {
			if strings.HasPrefix(t, "-") || strings.Contains(t, "=") {
				continue
			}
			if makeVerifyTargets[t] {
				return true, true
			}
		}
		return false, true
	case "npm", "yarn", "pnpm", "bun":

		if len(tokens) >= 2 {
			script := ""
			if tokens[1] == "run" && len(tokens) >= 3 {
				script = tokens[2]
			} else if tokens[1] != "run" {
				script = tokens[1]
			}
			if script != "" && npmVerifyScripts[script] {
				return true, true
			}
		}
		return false, true
	case "mvn", "mvnw", "./mvnw":
		for _, t := range tokens[1:] {
			if mvnVerifyPhases[t] {
				return true, true
			}
		}
		return false, true
	case "gradle", "gradlew", "./gradlew":
		for _, t := range tokens[1:] {
			if gradleVerifyTasks[t] {
				return true, true
			}
		}
		return false, true
	case "cmake":
		for _, t := range tokens[1:] {
			if cmakeVerifyTargets[t] || strings.HasPrefix(t, "--target=") {
				return true, true
			}
		}
		return false, true
	}
	return false, false
}

var psRunnerPrefixes = map[string]bool{
	"go": true, "cargo": true, "uv": true, "poetry": true, "npx": true,
	"bunx": true, "dotnet": true, "deno": true, "ruby": true, "bundle": true,
	"flutter": true, "dart": true, "python": true, "python3": true,
	"run":  true,
	"make": true, "gmake": true, "mingw32-make": true,
	"npm": true, "yarn": true, "pnpm": true, "bun": true,
	"mvn": true, "mvnw": true, "./mvnw": true, "gradle": true, "gradlew": true, "./gradlew": true,
	"cmake": true,
}

func indexOfToken(tokens []string, token string) int {
	for i, t := range tokens {
		if t == token {
			return i
		}
	}
	return -1
}

func psSegmentFirstTokens(tokens []string) map[string]bool {
	cmds := make(map[string]bool)
	segFirst := true
	for _, t := range tokens {
		if t == "|" || t == "||" || t == "&&" || t == ";" {
			segFirst = true
			continue
		}
		if segFirst {
			cmds[t] = true
		}
		segFirst = false
	}
	return cmds
}

func psCommandPositionTokens(tokens []string) map[string]bool {
	cmds := make(map[string]bool)
	segFirst := true
	prevRunner := false
	prevM := false
	for _, t := range tokens {
		if t == "|" || t == "||" || t == "&&" || t == ";" {
			segFirst = true
			prevRunner = false
			prevM = false
			continue
		}
		switch {
		case segFirst:
			cmds[t] = true
		case prevRunner || prevM:
			cmds[t] = true
		}
		segFirst = false
		prevRunner = psRunnerPrefixes[t]
		prevM = t == "-m"
	}
	return cmds
}

func (p *prematureSuccessState) checkSuccessClaim(assistantText string) string {
	p.mu.Lock()
	defer p.mu.Unlock()

	if p.guidanceFired >= prematureSuccessMaxFires {
		return ""
	}

	if p.editsSinceVerify == 0 {
		return ""
	}

	claimFound := false
	for _, re := range successClaimPatterns {
		loc := re.FindStringIndex(assistantText)
		if loc == nil {
			continue
		}

		start := loc[0] - 40
		if start < 0 {
			start = 0
		}
		contextBefore := assistantText[start:loc[0]]
		guarded := false
		for _, gw := range conditionalGuardWords {
			idx := strings.Index(strings.ToLower(contextBefore), gw)
			if idx == -1 {
				continue
			}

			if gw == "once " || gw == "after " {
				afterGuard := strings.ToLower(contextBefore[idx+len(gw):])
				for _, marker := range []string{"will ", "going to", "pending", "now"} {
					if strings.Contains(afterGuard, marker) {
						guarded = true
						break
					}
				}
				if guarded {
					break
				}

				continue
			}
			guarded = true
			break
		}
		if !guarded {
			claimFound = true
			break
		}
	}

	if !claimFound {
		return ""
	}

	p.guidanceFired++
	debug.Log("premature-success", "unverified success claim detected (editsSinceVerify=%d, everVerified=%v, lastVerifyFailed=%v)",
		p.editsSinceVerify, p.everVerified, p.lastVerifyFailed)

	var tips []string
	tips = append(tips,
		"Premature success declaration detected: you've claimed success but made edits without running verification afterward.",
		"Before declaring completion, you MUST run verification (build + test):",
		"1. Run the project's build command (e.g., go build, make build)",
		"2. Run the project's test command (e.g., go test ./..., make test)",
		"3. Only declare success after verification passes with no errors",
	)
	if p.lastVerifyFailed {

		cmdHint := p.lastVerifyFailedCmd
		if cmdHint == "" {
			cmdHint = "(verification command)"
		}
		tips = append(tips, "CRITICAL: your success claim CONTRADICTS the most recent verification, which FAILED (`"+cmdHint+"`). Re-run it, fix the failures, and only then declare success.")
	} else if !p.everVerified {
		tips = append(tips, "Note: no verification has been run at all in this session.")
	}

	return "WARNING: " + strings.Join(tips, "\n")
}
