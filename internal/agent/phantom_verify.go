package agent

// Phantom Verification Detector -- Process Supervision Gap
//
// Research basis:
//   - AgentPro (EMNLP 2025, Ma et al.): "Enhancing LLM Agents with Automated
//     Process Supervision" -- each claimed step in an agent trajectory should be
//     backed by a real verification action, not just an assertion.
//   - StepORLM (arXiv:2509.22558, 2025): step-level supervision ensures each
//     modeling step is verified, mitigating credit-assignment errors.
//   - AgentLiar Detector (2025, Nilofer): false completion claims where agents
//     assert task success without actual verification runs.
//   - Lightman et al., "Let's Verify Step by Step" (2024): process-level rewards
//     catch errors that outcome-only verification misses.
//
// Problem: Coding agents sometimes assert a SPECIFIC verification OUTCOME --
// "the build passes", "all tests pass", "lint reports no errors", "the code
// compiles cleanly" -- without having actually run the corresponding verification
// command. This is distinct from generic overconfidence ("this definitely works"
// caught by unverified_confidence.go) because it makes a falsifiable, testable
// claim tied to a specific verification category (build/test/lint/typecheck).
//
// If the agent claims "tests pass" but no test command was run, or claims "the
// build compiles" but no build/compile command was run, that is a phantom
// verification -- a process-supervision gap. The claim has zero grounding in the
// trajectory's actions.
//
// This detector:
//   1. Scans assistant text for concrete verification-outcome claims, categorized
//      by verification type (build, test, lint, typecheck, compile).
//   2. Tracks which verification command categories were actually run.
//   3. When a claim of category X exists but no verification command of category
//      X was run, injects a targeted reminder.
//
// Design:
//   - Zero LLM cost -- pure deterministic regex matching + tool call tracking.
//   - Non-blocking advisory hint, capped at 2 injections per run.
//   - Only fires when a CATEGORY-SPECIFIC verification claim lacks matching
//     evidence, not on generic confidence language.
//   - Runs of a verification command reset that category's claim window so
//     legitimate "I ran tests, they pass" sequences are not flagged.

import (
	"encoding/json"
	"fmt"
	"regexp"
	"strings"

	"github.com/topcheer/ggcode/internal/debug"
)

const (
	phantomVerifyMaxWarnings = 2
	phantomVerifyMaxExamples = 3
)

// Verification categories for claim-to-command matching.
const (
	phantomCatBuild     = "build"
	phantomCatTest      = "test"
	phantomCatLint      = "lint"
	phantomCatCompile   = "compile"
	phantomCatTypecheck = "typecheck"
	phantomCatCI        = "ci"
)

// phantomClaimPatterns maps verification category to outcome-claim regexes.
// These match assertions that a SPECIFIC verification type yielded a positive
// result -- not generic "it works" language.
var phantomClaimPatterns = map[string][]*regexp.Regexp{
	phantomCatBuild: {
		regexp.MustCompile(`(?i)\bbuild(s|ing)?\s+(pass(es|ed)?|succeed(s|ed)?|complet(es|ed)?|is (green|ok|clean))\b`),
		regexp.MustCompile(`(?i)\b(make\s+)?build\s+(is\s+)?success(ful)?(ly)?\b`),
		regexp.MustCompile(`(?i)\bbuild\s+(verification|check)\s+(passed|succeeds?|is\s+green)\b`),
	},
	phantomCatTest: {
		regexp.MustCompile(`(?i)\b(all\s+)?tests?\s+(pass(ed|es)?|succeed(ed|s)?|are\s+(green|passing|ok))\b`),
		regexp.MustCompile(`(?i)\btest\s+(suite\s+)?(passed|succeeds?|is\s+green)\b`),
		regexp.MustCompile(`(?i)\bno\s+(failing|failed)\s+tests?\b`),
		regexp.MustCompile(`(?i)\btest\s+results?\s+(are\s+)?(green|passing|clean)\b`),
	},
	phantomCatLint: {
		regexp.MustCompile(`(?i)\blint(ing)?\s+(pass(es|ed)?|is\s+(clean|clear|ok)|report(s|ed)?\s+no\s+(issues|errors|warnings))\b`),
		regexp.MustCompile(`(?i)\bno\s+lint\s+(errors|warnings|issues)\b`),
		regexp.MustCompile(`(?i)\blint(ing)?\s+(check\s+)?(passed|is\s+clean)\b`),
	},
	phantomCatCompile: {
		regexp.MustCompile(`(?i)\bcompil(es|ed|ing)\s+(successfully|clean(ly)?|without\s+(errors|warnings))\b`),
		regexp.MustCompile(`(?i)\bthe\s+code\s+(compiles|builds|runs)\s+(correctly|fine|successfully|cleanly)\b`),
		regexp.MustCompile(`(?i)\bcompilation\s+(succeed(ed|s)?|is\s+(clean|successful))\b`),
	},
	phantomCatTypecheck: {
		regexp.MustCompile(`(?i)\bno\s+(compile|type)\s+errors?\b`),
		regexp.MustCompile(`(?i)\btype\s*check(ing)?\s+(pass(es|ed)?|succeed(s|ed)?|is\s+clean)\b`),
		regexp.MustCompile(`(?i)\bno\s+type\s+errors?\b`),
	},
}

// Verification command classification (issue #1159)
//
// Categories are armed from a tokenized command string instead of substring
// regexes over the raw text. Two disciplines are ported from
// premature_success.go so both detectors agree on what counts as a
// verification run:
//
//  1. Build-system target whitelists (issue #350): make, npm/yarn/pnpm/bun,
//     mvn, gradle and cmake arm categories only when invoked with a
//     whitelisted verify target. A bare "make", hygiene targets
//     ("make clean") and service scripts ("npm run dev") arm nothing.
//  2. Command-position matching (issue #553, extends #593 P4): category
//     keywords are recognized only as the first token of a shell segment
//     or immediately after a known runner prefix. Argument text such as
//     grep -n "go test" main.go must not arm anything.
//
// Known limitation shared with psIsVerifyCommand: commands wrapped inside an
// inner quoted shell string (sh -c "go test ./...") are not recognized,
// because quoted arguments do not tokenize into sub-commands.

// phantomVerifyTargetCategories maps a normalized whitelisted verify target
// name to the phantom categories it grounds (issue #350 discipline).
func phantomVerifyTargetCategories(target string) []string {
	switch strings.ToLower(target) {
	case "test", "tests", "e2e", "integration", "integration-test", "integrationtest":
		return []string{phantomCatTest}
	case "build":
		return []string{phantomCatBuild, phantomCatCompile}
	case "lint":
		return []string{phantomCatLint}
	case "typecheck":
		return []string{phantomCatTypecheck}
	}
	return nil
}

func phantomArmsTarget(targets map[string]bool, target string) []string {
	if !targets[strings.ToLower(target)] {
		return nil
	}
	return phantomVerifyTargetCategories(target)
}

// phantomPlainVerbs arms categories when the token itself occupies command
// position (segment head, or right after a runner or python -m prefix).
var phantomPlainVerbs = map[string][]string{
	"pytest":              {phantomCatTest},
	"jest":                {phantomCatTest},
	"mocha":               {phantomCatTest},
	"eslint":              {phantomCatLint},
	"flake8":              {phantomCatLint},
	"pylint":              {phantomCatLint},
	"ruff":                {phantomCatLint},
	"rubocop":             {phantomCatLint},
	"golangci":            {phantomCatLint},
	"golangci-lint":       {phantomCatLint},
	"clang-tidy":          {phantomCatLint},
	"shellcheck":          {phantomCatLint},
	"lint":                {phantomCatLint},
	"typecheck":           {phantomCatTypecheck},
	"tsc":                 {phantomCatBuild, phantomCatCompile, phantomCatTypecheck},
	"mypy":                {phantomCatTypecheck},
	"pyright":             {phantomCatTypecheck},
	"gcc":                 {phantomCatBuild, phantomCatCompile},
	"clang":               {phantomCatBuild, phantomCatCompile},
	"cc":                  {phantomCatCompile},
	"g++":                 {phantomCatCompile},
	"run_command":         nil,
	"start_command":       nil,
	"wait_command":        nil,
	"read_command_output": nil,
	"task_output":         nil,
	"ci_status":           {phantomCatCI}, // #593 P3: CI checks count as verification
}

// phantomRunners are tokens whose following token occupies command position
// as a subcommand (issue #553), mirroring psRunnerPrefixes in
// premature_success.go. The phantom command tools are included so their
// names never break pairing when recordToolCall falls back to concatenating
// them before the command (non-JSON payloads).
var phantomRunners = map[string]bool{
	"go":                  true,
	"cargo":               true,
	"uv":                  true,
	"poetry":              true,
	"npx":                 true,
	"bunx":                true,
	"dotnet":              true,
	"deno":                true,
	"ruby":                true,
	"bundle":              true,
	"flutter":             true,
	"dart":                true,
	"python":              true,
	"python3":             true,
	"swift":               true,
	"time":                true,
	"sudo":                true,
	"env":                 true,
	"timeout":             true,
	"nice":                true,
	"xargs":               true,
	"verbose":             true,
	"run":                 true,
	"make":                true,
	"gmake":               true,
	"mingw32-make":        true,
	"npm":                 true,
	"yarn":                true,
	"pnpm":                true,
	"bun":                 true,
	"mvn":                 true,
	"mvnw":                true,
	"gradle":              true,
	"gradlew":             true,
	"./gradlew":           true,
	"cmake":               true,
	"run_command":         true,
	"start_command":       true,
	"wait_command":        true,
	"read_command_output": true,
	"task_output":         true,
}

// phantomRunnerVerbs maps "<runner> <subcommand>" pairs for generic tool
// runners. Build-system dispatchers (make/npm/mvn/...) are resolved against
// their own whitelists instead and therefore do not appear here. "run" maps
// conservatively so wrappers like "time npm run test" stay recognized.
var phantomRunnerVerbs = map[string]map[string][]string{
	"go": {
		"build": {phantomCatBuild, phantomCatCompile, phantomCatTypecheck},
		"test":  {phantomCatTest},
		"vet":   {phantomCatLint, phantomCatTypecheck},
	},
	"cargo": {
		"build": {phantomCatBuild, phantomCatCompile},
		"test":  {phantomCatTest},
	},
	"run": {
		"test":      {phantomCatTest},
		"build":     {phantomCatBuild, phantomCatCompile},
		"lint":      {phantomCatLint},
		"typecheck": {phantomCatTypecheck},
	},
}

// phantomSegmentOps split shell segments (#553). Flags and assignments are
// ignored, matching psCommandPositionTokens semantics.
func phantomIsSegmentOp(tok string) bool {
	return tok == "|" || tok == "||" || tok == "&&" || tok == ";"
}

// phantomArmMake handles a segment headed by a make-family binary: the first
// non-flag, non-assignment word is the target and only whitelisted targets
// count (issue #350).
func phantomArmMake(seg []string, cats map[string]bool) {
	for _, t := range seg[1:] {
		if strings.HasPrefix(t, "-") || strings.Contains(t, "=") {
			continue
		}
		for _, c := range phantomArmsTarget(makeVerifyTargets, t) {
			cats[c] = true
		}
	}
}

// phantomArmNpmScript resolves npm/yarn/pnpm/bun script scripts. Handled
// unconditionally: these families never fall back to keyword matching (#350).
func phantomArmNpmScript(seg []string, cats map[string]bool) {
	scriptIdx := 1
	if len(seg) < 2 {
		return
	}
	if seg[1] == "run" {
		if len(seg) < 3 {
			return
		}
		scriptIdx = 2
	}
	for _, c := range phantomArmsTarget(npmVerifyScripts, seg[scriptIdx]) {
		cats[c] = true
	}
}

// phantomArmMvn scans maven phases for whitelisted verify phases (#350).
func phantomArmMvn(seg []string, cats map[string]bool) {
	for _, t := range seg[1:] {
		for _, c := range phantomArmsTarget(mvnVerifyPhases, t) {
			cats[c] = true
		}
	}
}

// phantomArmGradle scans gradle tasks against the whitelist (#350).
func phantomArmGradle(seg []string, cats map[string]bool) {
	for _, t := range seg[1:] {
		if strings.HasPrefix(t, "-") {
			continue
		}
		for _, c := range phantomArmsTarget(gradleVerifyTasks, t) {
			cats[c] = true
		}
	}
}

// phantomArmGeneric applies command-position matching to one shell segment
// that was not dispatched to a build-system whitelist family.
func phantomArmGeneric(seg []string, cats map[string]bool) {
	prevRunner := false
	prevDashM := false // python -m pytest / python3 -m flake8
	for i, t := range seg {
		cmdPos := i == 0 || prevRunner || prevDashM
		prevToken := ""
		if i > 0 {
			prevToken = seg[i-1]
		}
		switch prevToken {
		case "make", "gmake", "mingw32-make":
			for _, c := range phantomArmsTarget(makeVerifyTargets, t) {
				cats[c] = true
			}
		case "npm", "yarn", "pnpm", "bun":
			for _, c := range phantomArmsTarget(npmVerifyScripts, t) {
				cats[c] = true
			}
		case "mvn", "mvnw":
			for _, c := range phantomArmsTarget(mvnVerifyPhases, t) {
				cats[c] = true
			}
		case "gradle", "gradlew", "./gradlew":
			for _, c := range phantomArmsTarget(gradleVerifyTasks, t) {
				cats[c] = true
			}
		case "cmake":
			if cmakeVerifyTargets[t] || strings.HasPrefix(t, "--target=") {
				cats[phantomCatBuild] = true
				cats[phantomCatCompile] = true
			}
		default:
			if verbs := phantomRunnerVerbs[prevToken]; cmdPos || prevRunner || prevDashM {
				for _, c := range verbs[t] {
					cats[c] = true
				}
			}
			if cs := phantomPlainVerbs[t]; len(cs) > 0 && cmdPos {
				for _, c := range cs {
					cats[c] = true
				}
			}
		}
		prevRunner = phantomRunners[t]
		prevDashM = t == "-m"
	}
}

// phantomQuotedRe matches single- or double-quoted shell spans. Their
// contents are documentation/search arguments, not commands (issue #553):
// `grep -rn "go test" .` or `echo 'run go test later'` must not arm the
// test category just because the words sit next to the runner verb "go"
// after whitespace tokenization. Mirrors premature_success.go behavior,
// where quoted inner strings are not recognized as sub-commands.
var phantomQuotedRe = regexp.MustCompile(`"[^"]*"|'[^']*'`)

// phantomArmSegment dispatches one shell segment to its build-system family
// handler and reports whether a whitelist family claimed it (issue #350).
// Unclaimed segments fall through to generic command-position matching.
func phantomArmSegment(seg []string, cats map[string]bool) bool {
	switch seg[0] {
	case "make", "gmake", "mingw32-make":
		phantomArmMake(seg, cats)
	case "npm", "yarn", "pnpm", "bun":
		phantomArmNpmScript(seg, cats)
	case "mvn", "mvnw":
		phantomArmMvn(seg, cats)
	case "gradle", "gradlew", "./gradlew":
		phantomArmGradle(seg, cats)
	case "cmake":
		for _, t := range seg[1:] {
			if cmakeVerifyTargets[t] || strings.HasPrefix(t, "--target=") {
				cats[phantomCatBuild] = true
				cats[phantomCatCompile] = true
			}
		}
	default:
		return false
	}
	return true
}

// phantomArmCategories classifies cmdStr into verification categories and
// records the result in cats (issue #1159). See the classification block
// comment above for the enforced disciplines.
func phantomArmCategories(cmdStr string, cats map[string]bool) {
	tokens := strings.Fields(strings.ToLower(phantomQuotedRe.ReplaceAllString(cmdStr, " ")))
	for i := 0; i < len(tokens); {
		j := i
		for j < len(tokens) && !phantomIsSegmentOp(tokens[j]) {
			j++
		}
		seg := tokens[i:j]
		if len(seg) > 0 && !phantomArmSegment(seg, cats) {
			phantomArmGeneric(seg, cats)
		}
		i = j + 1 // skip the operator token itself
	}

	// issue #1150: passing tests compile and type-check every exercised
	// package, so a test run satisfies those two categories as well.
	if cats[phantomCatTest] {
		cats[phantomCatCompile] = true
		cats[phantomCatTypecheck] = true
	}
}

// phantomCommandTools are tools whose "command" parameter should be checked
// against verification patterns. Only command execution tools are included
// — file content parameters (write_file content, edit_file new_text) are
// excluded to avoid false positives (issue #593 P1).
var phantomCommandTools = map[string]bool{
	"run_command":         true,
	"start_command":       true,
	"wait_command":        true,
	"read_command_output": true,
	"task_output":         true,
	"ci_status":           true,
}

// phantomVerifyClaim captures a single unverified verification-outcome claim.
type phantomVerifyClaim struct {
	category  string // build, test, lint, compile, typecheck
	statement string // full sentence containing the claim
}

// phantomVerifyState tracks verification commands run and warning count.
type phantomVerifyState struct {
	warnings          int
	categoriesRun     map[string]bool // verification categories actually executed this run
	categoriesEverRun map[string]bool // session-level: categories EVER verified (#1478-A)
	recentClaimCats   map[string]bool // categories claimed in current assistant text
}

func newPhantomVerifyState() *phantomVerifyState {
	return &phantomVerifyState{
		categoriesRun:     make(map[string]bool),
		categoriesEverRun: make(map[string]bool),
		recentClaimCats:   make(map[string]bool),
	}
}

func (s *phantomVerifyState) reset() {
	s.warnings = 0
	for k := range s.categoriesRun {
		delete(s.categoriesRun, k)
	}
	for k := range s.recentClaimCats {
		delete(s.recentClaimCats, k)
	}
}

// recordToolCall tracks whether a tool call constitutes running a verification
// command of a specific category. isError is the tool result's IsError flag:
// a FAILED verification must not arm the category (issue #593 P3).
func (s *phantomVerifyState) recordToolCall(toolName string, toolInput string, isError bool) {
	// Failed verifications do not count as having run a successful verification
	// - they should not arm categories (issue #593 P3, aligned with #350 fix).
	if isError {
		return
	}

	// Only check the "command" parameter for command execution tools (issue #593 P1).
	// For file content tools (write_file, edit_file, etc.), we do NOT check the
	// full arguments JSON - that would false-positive on content like
	// "notes about go test conventions" in the file being written.
	var cmdStr string
	if phantomCommandTools[toolName] {
		// For command tools, try to extract just the command string from arguments.
		// If we can't parse it, fall back to checking the full input.
		if extracted := extractCommandArg(toolInput); extracted != "" {
			cmdStr = extracted
		} else {
			cmdStr = toolName + " " + toolInput
		}
	} else {
		// For non-command tools, only check toolName, not the full arguments.
		// This prevents file content from triggering false positives (issue #593 P1).
		// ci_status is special: we check the toolName since it indicates CI verification.
		cmdStr = toolName
	}

	// Structural classification (issue #1159): whitelisted build-system
	// targets and command-position keyword matching instead of substring
	// regexes over the raw command text.
	phantomArmCategories(cmdStr, s.categoriesRun)
	// #1478-A: session-level arming - reset() clears the per-run table each
	// turn, so a legitimate cross-turn back-reference ("yes, the tests passed")
	// after a REAL earlier run was flagged phantom. The ever-run table
	// survives resets and exempts such back-references while still catching
	// claims about never-verified categories.
	phantomArmCategories(cmdStr, s.categoriesEverRun)
}

// extractCommandArg extracts the "command" field value from a JSON arguments
// string by decoding the JSON properly (#1144). The previous naive quote scan
// truncated commands at the first escaped quote (\"), producing a wrong but
// non-empty string that bypassed the empty-string fallback in recordToolCall.
// Returns empty string on parse failure or missing field, preserving the
// caller's fallback semantics.
func extractCommandArg(argsJSON string) string {
	var args struct {
		Command string `json:"command"`
	}
	if err := json.Unmarshal([]byte(argsJSON), &args); err != nil {
		return ""
	}
	return args.Command
}

// detectPhantomClaims scans assistant text and returns verification-outcome claims
// whose category has NOT been verified by a matching command.
func (s *phantomVerifyState) detectPhantomClaims(text string) []phantomVerifyClaim {
	// Reset per-text claim tracking
	for k := range s.recentClaimCats {
		delete(s.recentClaimCats, k)
	}

	var claims []phantomVerifyClaim
	seen := make(map[string]bool) // dedupe by category+sentence

	for cat, patterns := range phantomClaimPatterns {
		for _, ptn := range patterns {
			matches := ptn.FindAllStringIndex(text, -1)
			for _, idx := range matches {
				statement := extractSentence(text, idx)
				if statement == "" {
					continue
				}
				key := cat + "|" + statement
				if seen[key] {
					continue
				}
				seen[key] = true
				s.recentClaimCats[cat] = true

				// Only flag if this category was NOT actually verified
				// Only flag if this category was NOT actually verified - neither
				// this run nor earlier in the session (#1478-A back-reference).
				if !s.categoriesRun[cat] && !s.categoriesEverRun[cat] {
					claims = append(claims, phantomVerifyClaim{
						category:  cat,
						statement: statement,
					})
				}
			}
		}
	}

	return claims
}

// maybeWarnPhantomVerify checks for verification-outcome claims lacking matching
// verification commands and returns a guidance message if a process-supervision
// gap is detected.
func (a *Agent) maybeWarnPhantomVerify(assistantText string) string {
	if a.phantomVerify == nil {
		return ""
	}
	s := a.phantomVerify

	if s.warnings >= phantomVerifyMaxWarnings {
		return ""
	}

	claims := s.detectPhantomClaims(assistantText)
	if len(claims) == 0 {
		return ""
	}

	s.warnings++

	var examples []string
	for i, c := range claims {
		if i >= phantomVerifyMaxExamples {
			break
		}
		ex := c.statement
		if len(ex) > 90 {
			ex = ex[:87] + "..."
		}
		examples = append(examples, fmt.Sprintf("  [%s] \"%s\"", c.category, ex))
	}

	hint := fmt.Sprintf(`[Process Supervision] You asserted verification outcomes without running the matching commands:

%s

Claims like "tests pass" or "the build compiles" are phantom verifications if no test/build command was executed (AgentPro, EMNLP 2025). Run the relevant verification command (go test, go build, lint) and reference its actual output before asserting the result. A claim of success must be grounded in an observed command outcome, not inferred.`, strings.Join(examples, "\n"))

	debug.Log("phantom-verify", "detected %d unverified claims", len(claims))
	return hint
}

// extractSentence extracts the sentence containing the given position in text.
// Returns the sentence text, or empty string if not found.
func extractSentence(text string, pos []int) string {
	if len(pos) < 2 || pos[0] < 0 || pos[1] > len(text) {
		return ""
	}

	start := pos[0]
	end := pos[1]

	// Extend backwards to find sentence start
	for start > 0 && text[start-1] != '.' && text[start-1] != '!' && text[start-1] != '?' {
		start--
	}

	// Extend forwards to find sentence end
	for end < len(text) && text[end] != '.' && text[end] != '!' && text[end] != '?' {
		end++
	}

	if end < len(text) {
		end++ // Include the terminating punctuation
	}

	return strings.TrimSpace(text[start:end])
}

// invalidateEdits clears the session-level everRun exemption (#1598-A):
// a successful edit invalidates every prior verification, so back-references
// vouching for pre-edit runs must not stay exempt after the sources changed.
// The per-run table was already clear (it resets every turn).
func (s *phantomVerifyState) invalidateEdits() {
	if s == nil {
		return
	}
	for k := range s.categoriesEverRun {
		delete(s.categoriesEverRun, k)
	}
}
