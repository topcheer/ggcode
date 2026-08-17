package agent

import (
	"regexp"
	"strings"
	"sync"

	"github.com/topcheer/ggcode/internal/debug"
)

// Premature success declaration detection.
//
// When an agent edits files and then declares success ("all tests pass",
// "done", "fixed", "task complete") WITHOUT having actually run verification
// (build, test, lint) after those edits, it's making an unverified success
// claim. This is one of the most common and costly agent failure modes: the
// agent believes its work is correct, reports completion, and the user
// discovers breakage later.
//
// Research basis:
//   - Epistemic Alignment paradigm (Chong et al., Dec 2025): agents should
//     quantify confidence and avoid claiming knowledge they don't have.
//   - "Phantom verification" patterns in coding agent evaluations (2025-2026):
//     agents claim to have verified without actually doing so.
//
// This is distinct from:
//   - phantom_verify: detects claiming a specific verification action that
//     wasn't performed (e.g., "I ran the tests" when no test tool was called).
//     This detector catches BROADER success claims ("done", "fixed") that
//     imply correctness without naming a specific verification action.
//   - truncated_completeness_fallacy: model stops generating due to output
//     limits. This detector addresses the agent's JUDGMENT being wrong.
//   - satisficing_settle: accepting "good enough" solution. This detector
//     catches claiming success when the solution hasn't been verified at all.
//   - fulfillment_gate: checks task completion criteria. This detector is
//     about the TIMING gap between edits and verification.

const (
	// prematureSuccessMaxFires caps guidance injections per run.
	prematureSuccessMaxFires = 1

	// verifyCommandPatterns are substrings that indicate a command is a
	// verification action (build, test, lint, etc.).
	// Checked against the run_command tool's command argument.
)

// verifyCmdPatterns are patterns for verification commands.
var verifyCmdPatterns = []string{
	"build", "test", "lint", "vet", "verify", "check", "compile",
	"go test", "go build", "go vet", "npm test",
	"cargo test", "cargo build", "pytest", "jest", "rspec",
	"dotnet test", "dotnet build",
}

// makeVerifyTargets is the whitelist of make targets that count as
// verification (#350). Aligned with convergence_lock.go's precedent:
// clean/fmt/tidy and other hygiene targets are NOT verification — a bare
// "make " substring match previously counted `make clean` as verification
// and silenced all subsequent success-claim warnings for the whole run.
var makeVerifyTargets = map[string]bool{
	"test": true, "tests": true, "verify": true, "ci": true, "build": true,
	"lint": true, "check": true, "e2e": true, "validate": true, "integration": true,
}

// npmVerifyScripts is the whitelist of npm script names that count as
// verification (#350). dev/start/serve/watch boot a service — they are
// manual smoke checks at best, not verification.
var npmVerifyScripts = map[string]bool{
	"test": true, "tests": true, "build": true, "lint": true, "check": true,
	"verify": true, "e2e": true, "ci": true, "typecheck": true, "validate": true,
}

// mvnVerifyPhases whitelists maven phases that count as verification (#350).
var mvnVerifyPhases = map[string]bool{
	"test": true, "verify": true, "validate": true, "check": true, "integration-test": true,
}

// gradleVerifyTasks whitelists gradle tasks that count as verification (#350).
var gradleVerifyTasks = map[string]bool{
	"test": true, "check": true, "verify": true, "build": true, "lint": true, "validate": true,
	"connectedCheck": true, "integrationTest": true,
}

// cmakeVerifyTargets whitelists cmake invocations that count as
// verification (#350). A bare "cmake " substring matched configure runs.
var cmakeVerifyTargets = map[string]bool{
	"--build": true, "--target": true,
}

// successClaimPatterns are regex patterns that match success declarations.
// These are specifically phrased as ASSERTIONS of completion/correctness,
// not conditional or future-tense references.
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
	// #595: gerund completed-action claims — "After applying the fix, all
	// tests pass" / "After running the tests, everything passes" / "Once
	// applied, the fix resolves the issue" are present-tense assertions.
	regexp.MustCompile(`(?i)\beverything\s+(?:passes|passed|works|working|succeeds|succeeded)\b`),
	regexp.MustCompile(`(?i)\b(?:fix|patch|change)\s+(?:resolves|resolved|fixes|fixed)\b`),
}

// conditionalGuardWords prevent false positives - if these appear near the
// success phrase, it's likely conditional/hypothetical, not a declaration.
var conditionalGuardWords = []string{
	"if ", "when ", "should ", "would ", "could ", "might ", "may ",
	"need to ", "want to ", "going to ", "about to ",
	"once ", "after ", "before ",
}

// verifyTools are tool names that constitute verification actions.
// Aligned with false_premise_check.go fpIsBuildTestTool (#595).
var verifyTools = map[string]bool{
	"run_command":         true, // checked further via command argument
	"start_command":       true, // checked further via command argument
	"wait_command":        true,
	"task_output":         true,
	"read_command_output": true,
	"ci_status":           true,
}

// psEditTools are tool names that modify files (require re-verification).
var psEditTools = map[string]bool{
	"edit_file":       true,
	"write_file":      true,
	"multi_edit_file": true,
	"multi_file_edit": true,
	"file_ops":        true,
	"batch_replace":   true,
}

// prematureSuccessState tracks edits and verification across a run.
type prematureSuccessState struct {
	mu                  sync.Mutex
	editsSinceVerify    int  // edits made since last verification
	everVerified        bool // has any verification been done this run?
	lastVerifyFailed    bool // last verification command errored (#350)
	lastVerifyFailedCmd string
	guidanceFired       int // how many times guidance was injected
}

func newPrematureSuccessState() *prematureSuccessState {
	return &prematureSuccessState{}
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

// recordToolCall updates state based on tool calls. isError is the tool
// result's IsError flag (#350): a FAILED verification must not clear the
// edit counter — a subsequent success claim then contradicts an observed
// failure, which is worse than claiming success without verifying at all.
func (p *prematureSuccessState) recordToolCall(toolName string, args map[string]interface{}, isError bool) {
	p.mu.Lock()
	defer p.mu.Unlock()

	if psEditTools[toolName] {
		p.editsSinceVerify++
		return
	}

	if verifyTools[toolName] {
		// For run_command and start_command, check if the command is actually
		// a verify command (#595).
		if toolName == "run_command" || toolName == "start_command" {
			cmd, _ := args["command"].(string)
			if !psIsVerifyCommand(cmd) {
				return
			}
		}
		if isError {
			// Failed verification: do NOT reset the edit counter and do not
			// count it as a passing verification. Record it so a later success
			// claim can be flagged as contradicting an observed failure (#350).
			p.lastVerifyFailed = true
			if cmd, ok := args["command"].(string); ok {
				p.lastVerifyFailedCmd = cmd
			}
			return
		}
		// Successful verification: clear edit counter and failure state.
		// wait_command, task_output, read_command_output success results
		// count as verification completion (#595).
		p.editsSinceVerify = 0
		p.everVerified = true
		p.lastVerifyFailed = false
		p.lastVerifyFailedCmd = ""
	}
}

// psIsVerifyCommand checks if a command string is a verification action.
// Single-word patterns use token matching (avoids "check" matching
// "checkout"); build-system invocations (make/npm run/mvn/gradle/cmake) use
// target whitelists so hygiene/service commands (make clean, npm run dev)
// do NOT count as verification (#350).
func psIsVerifyCommand(cmd string) bool {
	if cmd == "" {
		return false
	}
	lower := strings.ToLower(cmd)
	tokens := strings.Fields(lower)
	if len(tokens) == 0 {
		return false
	}

	// Build-system dispatch with target whitelists (#350).
	if isVerify, handled := psBuildSystemVerify(tokens); handled {
		return isVerify
	}

	// Generic patterns: phrase patterns use command-position match
	// (issue #593 P4) to avoid false positives like `grep -n "go test" Makefile`;
	// single-word patterns only match in COMMAND position (#553).
	for _, pat := range verifyCmdPatterns {
		if strings.Contains(pat, " ") {
			// Multi-word phrases must match at command position, not anywhere
			// in the string. Use psCommandPositionTokens for the check.
			cmdPosTokens := psCommandPositionTokens(tokens)
			for t := range cmdPosTokens {
				// Check if the multi-word pattern matches at this token position
				// by reconstructing the substring starting from this token.
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
		// #553: a verify verb token (test/build/lint/verify/check/...) is only a
		// verification action when it appears in COMMAND position — the first
		// token of a pipeline segment, or the subcommand following a known
		// runner (go/cargo/python -m/...). Bare token matching at ANY position
		// let `grep -n test main.go` ("test" is a filename argument) arm
		// everVerified and silence the detector for the entire run — the same
		// consequence #483 fixed for hyphen-prefixed variants, but the exact
		// bare-token match was left behind.
		for t := range psCommandPositionTokens(tokens) {
			if t == pat {
				return true
			}
			// Hyphen/underscore variants (check-all, test-flight) are only
			// trusted at SEGMENT-FIRST position — after a runner they are
			// indistinguishable from script filenames (`go run lint_script.go`).
			if psSegmentFirstTokens(tokens)[t] && (strings.HasPrefix(t, pat+"-") || strings.HasPrefix(t, pat+"_")) {
				return true
			}
		}
	}
	return false
}

// psBuildSystemVerify applies the build-system target whitelists (#350) to
// a tokenized command. It reports (isVerify, handled): handled=false means
// tokens[0] is not a build-system dispatcher and the caller should fall
// through to generic pattern matching. Hygiene/service targets (make clean,
// npm run dev) are NOT verification — a bare substring match previously
// counted them and silenced the detector for the whole run.
func psBuildSystemVerify(tokens []string) (isVerify, handled bool) {
	switch tokens[0] {
	case "make", "gmake", "mingw32-make":
		// Only whitelisted targets count; `make` with no explicit target runs
		// the default goal (often `all`/bootstrap) which is not verification.
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
		// npm test / npm run <verify-script> count; dev/start/serve do not.
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
	case "mvn", "mvnw":
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

// psRunnerPrefixes are commands whose NEXT token is a subcommand verb in
// command position ("go test", "cargo build", "flutter analyze", "uv run
// pytest"). Build-system dispatchers (make/npm/mvn/gradle/cmake) are included
// so their targets are command-position inside compound commands
// ("cd pkg; make test"), where the tokens[0] switch cannot see them.
var psRunnerPrefixes = map[string]bool{
	"go": true, "cargo": true, "uv": true, "poetry": true, "npx": true,
	"bunx": true, "dotnet": true, "deno": true, "ruby": true, "bundle": true,
	"flutter": true, "dart": true, "python": true, "python3": true,
	"run":  true, // uv/poetry/deno run <script> — the script is the real command
	"make": true, "gmake": true, "mingw32-make": true,
	"npm": true, "yarn": true, "pnpm": true, "bun": true,
	"mvn": true, "mvnw": true, "gradle": true, "gradlew": true, "./gradlew": true,
	"cmake": true,
}

// indexOfToken returns the index of token in the tokens slice, or -1 if not found.
func indexOfToken(tokens []string, token string) int {
	for i, t := range tokens {
		if t == token {
			return i
		}
	}
	return -1
}

// psSegmentFirstTokens returns the tokens that occupy the FIRST position of
// a pipeline/list segment (the tokens[0]-equivalent for each segment). Used
// for the more permissive hyphen-variant matching: a bare "check-all" or
// "test-flight" as the command itself is a verification verb, but the same
// string at a non-first position is almost always a file/dir name.
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

// psCommandPositionTokens collects all tokens that occupy COMMAND position
// in a tokenized shell command (#553): the first token of each pipeline /
// list segment (split on |, ||, &&, ;) plus the token immediately following
// a known runner prefix (go, cargo, ...) or a "-m" module flag
// ("python -m pytest"). Tokens at argument positions (file names, flags)
// are excluded so filename arguments like `grep -n test main.go` cannot
// masquerade as verification verbs.
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

// checkSuccessClaim scans assistant text for success declarations and returns
// guidance text if the claim is unverified (edits were made without subsequent
// verification). Returns "" if no guidance needed.
func (p *prematureSuccessState) checkSuccessClaim(assistantText string) string {
	p.mu.Lock()
	defer p.mu.Unlock()

	if p.guidanceFired >= prematureSuccessMaxFires {
		return ""
	}

	// Must have unverified edits for the claim to be premature.
	if p.editsSinceVerify == 0 {
		return ""
	}

	// Scan for success claim patterns.
	claimFound := false
	for _, re := range successClaimPatterns {
		loc := re.FindStringIndex(assistantText)
		if loc == nil {
			continue
		}
		// Check for conditional guard words near the match (within 40 chars before).
		// Exception (#595): "after" and "once" are only guard words when followed
		// by incomplete verb forms (participles, future tense). Past-tense assertions
		// like "After applying the fix, all tests pass" are legitimate claims.
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
			// For "once " and "after ", only genuinely incomplete/future forms
			// guard the claim (#595). A gerund + object ("applying the fix",
			// "running the tests") or a participle ("once applied") denotes a
			// COMPLETED action, so the claim stands. In-progress/future markers
			// — "will ", "going to", "pending", "now" right after the guard
			// word ("After applying now...") — keep the guard.
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
				// Completed-action phrasing is NOT a guard.
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
		// Stronger contradiction warning (#350): the claim directly contradicts
		// a verification that just FAILED, which is worse than no verification.
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
