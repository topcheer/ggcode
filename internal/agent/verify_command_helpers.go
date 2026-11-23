package agent

// Residual pure helpers recovered from the removed advisory-gate files
// (irrev_gate.go, premature_success.go, tool_arg_complexity.go) during the
// advisory-gate removal sweep. These are pure command-classification /
// arg-sanitization functions still referenced by non-gate call sites
// (branch_delete_probe, bare_edit_streak, agent_tool arg-rationale
// stripping) and their regression tests. No advisory state, no injection.
import (
	"encoding/json"
	"regexp"
	"strings"
)

var verifyCmdPatterns = []string{
	"build", "test", "lint", "vet", "verify", "check", "compile",
	"go test", "go build", "go vet", "npm test",
	"cargo test", "cargo build", "pytest", "jest", "rspec",
	"dotnet test", "dotnet build",
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

func irrevSplitShellSegments(cmd string) []string {
	// #5269: the splitter was quote-blind, so a quoted jq program like
	// `gh api ... -q '.[] | select(.x) | .number]'` was shredded on the pipes
	// INSIDE the quotes; the fragment then failed the read-only table and the
	// whole GET-class patrol call gated (the "0 grounding" FP). Split on
	// separators only OUTSIDE single/double quotes.
	var segs []string
	var cur strings.Builder
	var quote rune
	for _, r := range cmd {
		switch {
		case quote != 0:
			if r == quote {
				quote = 0
			}
			cur.WriteRune(r)
		case r == '\'' || r == '"':
			quote = r
			cur.WriteRune(r)
		case r == ';' || r == '|' || r == '&' || r == '\n':
			if cur.Len() > 0 {
				segs = append(segs, cur.String())
			}
			cur.Reset()
		default:
			cur.WriteRune(r)
		}
	}
	if cur.Len() > 0 {
		segs = append(segs, cur.String())
	}
	return segs
}

func isEnvAssignment(tok string) bool {
	eq := strings.Index(tok, "=")
	if eq <= 0 {
		return false
	}
	for j, r := range tok[:eq] {
		if !(r == '_' || (r >= 'a' && r <= 'z') || (r >= 'A' && r <= 'Z') || (j > 0 && r >= '0' && r <= '9')) {
			return false
		}
	}
	return true
}

const argRationaleField = "arg_rationale"

var argComplexityWriteTools = map[string]bool{
	"run_command":      true,
	"edit_file":        true,
	"multi_edit_file":  true,
	"multi_file_edit":  true,
	"write_file":       true,
	"multi_file_write": true,
	"batch_replace":    true,
	"file_ops":         true,
	"notebook_edit":    true,
	"git_checkout":     true,
	"git_reset":        true,
	"git_revert":       true,
	"git_add":          true,
	"git_commit":       true,
}

func stripArgRationale(args json.RawMessage) json.RawMessage {
	if len(args) == 0 || !strings.Contains(string(args), argRationaleField) {
		return args
	}
	var call map[string]json.RawMessage
	if err := json.Unmarshal(args, &call); err != nil {
		return args
	}
	if _, ok := call[argRationaleField]; !ok {
		return args
	}
	delete(call, argRationaleField)
	b, err := json.Marshal(call)
	if err != nil {
		return args
	}
	return b
}

func extractArgRationale(args json.RawMessage) string {
	if len(args) == 0 || !strings.Contains(string(args), argRationaleField) {
		return ""
	}
	var call map[string]json.RawMessage
	if err := json.Unmarshal(args, &call); err != nil {
		return ""
	}
	raw, ok := call[argRationaleField]
	if !ok {
		return ""
	}
	var s string
	if err := json.Unmarshal(raw, &s); err != nil {
		return ""
	}
	runes := []rune(s)
	if len(runes) > 500 {
		runes = runes[:500]
	}
	return strings.TrimSpace(string(runes))
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

func intToStr(n int) string {
	if n == 0 {
		return "0"
	}
	var buf [20]byte
	pos := len(buf)
	neg := n < 0
	if neg {
		n = -n
	}
	for n > 0 {
		pos--
		buf[pos] = byte('0' + n%10)
		n /= 10
	}
	if neg {
		pos--
		buf[pos] = '-'
	}
	return string(buf[pos:])
}

func indexOfToken(tokens []string, token string) int {
	for i, t := range tokens {
		if t == token {
			return i
		}
	}
	return -1
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

// minMatchPatternLen pins the minimum learned-rule pattern length (data-layer
// guard from the former ratchet_reactive advisory; RuleStore keeps it).
const minMatchPatternLen = 8

var conditionalGuardWords = []string{
	"if ", "when ", "should ", "would ", "could ", "might ", "may ",
	"need to ", "want to ", "going to ", "about to ",
	"once ", "after ", "before ",

	"no ", "zero ", "none ", "0 ",
	"make ",
}

var psEditTools = sourceMutatingTools

type psJobRecord struct {
	isVerify bool
	cmd      string
	subAgent bool
}

const psMaxTrackedJobs = 64

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

var verifyTools = map[string]bool{
	"run_command":         true,
	"start_command":       true,
	"wait_command":        true,
	"task_output":         true,
	"read_command_output": true,
	"ci_status":           true,
	"merge_preflight":     true,
}
