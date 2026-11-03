package agent

import (
	"regexp"
	"strings"

	"github.com/topcheer/ggcode/internal/debug"
)

type scopeNarrowState struct {
	history               []scopeEntry
	fired                 bool
	lastCoverageWarnedCmd string
}

type scopeEntry struct {
	toolName   string
	rawCommand string
	scope      string
	category   string
	passed     bool
}

func newScopeNarrowState() *scopeNarrowState {
	return &scopeNarrowState{}
}

func (s *scopeNarrowState) reset() {
	s.history = s.history[:0]
	s.fired = false
}

var (
	goTestScopeRe = regexp.MustCompile(`go\s+test\s+(?:-tags\s+\S+\s+)?(.*)`)
	goTestRunRe   = regexp.MustCompile(`-run(?:\s+|=)\s*(\S+)`)
	goTestPkgRe   = regexp.MustCompile(`(?:^|\s)(\./\S*|internal/\S*|cmd/\S*|\.\.\.|[a-zA-Z0-9][a-zA-Z0-9.-]*\.[a-zA-Z]{2,}(?:/\S*)?)`)

	pytestScopeRe = regexp.MustCompile(`pytest\s+(.*)`)
	pytestKRe     = regexp.MustCompile(`-k(?:\s+|=)\s*(\S+)`)

	npmTestGrepRe = regexp.MustCompile(`--grep(?:\s+|=)\s*(\S+)`)
)

func isGoSubcommand(cmd, sub string) bool {
	for _, seg := range splitShellSegments(cmd) {
		if segmentInvokesGo(strings.Fields(seg), sub) {
			return true
		}
	}
	return false
}

func segmentInvokesGo(fields []string, sub string) bool {
	i := 0
	for i < len(fields) {
		switch fields[i] {
		case "sudo", "time", "nice":
			i++
		case "env":
			i++

			for i < len(fields) && strings.Contains(fields[i], "=") {
				i++
			}
		case "timeout":
			i += 2
		default:
			return fields[i] == "go" && i+1 < len(fields) && fields[i+1] == sub
		}
	}
	return false
}

func isHeadCommand(cmd string, head []string) bool {
	for _, seg := range splitShellSegments(cmd) {
		if segmentMatchesHead(strings.Fields(seg), head) {
			return true
		}
	}
	return false
}

func segmentMatchesHead(fields, head []string) bool {
	i := 0
	for i < len(fields) {
		switch fields[i] {
		case "sudo", "time", "nice":
			i++
		case "env":
			i++
			for i < len(fields) && strings.Contains(fields[i], "=") {
				i++
			}
		default:
			if len(fields)-i < len(head) {
				return false
			}
			for j, w := range head {
				if fields[i+j] != w {
					return false
				}
			}
			return true
		}
	}
	return false
}

func isNpmTestCommand(cmd string) bool {
	fields := strings.Fields(cmd)
	if len(fields) < 2 {
		return false
	}
	switch fields[0] {
	case "npm", "yarn", "pnpm", "bun":
		if fields[1] == "test" {
			return true
		}

		return len(fields) > 2 && fields[1] == "run" && fields[2] == "test"
	}
	return false
}

func classifyVerificationCommand(cmd string) (category, scope string) {
	cmd = strings.TrimSpace(cmd)
	if cmd == "" {
		return "", ""
	}

	for _, prefix := range []string{"sudo ", "time ", "nice "} {
		cmd = strings.TrimPrefix(cmd, prefix)
	}

	if isGoSubcommand(cmd, "test") || isGoSubcommand(cmd, "vet") {
		category = "go-test"
		scope = extractGoTestScope(cmd)
		return
	}

	if isGoSubcommand(cmd, "build") {
		category = "go-build"
		scope = extractGoTestScope(cmd)
		return
	}

	if isHeadCommand(cmd, []string{"pytest"}) || isHeadCommand(cmd, []string{"python", "-m", "pytest"}) {
		category = "pytest"
		scope = extractPytestScope(cmd)
		return
	}

	if isNpmTestCommand(cmd) {
		category = "npm-test"
		scope = extractNpmTestScope(cmd)
		return
	}

	if isHeadCommand(cmd, []string{"cargo"}) {
		if fields := strings.Fields(cmd); len(fields) > 1 && fields[1] == "test" {
			category = "cargo-test"
			scope = extractAfterKeyword(cmd, "cargo test")
			return
		}
	}

	if isHeadCommand(cmd, []string{"make"}) {
		if fields := strings.Fields(cmd); len(fields) > 1 {
			for _, verb := range []string{"test", "check", "verify"} {
				if fields[1] == verb || strings.HasPrefix(fields[1], verb+"-") {
					category = "make-test"
					return
				}
			}
		}
	}

	return "", ""
}

func extractGoTestScope(cmd string) string {
	var parts []string

	if m := goTestRunRe.FindStringSubmatch(cmd); len(m) > 1 {
		parts = append(parts, "run:"+m[1])
	}

	if strings.Contains(cmd, "./...") {
		parts = append(parts, "scope:broad")
	} else {

		pkgs := goTestPkgRe.FindAllStringSubmatch(cmd, -1)
		for _, m := range pkgs {
			if len(m) > 1 && m[1] != "./..." {

				if strings.HasSuffix(m[1], ".go") {
					parts = append(parts, "file:"+m[1])
				} else {
					parts = append(parts, "pkg:"+m[1])
				}
			}
		}
		if len(parts) == 0 {
			parts = append(parts, "scope:broad")
		}
	}

	return strings.Join(parts, "|")
}

func extractPytestScope(cmd string) string {
	var parts []string

	if m := pytestKRe.FindStringSubmatch(cmd); len(m) > 1 {
		parts = append(parts, "k:"+m[1])
	}

	rest := ""
	if m := pytestScopeRe.FindStringSubmatch(cmd); len(m) > 1 {
		rest = m[1]
	}
	for _, tok := range strings.Fields(rest) {
		if !strings.HasPrefix(tok, "-") && (strings.Contains(tok, "/") || strings.HasSuffix(tok, ".py")) {
			parts = append(parts, "file:"+tok)
		}
	}

	if len(parts) == 0 {
		parts = append(parts, "scope:broad")
	}

	return strings.Join(parts, "|")
}

func extractNpmTestScope(cmd string) string {
	var parts []string

	if m := npmTestGrepRe.FindStringSubmatch(cmd); len(m) > 1 {
		parts = append(parts, "grep:"+m[1])
	}

	for _, tok := range strings.Fields(cmd) {
		if strings.HasSuffix(tok, ".test.js") || strings.HasSuffix(tok, ".spec.js") ||
			strings.HasSuffix(tok, ".test.ts") || strings.HasSuffix(tok, ".spec.ts") {
			parts = append(parts, "file:"+tok)
		}
	}

	if len(parts) == 0 {
		parts = append(parts, "scope:broad")
	}

	return strings.Join(parts, "|")
}

func extractAfterKeyword(cmd, keyword string) string {
	idx := strings.Index(cmd, keyword)
	if idx < 0 {
		return ""
	}
	return strings.TrimSpace(cmd[idx+len(keyword):])
}

func (s *scopeNarrowState) recordVerificationCommand(toolName, cmd string, output string, isError bool) string {
	if s.fired {
		return ""
	}

	if s.lastCoverageWarnedCmd != "" && s.lastCoverageWarnedCmd == cmd {
		s.lastCoverageWarnedCmd = ""
		return ""
	}

	category, scope := classifyVerificationCommand(cmd)
	if category == "" {
		return ""
	}

	entry := scopeEntry{
		toolName:   toolName,
		rawCommand: cmd,
		scope:      scope,
		category:   category,

		passed: !isError,
	}

	s.history = append(s.history, entry)

	if len(s.history) < 3 {
		return ""
	}

	return s.checkNarrowingPattern()
}

func looksLikeTestFailure(output string) bool {
	output = strings.ToLower(output)
	for _, marker := range []string{
		"fail", "failed", "error", "panic:", "--- fail", "assertion", "not ok",
	} {
		if strings.Contains(output, marker) {
			return true
		}
	}
	return false
}

func (s *scopeNarrowState) checkNarrowingPattern() string {
	n := len(s.history)
	if n < 3 {
		return ""
	}

	last := s.history[n-1]
	if last.category == "make-test" {

		return ""
	}

	var sameCat []scopeEntry
	for i := n - 1; i >= 0 && len(sameCat) < 3; i-- {
		if s.history[i].category == last.category {
			sameCat = append(sameCat, s.history[i])
		}
	}

	if len(sameCat) < 3 {
		return ""
	}

	oldest := sameCat[2]
	middle := sameCat[1]
	newest := sameCat[0]

	if !isNarrower(middle.scope, oldest.scope) || !isNarrower(newest.scope, middle.scope) {
		return ""
	}
	if oldest.passed || !middle.passed || !newest.passed {

		if oldest.passed {
			return ""
		}
		if !middle.passed && !newest.passed {
			return ""
		}
	}

	s.fired = true
	originalCmd := oldest.rawCommand
	debug.Log("agent", "verification scope narrowing detected: '%s' → '%s' → '%s'",
		truncateCmdShort(oldest.rawCommand), truncateCmdShort(middle.rawCommand), truncateCmdShort(newest.rawCommand))

	return "[Verification Scope Narrowing Detected] Your test/build commands have progressively narrowed scope:\n" +
		"  1. `" + truncateCmdShort(oldest.rawCommand) + "` -> failed\n" +
		"  2. `" + truncateCmdShort(middle.rawCommand) + "` -> " + passFailStr(middle.passed) + "\n" +

		"  3. `" + truncateCmdShort(newest.rawCommand) + "` -> " + passFailStr(newest.passed) + "\n\n" +
		"The narrowing may be masking failures outside the narrowed scope. " +
		"Re-run the ORIGINAL broader command (`" + truncateCmdShort(originalCmd) + "`) to confirm " +
		"all tests/builds pass, not just the narrowed subset. " +
		"If broader tests legitimately fail, fix them rather than narrowing scope further."
}

func isNarrower(b, a string) bool {
	if a == "" || b == "" {
		return false
	}

	if strings.Contains(a, "scope:broad") && !strings.Contains(b, "scope:broad") {
		return true
	}
	aPkgs := extractPkgList(a)
	bPkgs := extractPkgList(b)
	bFiles := extractFileList(b)
	if pkgSetNarrower(bPkgs, aPkgs) {
		return true
	}

	if len(aPkgs) > 0 && len(bFiles) > 0 && scopeUnderAnyPrefix(bFiles, aPkgs) {
		return true
	}

	for _, marker := range []string{"run:", "k:", "grep:"} {
		if strings.Contains(b, marker) && !strings.Contains(a, marker) {
			return true
		}
	}

	if strings.Contains(b, "file:") && !strings.Contains(a, "file:") {

		if len(aPkgs) == 0 {
			return true
		}
	}
	return false
}

func pkgSetNarrower(bPkgs, aPkgs []string) bool {
	if len(aPkgs) == 0 || len(bPkgs) == 0 {
		return false
	}
	aSet := make(map[string]bool, len(aPkgs))
	for _, p := range aPkgs {
		aSet[p] = true
	}
	allSubset := true
	for _, p := range bPkgs {
		if !aSet[p] {
			allSubset = false
			break
		}
	}
	if allSubset && len(bPkgs) < len(aPkgs) {
		return true
	}

	return scopeUnderAnyPrefix(bPkgs, aPkgs)
}

func extractPkgList(scope string) []string {
	var pkgs []string
	for _, part := range strings.Split(scope, "|") {
		if strings.HasPrefix(part, "pkg:") {
			pkgs = append(pkgs, strings.TrimPrefix(part, "pkg:"))
		}
	}
	return pkgs
}

func extractFileList(scope string) []string {
	var files []string
	for _, part := range strings.Split(scope, "|") {
		if strings.HasPrefix(part, "file:") {
			files = append(files, strings.TrimPrefix(part, "file:"))
		}
	}
	return files
}

func scopeUnderAnyPrefix(sub, dirs []string) bool {
	if len(sub) == 0 || len(dirs) == 0 {
		return false
	}
	for _, s := range sub {
		under := false
		for _, d := range dirs {
			if pathSegmentPrefix(s, d) {
				under = true
				break
			}
		}
		if !under {
			return false
		}
	}
	return true
}

func pathSegmentPrefix(sub, dir string) bool {
	s := strings.TrimSuffix(sub, "/")
	d := strings.TrimSuffix(dir, "/")
	if s == d {
		return true
	}
	return strings.HasPrefix(s, d+"/")
}

func truncateCmdShort(cmd string) string {
	if len(cmd) > 80 {
		return cmd[:77] + "..."
	}
	return cmd
}

func passFailStr(passed bool) string {
	if passed {
		return "passed"
	}
	return "failed"
}
