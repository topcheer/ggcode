package agent

import (
	"fmt"
	"strings"

	"github.com/topcheer/ggcode/internal/debug"
)

var successClaimPhrases = []string{
	"tests pass", "all tests pass", "test passes", "tests are passing",
	"all passing", "tests passing", "unit tests pass",
	"build passes", "build succeeds", "build is successful",
	"build passes successfully", "compiles successfully", "compiles fine",
	"lint clean", "linting passes", "lint passes",
	"verified", "verification passed", "verification successful",
	"confirmed working", "confirmed it works", "confirmed the fix",
	"validated", "validation passed",
	"all green", "everything passes", "all checks pass",
	"ci passes", "ci will pass",
	"no errors", "no compilation errors", "no test failures",
	"all tests successful", "tests are successful",
	"type checks", "type checking passes",
}

var verificationToolNames = map[string]bool{
	"lsp_diagnostics": true,
	"code_health":     true,
	"review_changes":  true,
	"scan_todos":      true,
}

type unverifiedClaimState struct {
	fired bool
}

func newUnverifiedClaimState() *unverifiedClaimState {
	return &unverifiedClaimState{}
}

func (u *unverifiedClaimState) reset() {
	u.fired = false
}

func (a *Agent) checkUnverifiedClaim(assistantText string, runStats *RunStats) string {
	if a.unverifiedClaim.fired {
		return ""
	}

	text := strings.ToLower(assistantText)
	if len(strings.TrimSpace(text)) < 5 {
		return ""
	}

	claims := detectSuccessClaims(text)
	if len(claims) == 0 {
		return ""
	}

	if hasVerificationCommands(runStats) {
		debug.Log("unverified-claim", "claims found (%v) but verification commands present — OK", claims[:min(3, len(claims))])
		return ""
	}

	if hasVerificationTools(runStats) {
		debug.Log("unverified-claim", "claims found (%v) but verification tools present — OK", claims[:min(3, len(claims))])
		return ""
	}

	a.unverifiedClaim.fired = true
	debug.Log("unverified-claim", "unverified success claims detected: %v", claims[:min(3, len(claims))])

	return fmt.Sprintf(
		"Before finishing: your response claims success (%s), but no build, test, or verification commands "+
			"were run during this session. If you are asserting that code compiles, tests pass, or lint is clean, "+
			"run the appropriate verification command (e.g. build, test, lint) to confirm. "+
			"If your claims refer to pre-existing conditions or analysis rather than new verification, "+
			"clarify that explicitly in your response.",
		strings.Join(claims[:min(3, len(claims))], ", "),
	)
}

func detectSuccessClaims(text string) []string {
	lower := strings.ToLower(text)
	var found []string
	seen := make(map[string]bool)
	for _, phrase := range successClaimPhrases {
		if !seen[phrase] && containsWordBounded(lower, phrase) {
			found = append(found, phrase)
			seen[phrase] = true
		}
	}
	return found
}

func containsWordBounded(lower, phrase string) bool {
	p := strings.ToLower(phrase)
	for from := 0; ; {
		idx := strings.Index(lower[from:], p)
		if idx < 0 {
			return false
		}
		abs := from + idx
		beforeOK := abs == 0 || !claimIsWordByte(lower[abs-1])
		after := abs + len(p)
		afterOK := after >= len(lower) || !claimIsWordByte(lower[after])
		if beforeOK && afterOK {
			return true
		}
		from = abs + 1
	}
}

func claimIsWordByte(b byte) bool {
	return b >= 'a' && b <= 'z' || b >= 'A' && b <= 'Z' || b >= '0' && b <= '9' || b == '_'
}

func isBuildTestCommand(lower string) bool {
	prefixes := []string{
		"go build", "go test", "go vet",
		"cargo ", "cmake",
		"npm run", "yarn ", "pnpm ", "npx ",
		"flutter ", "dart ", "gradle", "mvn ",
		"pytest", "python -m pytest", "python -m unittest",
		"./scripts/", "bash scripts/", "sh scripts/",
	}
	for _, prefix := range prefixes {
		if strings.HasPrefix(lower, prefix) {
			return true
		}
	}

	if strings.HasPrefix(lower, "make") {
		rest := strings.TrimPrefix(lower, "make")
		if strings.HasPrefix(rest, " ") || strings.HasPrefix(rest, "\t") {
			target := strings.TrimSpace(rest)
			for _, m := range []string{"test", "check", "verify", "lint"} {
				if target == m || strings.HasPrefix(target, m+"-") || strings.HasPrefix(target, m+"_") {
					return true
				}
			}
		}
	}

	for _, runner := range []string{"pytest", "jest", "vitest"} {
		if claimContainsWord(lower, runner) {
			return true
		}
	}
	return false
}

func claimContainsWord(s, w string) bool {
	for i := 0; i+len(w) <= len(s); i++ {
		if s[i:i+len(w)] != w {
			continue
		}
		beforeOK := i == 0 || !claimIsWordByte(s[i-1])
		afterOK := i+len(w) == len(s) || !claimIsWordByte(s[i+len(w)])
		if beforeOK && afterOK {
			return true
		}
	}
	return false
}

func hasVerificationCommands(runStats *RunStats) bool {
	for _, cmd := range runStats.CommandsRun {
		lower := strings.ToLower(stripCommandComment(cmd))
		if isBuildTestCommand(lower) {

			if commandFailed(runStats, cmd) {
				continue
			}
			return true
		}
	}
	return false
}

func commandFailed(runStats *RunStats, cmd string) bool {
	if len(runStats.Errors) == 0 {
		return false
	}
	base := strings.TrimSpace(stripCommandComment(cmd))
	if base == "" {
		return false
	}

	fields := strings.Fields(base)
	if len(fields) == 0 {
		return false
	}
	anchor := strings.ToLower(fields[0])
	if len(fields) > 1 {
		anchor = strings.ToLower(fields[0] + " " + fields[1])
	}
	for _, e := range runStats.Errors {
		el := strings.ToLower(e)
		if strings.Contains(el, anchor) && (strings.Contains(el, "fail") || strings.Contains(el, "exit status") || strings.Contains(el, "non-zero") || strings.Contains(el, "error")) {
			return true
		}
	}
	return false
}

func hasVerificationTools(runStats *RunStats) bool {
	for toolName := range runStats.ToolCalls {
		if verificationToolNames[toolName] {
			return true
		}
	}
	return false
}
