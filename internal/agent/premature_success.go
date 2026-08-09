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

// verifyCmdPatterns are regex patterns for verification commands.
var verifyCmdPatterns = []string{
	"build", "test", "lint", "vet", "verify", "check", "compile",
	"make ", "go test", "go build", "go vet", "npm test", "npm run",
	"cargo test", "cargo build", "pytest", "jest", "rspec", "mvn ",
	"gradle ", "cmake ", "dotnet test", "dotnet build",
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
}

// conditionalGuardWords prevent false positives - if these appear near the
// success phrase, it's likely conditional/hypothetical, not a declaration.
var conditionalGuardWords = []string{
	"if ", "when ", "should ", "would ", "could ", "might ", "may ",
	"need to ", "want to ", "going to ", "about to ",
	"once ", "after ", "before ",
}

// verifyTools are tool names that constitute verification actions.
var verifyTools = map[string]bool{
	"run_command": true, // checked further via command argument
	"ci_status":   true,
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
	mu               sync.Mutex
	editsSinceVerify int  // edits made since last verification
	everVerified     bool // has any verification been done this run?
	guidanceFired    int  // how many times guidance was injected
}

func newPrematureSuccessState() *prematureSuccessState {
	return &prematureSuccessState{}
}

func (p *prematureSuccessState) reset() {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.editsSinceVerify = 0
	p.everVerified = false
	p.guidanceFired = 0
}

// recordToolCall updates state based on tool calls.
func (p *prematureSuccessState) recordToolCall(toolName string, args map[string]interface{}) {
	p.mu.Lock()
	defer p.mu.Unlock()

	if psEditTools[toolName] {
		p.editsSinceVerify++
		return
	}

	if verifyTools[toolName] {
		// For run_command, check if the command is actually a verify command.
		if toolName == "run_command" {
			cmd, _ := args["command"].(string)
			if !psIsVerifyCommand(cmd) {
				return
			}
		}
		p.editsSinceVerify = 0
		p.everVerified = true
	}
}

// psIsVerifyCommand checks if a command string contains verification patterns.
func psIsVerifyCommand(cmd string) bool {
	if cmd == "" {
		return false
	}
	lower := strings.ToLower(cmd)
	for _, pat := range verifyCmdPatterns {
		if strings.Contains(lower, pat) {
			return true
		}
	}
	return false
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
		start := loc[0] - 40
		if start < 0 {
			start = 0
		}
		contextBefore := assistantText[start:loc[0]]
		guarded := false
		for _, gw := range conditionalGuardWords {
			if strings.Contains(strings.ToLower(contextBefore), gw) {
				guarded = true
				break
			}
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
	debug.Log("premature-success", "unverified success claim detected (editsSinceVerify=%d, everVerified=%v)",
		p.editsSinceVerify, p.everVerified)

	var tips []string
	tips = append(tips,
		"Premature success declaration detected: you've claimed success but made edits without running verification afterward.",
		"Before declaring completion, you MUST run verification (build + test):",
		"1. Run the project's build command (e.g., go build, make build)",
		"2. Run the project's test command (e.g., go test ./..., make test)",
		"3. Only declare success after verification passes with no errors",
	)
	if !p.everVerified {
		tips = append(tips, "Note: no verification has been run at all in this session.")
	}

	return "WARNING: " + strings.Join(tips, "\n")
}
