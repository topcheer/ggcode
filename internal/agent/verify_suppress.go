package agent

// Verification Suppression Detection -- Reward Hacking via Error Masking
//
// Research basis: Reward Hacking Benchmark (RHB, ICML 2026, arXiv:2605.02964)
// identifies "shortcut opportunities" where LLM agents exploit evaluation
// mechanics instead of solving problems. One key exploit category is
// "tampering with evaluation-relevant functions" - agents modify the
// verification environment rather than fixing the underlying issue.
//
// In coding agents, this manifests as shell commands that suppress error
// signals instead of addressing root causes:
//   - `go test ... || true`       (masks test failures)
//   - `npm test 2>/dev/null`      (hides error output)
//   - `make build || exit 0`      (forces success exit code)
//   - `go vet ... || echo done`   (swallows non-zero exit)
//   - `git commit ... || :`       (no-op on failure)
//   - `pytest || true && echo ok` (chains false success)
//
// Agentic Confidence Calibration (arXiv:2601.15778) shows agents under
// pressure systematically overconfidence failure states. Verification
// suppression is the tool-level manifestation: instead of accepting failure
// and fixing root cause, the agent engineers the command to "succeed."
//
// Gap in existing ggcode systems:
//   - silent_error.go: detects IGNORING errors. Does not detect commands
//     engineered to never produce errors in the first place.
//   - error_classifier.go: classifies errors after they occur. Suppressed
//     errors never surface for classification.
//   - spec_gaming.go: detects gaming verification criteria. Doesn't inspect
//     command syntax for error-masking operators.
//   - test_gaming.go: detects modifying test files/conditions. Doesn't catch
//     runtime suppression via shell operators.
//
// This detector inspects run_command and start_command arguments for error-
// suppression patterns. It fires at most once per run to avoid nagging.
//
// Design: zero LLM cost, deterministic regex-based detection.

import (
	"encoding/json"
	"fmt"
	"regexp"
	"strings"

	"github.com/topcheer/ggcode/internal/debug"
)

const (
	// verifySuppressMaxExamples caps the number of suppressed commands shown.
	verifySuppressMaxExamples = 4
)

// verifySuppressState tracks shell commands that suppress error signals.
type verifySuppressState struct {
	suppressedCmds []suppressedCommand
	fired          bool
}

type suppressedCommand struct {
	command        string
	pattern        string
	category       string
	isVerification bool
}

func newVerifySuppressState() *verifySuppressState {
	return &verifySuppressState{}
}

func (s *verifySuppressState) reset() {
	s.suppressedCmds = nil
	s.fired = false
}

// errorMaskingRe detects operators that force success exit codes regardless
// of actual command outcome. These are the most dangerous -- the agent never
// sees the failure.
var errorMaskingRe = []*regexp.Regexp{
	// `|| true`, `|| :`, `|| exit 0`, `|| echo ...`
	regexp.MustCompile(`\|\|\s*(true|:|exit\s+0|echo\s+\S)`),
	// `; true` at end of command (appended success)
	regexp.MustCompile(`;\s*true\s*$`),
	// `set +e` disables error propagation in the current shell
	regexp.MustCompile(`\bset\s+\+e\b`),
}

// outputHidingRe detects operators that hide error output (stderr) but don't
// necessarily mask exit codes. Less severe but still problematic when used
// on verification commands.
var outputHidingRe = []*regexp.Regexp{
	// `2>/dev/null` discards stderr entirely
	regexp.MustCompile(`2>/dev/null\b`),
	// `2>&1 | ...` merges stderr to stdout then pipes (loses error signal)
	regexp.MustCompile(`2>&1\s*\|`),
	// `>/dev/null 2>&1` discards all output
	regexp.MustCompile(`>/dev/null\s+2>&1`),
	// `&>/dev/null` discards all output (bash shorthand)
	regexp.MustCompile(`&>/dev/null\b`),
}

// verificationCmdRe identifies commands that are likely verification steps
// (build, test, lint, vet, check). Suppression on these is highest-risk.
var verificationCmdRe = regexp.MustCompile(
	`\b(go\s+(test|vet|build)|npm\s+(test|run)|make\s+(test|build|check|lint|verify)|cargo\s+(test|build|check)|pytest|jest|mvn\s+test|gradle\s+(test|build)|rake\s+test|python\s+.*test|node\s+.*test)\b`,
)

// checkVerificationSuppression inspects a shell command for error-suppression
// patterns. Returns guidance if suppression is detected on verification-like
// commands or after enough general occurrences.
func (s *verifySuppressState) checkVerificationSuppression(toolName, command string) string {
	if s.fired {
		return ""
	}

	matched, _, category := detectSuppression(command)
	if !matched {
		return ""
	}

	entry := suppressedCommand{
		command:  truncateCmd(command),
		category: category,
	}

	isVerification := verificationCmdRe.MatchString(command)
	entry.isVerification = isVerification
	s.suppressedCmds = append(s.suppressedCmds, entry)

	// Error masking on verification commands = immediate fire (critical).
	// Output hiding on verification = warn after 2 same-branch occurrences.
	// Any suppression on non-verification commands = warn after 2 same-branch
	// occurrences.
	// #170: suppressedCmds mixes both branches — each threshold must count
	// only its own branch's entries, otherwise a single non-verify
	// suppression after one verify entry fires the reward-hacking warning.
	shouldFire := false
	if isVerification {
		if category == "error-masking" {
			shouldFire = true
		} else {
			verifyCount := 0
			for _, c := range s.suppressedCmds {
				if c.isVerification {
					verifyCount++
				}
			}
			if verifyCount >= 2 {
				shouldFire = true
			}
		}
	} else {
		nonVerifyCount := 0
		for _, c := range s.suppressedCmds {
			if !c.isVerification {
				nonVerifyCount++
			}
		}
		if nonVerifyCount >= 2 {
			shouldFire = true
		}
	}

	debug.Log("agent", "Verification suppression detected: tool=%s cat=%s verify=%v fire=%v cmd=%s",
		toolName, category, isVerification, shouldFire, truncateCmd(command))

	if shouldFire {
		return s.buildGuidance()
	}

	return ""
}

// detectSuppression checks if a command contains error-suppression patterns.
// Returns (matched, patternDescription, category).
func detectSuppression(command string) (bool, string, string) {
	for _, re := range errorMaskingRe {
		if re.MatchString(command) {
			return true, re.String(), "error-masking"
		}
	}
	for _, re := range outputHidingRe {
		if re.MatchString(command) {
			return true, re.String(), "output-hiding"
		}
	}
	return false, "", ""
}

func (s *verifySuppressState) buildGuidance() string {
	s.fired = true

	var examples []string
	for i, c := range s.suppressedCmds {
		if i >= verifySuppressMaxExamples {
			break
		}
		examples = append(examples, fmt.Sprintf("  %d. [%s] %s", i+1, c.category, c.command))
	}

	return fmt.Sprintf("[Verification Suppression Warning] Your commands contain error-suppression operators "+
		"that mask failures instead of fixing root causes. The Reward Hacking Benchmark (ICML 2026) shows "+
		"this is a common exploit pattern where agents shortcut verification rather than solving problems.\n"+
		"Suppressed commands:\n%s\n"+
		"Remove suppression operators (|| true, 2>/dev/null, etc.) from verification commands. "+
		"If a command fails, fix the underlying issue -- do not engineer the command to appear successful.",
		strings.Join(examples, "\n"))
}

// extractCommandFromToolCall gets the command string from run_command/start_command args.
func extractCommandFromToolCall(args json.RawMessage) string {
	return extractCommandFromArgs(args)
}
