package agent

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

const (
	phantomCatBuild     = "build"
	phantomCatTest      = "test"
	phantomCatLint      = "lint"
	phantomCatCompile   = "compile"
	phantomCatTypecheck = "typecheck"
	phantomCatCI        = "ci"
)

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
	"ci_status":           {phantomCatCI},
	"merge_preflight":     {phantomCatCI},
}

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
	"./mvnw":              true,
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

func phantomIsSegmentOp(tok string) bool {
	return tok == "|" || tok == "||" || tok == "&&" || tok == ";"
}

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

func phantomArmMvn(seg []string, cats map[string]bool) {
	for _, t := range seg[1:] {
		for _, c := range phantomArmsTarget(mvnVerifyPhases, t) {
			cats[c] = true
		}
	}
}

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

func phantomArmGeneric(seg []string, cats map[string]bool) {
	prevRunner := false
	prevDashM := false
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
		case "mvn", "mvnw", "./mvnw":
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

var phantomQuotedRe = regexp.MustCompile(`"[^"]*"|'[^']*'`)

func phantomArmSegment(seg []string, cats map[string]bool) bool {
	switch seg[0] {
	case "make", "gmake", "mingw32-make":
		phantomArmMake(seg, cats)
	case "npm", "yarn", "pnpm", "bun":
		phantomArmNpmScript(seg, cats)
	case "mvn", "mvnw", "./mvnw":
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
		i = j + 1
	}

	if cats[phantomCatTest] {
		cats[phantomCatCompile] = true
		cats[phantomCatTypecheck] = true
	}
}

var phantomCommandTools = map[string]bool{
	"run_command":         true,
	"start_command":       true,
	"wait_command":        true,
	"read_command_output": true,
	"task_output":         true,
	"ci_status":           true,
	"merge_preflight":     true,
}

type phantomVerifyClaim struct {
	category  string
	statement string
}

type phantomVerifyState struct {
	warnings          int
	categoriesRun     map[string]bool
	categoriesEverRun map[string]bool
	recentClaimCats   map[string]bool
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

func (s *phantomVerifyState) recordToolCall(toolName string, toolInput string, isError bool) {

	if isError {
		return
	}

	var cmdStr string
	if phantomCommandTools[toolName] {

		if extracted := extractCommandArg(toolInput); extracted != "" {
			cmdStr = extracted
		} else {
			cmdStr = toolName + " " + toolInput
		}
	} else {

		cmdStr = toolName
	}

	phantomArmCategories(cmdStr, s.categoriesRun)

	phantomArmCategories(cmdStr, s.categoriesEverRun)
}

func extractCommandArg(argsJSON string) string {
	var args struct {
		Command string `json:"command"`
	}
	if err := json.Unmarshal([]byte(argsJSON), &args); err != nil {
		return ""
	}
	return args.Command
}

func (s *phantomVerifyState) detectPhantomClaims(text string) []phantomVerifyClaim {

	for k := range s.recentClaimCats {
		delete(s.recentClaimCats, k)
	}

	var claims []phantomVerifyClaim
	seen := make(map[string]bool)

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

func extractSentence(text string, pos []int) string {
	if len(pos) < 2 || pos[0] < 0 || pos[1] > len(text) {
		return ""
	}

	start := pos[0]
	end := pos[1]

	for start > 0 && text[start-1] != '.' && text[start-1] != '!' && text[start-1] != '?' {
		start--
	}

	for end < len(text) && text[end] != '.' && text[end] != '!' && text[end] != '?' {
		end++
	}

	if end < len(text) {
		end++
	}

	return strings.TrimSpace(text[start:end])
}

func (s *phantomVerifyState) invalidateEdits() {
	if s == nil {
		return
	}
	// #5371: a source edit invalidates verification in BOTH layers. The
	// claim gate suppresses a warning whenever either category is armed, so
	// clearing only categoriesEverRun leaves the current-turn
	// categoriesRun entries suppressing phantom-claim warnings for
	// verification made stale by the edit. reset() deliberately preserves
	// categoriesEverRun across turns; invalidateEdits deliberately does not.
	for k := range s.categoriesRun {
		delete(s.categoriesRun, k)
	}
	for k := range s.categoriesEverRun {
		delete(s.categoriesEverRun, k)
	}
}
