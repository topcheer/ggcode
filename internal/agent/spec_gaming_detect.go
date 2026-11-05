package agent

import (
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strings"

	"github.com/topcheer/ggcode/internal/debug"
)

type specGamingState struct {
	fired bool
}

func newSpecGamingState() *specGamingState {
	return &specGamingState{}
}

func (s *specGamingState) reset() {
	s.fired = false
}

var testFileSuffixes = []string{
	"_test.go",
	"_test.py",
	".test.ts",
	".test.tsx",
	".test.js",
	".test.jsx",
	".spec.ts",
	".spec.tsx",
	".spec.js",
	".spec.jsx",
	"_test.rb",
	"test.java",

	"_test.rs",
	"_test.c",
	"_test.cpp",
	".test.kt",
}

var ciConfigFiles = map[string]bool{
	".github/workflows": true,
	"pytest.ini":        true,
	"setup.cfg":         true,
	"tox.ini":           true,
	"conftest.py":       true,
	"jest.config.js":    true,
	"jest.config.ts":    true,
	"vitest.config.ts":  true,
	"vitest.config.js":  true,
	".babelrc":          true,
	"Makefile":          false,
}

var skipMarkers = []string{
	"@pytest.mark.skip",
	"@pytest.mark.xfail",
	"@skip",
	"@unittest.skip",
	"pytest.skip(",
	"it.skip(",
	"test.skip(",
	"describe.skip(",
	"xdescribe(",

	"// @ts-ignore",
	"@Disabled",
	"testing.Skip(",
	"t.Skip(",
}

var boundarySkipMarkerRes = []*regexp.Regexp{
	regexp.MustCompile(`(?:^|[^a-z0-9_])xit\(`),
	regexp.MustCompile(`(?:^|[^a-z0-9_])xtest\(`),
}

func containsBoundarySkipMarker(lower string) bool {
	for _, re := range boundarySkipMarkerRes {
		if re.MatchString(lower) {
			return true
		}
	}
	return false
}

func specGamingIsTestFile(path string) bool {
	lower := strings.ToLower(path)
	for _, suffix := range testFileSuffixes {
		if strings.HasSuffix(lower, suffix) {
			return true
		}
	}

	base := strings.ToLower(filepath.Base(path))
	for _, ext := range []string{".py", ".rb", ".ts", ".js"} {
		if strings.HasSuffix(base, ext) && (strings.HasPrefix(base, "test_") || strings.HasPrefix(base, "test-")) {
			return true
		}
	}

	if strings.Contains(lower, "/testdata/") || strings.HasPrefix(lower, "testdata/") ||
		strings.Contains(lower, "/golden/") || strings.Contains(lower, "__snapshots__") {
		return true
	}
	if strings.HasSuffix(lower, ".expected") || strings.HasSuffix(lower, ".golden") {
		return true
	}
	return false
}

func stripTestSuffix(path string) string {
	lower := strings.ToLower(path)
	for _, suffix := range testFileSuffixes {
		if strings.HasSuffix(lower, suffix) {

			if len(path) == len(suffix) {
				return path
			}

			base := path[:len(path)-len(suffix)]

			if suffix == "_test.go" {
				return base + ".go"
			}

			if suffix == "_test.py" {
				return base + ".py"
			}

			if suffix == ".test.ts" || suffix == ".spec.ts" {
				return base + ".ts"
			}
			if suffix == ".test.tsx" || suffix == ".spec.tsx" {
				return base + ".tsx"
			}
			if suffix == ".test.js" || suffix == ".spec.js" {
				return base + ".js"
			}
			if suffix == ".test.jsx" || suffix == ".spec.jsx" {
				return base + ".jsx"
			}
			if suffix == "_test.rb" {
				return base + ".rb"
			}
			if suffix == "_test.rs" {
				return base + ".rs"
			}
			if suffix == "_test.c" {
				return base + ".c"
			}
			if suffix == "_test.cpp" {
				return base + ".cpp"
			}
			if suffix == ".test.kt" {
				return base + ".kt"
			}

			if suffix == "test.java" {
				return base + ".java"
			}

			return base
		}
	}
	return path
}

func isConfigOrLockFile(path string) bool {
	base := path
	if idx := strings.LastIndex(path, "/"); idx >= 0 {
		base = path[idx+1:]
	}
	lower := strings.ToLower(base)

	if strings.EqualFold(base, "dockerfile") {
		return true
	}

	configSuffixes := []string{
		".toml", ".yaml", ".yml", ".json", ".ini", ".cfg",
		".lock", ".mod", ".sum", ".env",
	}
	for _, s := range configSuffixes {
		if strings.HasSuffix(lower, s) {
			return true
		}
	}
	return false
}

func isCIConfigPath(path string) bool {
	lower := strings.ToLower(path)
	base := strings.ToLower(filepath.Base(path))
	for pattern, enabled := range ciConfigFiles {
		if !enabled {
			continue
		}
		if strings.Contains(pattern, "/") {

			if strings.HasPrefix(lower, pattern+"/") || lower == pattern {
				return true
			}
		} else {

			if base == pattern {
				return true
			}
		}
	}
	return false
}

var makefileNoOpCommands = []string{"echo", "true", "exit 0", "exit", ":", "pass", "printf ''", "printf \"\"", "@:", "@true", "@echo"}

func isMakefileName(base string) bool {
	b := strings.ToLower(base)
	return b == "makefile" || b == "gnubsdmakefile" || b == "gnumakefile" || b == "bsdmakefile" || b == "makefile.mingw" || b == "makefile.msvc"
}

func hasMakefileTampering(path string) bool {
	data, err := os.ReadFile(path)
	if err != nil {
		return false
	}
	return hasMakefileTamperingContent(string(data))
}

var makefileNoOpPrefixes = []string{"echo", "true", "exit 0", "exit", ":", "pass", "printf", "@echo", "@true", "@exit", "@pass", "@printf", "@:", "-echo", "-true", "-exit", "-pass", "-printf"}

func isIndentedLine(ln string) bool {
	trimmed := strings.TrimLeft(ln, " \t")
	return trimmed != "" && trimmed != ln
}

func hasMakefileTamperingContent(content string) bool {
	lines := strings.Split(content, "\n")
	targets := map[string][]string{}
	deps := map[string][]string{}
	var order []string
	cur := ""
	for _, ln := range lines {
		switch {
		case isIndentedLine(ln) && cur != "":

			targets[cur] = append(targets[cur], strings.TrimSpace(ln))
		case strings.HasPrefix(ln, "#") || strings.TrimSpace(ln) == "":

		default:

			head := ln
			if idx := strings.Index(head, ":"); idx > 0 {
				name := strings.TrimSpace(head[:idx])
				if strings.HasSuffix(name, "=") || strings.Contains(head[idx:], ":=") {
					cur = ""
					continue
				}
				cur = name
				targets[cur] = nil
				order = append(order, cur)
				rest := head[idx+1:]

				if semi := strings.Index(rest, ";"); semi >= 0 {
					if inline := strings.TrimSpace(rest[semi+1:]); inline != "" {
						targets[cur] = append(targets[cur], inline)
					}
					rest = rest[:semi]
				}

				for _, f := range strings.Fields(rest) {
					deps[cur] = append(deps[cur], strings.TrimSuffix(f, ":"))
				}
			} else {
				cur = ""
			}
		}
	}
	isNoOp := func(cmd string) bool {
		body := strings.TrimSpace(cmd)
		body = strings.TrimPrefix(body, "@")
		body = strings.TrimPrefix(body, "-")
		body = strings.TrimSpace(body)

		if strings.ContainsAny(body, "&;|") {
			return false
		}
		for _, prefix := range makefileNoOpPrefixes {
			prefix = strings.TrimPrefix(strings.TrimPrefix(prefix, "@"), "-")
			if body == prefix || strings.HasPrefix(body, prefix+" ") {
				return true
			}
		}
		return false
	}
	_ = order
	for name, cmds := range targets {
		isTestTarget := name == "test" || name == "check" || strings.HasSuffix(name, "-test") || name == "verify"
		isBuildTarget := name == "build" || name == "all"
		if !isTestTarget && !isBuildTarget {
			continue
		}
		effCmds := cmds
		if len(effCmds) == 0 {

			effCmds = collectDelegatedCommands(targets, deps, name)
		}
		if len(effCmds) == 0 {

			if isTestTarget || makefilePhonyMentionsTest(lines) {
				return true
			}
			continue
		}
		allNoOp := true
		for _, c := range effCmds {
			if !isNoOp(c) {
				allNoOp = false
				break
			}
		}

		if allNoOp {
			return true
		}
	}

	hasBuild := false
	for name := range targets {
		if name == "build" || name == "all" {
			hasBuild = true
		}
	}
	hasTest := false
	for name := range targets {
		if name == "test" || name == "check" || strings.HasSuffix(name, "-test") || name == "verify" {
			hasTest = true
		}
	}
	if hasBuild && !hasTest {

		if strings.Contains(content, "# test:") || strings.Contains(content, "#test:") {
			return false
		}

		if !makefilePhonyMentionsTest(lines) {
			return false
		}
		return true
	}
	return false
}

func collectDelegatedCommands(targets map[string][]string, deps map[string][]string, root string) []string {
	var out []string
	visited := map[string]bool{root: true}
	queue := append([]string(nil), deps[root]...)
	for depth := 0; len(queue) > 0 && depth < 64; depth++ {
		next := queue[:1]
		queue = queue[1:]
		dep := next[0]
		if visited[dep] {
			continue
		}
		visited[dep] = true
		out = append(out, targets[dep]...)
		queue = append(queue, deps[dep]...)
	}
	return out
}

func makefilePhonyMentionsTest(lines []string) bool {
	for _, ln := range lines {
		if !strings.HasPrefix(strings.TrimSpace(ln), ".PHONY") {
			continue
		}
		for _, f := range strings.Fields(ln) {
			if f == "test" || f == "check" || f == "verify" || strings.HasSuffix(f, "-test") {
				return true
			}
		}
	}
	return false
}

var readOnlySearchVerbs = map[string]bool{
	"grep": true, "egrep": true, "fgrep": true, "rg": true, "ripgrep": true,
	"ag": true, "ack": true, "find": true, "cat": true, "head": true,
	"tail": true, "less": true, "more": true, "wc": true,
}

var downstreamMutatorTokens = []string{
	"-exec", "-execdir", "-ok", "-okdir", "-delete", "-fprint", "xargs",
}

func hasDownstreamMutator(cmd string) bool {
	for _, tok := range downstreamMutatorTokens {

		if strings.Contains(cmd, tok) {

			if tok != "xargs" {
				return true
			}
			for _, f := range strings.Fields(cmd) {
				if f == "xargs" || strings.HasSuffix(f, "/xargs") || strings.HasSuffix(f, "|xargs") {
					return true
				}
			}
		}
	}
	return false
}

func isReadOnlySearchCommand(cmd string) bool {
	fields := strings.Fields(cmd)
	idx := 0

	for idx < len(fields) && (strings.Contains(fields[idx], "=") || fields[idx] == "sudo" || fields[idx] == "command" || fields[idx] == "env") {
		idx++
	}
	if idx >= len(fields) {
		return false
	}
	if carriesSearchDownstreamMutation(cmd, fields[idx:]) {
		return false
	}
	verb := filepath.Base(fields[idx])
	if readOnlySearchVerbs[verb] {

		return !hasDownstreamMutator(cmd)
	}
	if verb == "git" && idx+1 < len(fields) {

		if fields[idx+1] == "grep" {
			return !hasDownstreamMutator(cmd)
		}

		if fields[idx+1] == "log" && idx+2 < len(fields) {
			f := fields[idx+2]
			if f == "-S" || f == "-G" ||
				strings.HasPrefix(f, "-S") || strings.HasPrefix(f, "-G") {
				return true
			}
		}
	}
	return false
}

func carriesSearchDownstreamMutation(cmd string, tail []string) bool {
	for _, t := range tail {
		switch t {
		case "-exec", "-execdir", "-ok", "-okdir", "xargs":
			return true
		}
	}
	// #5093: raw ContainsAny flagged the regex alternation '|' inside
	// quoted search patterns (rg '(@skip|xit)\(') as a pipeline, voiding
	// the read-only exemption. Only operators OUTSIDE quoted literal
	// segments indicate a downstream mutation.
	return carriesShellOperators(cmd)
}

// carriesShellOperators reports whether cmd contains shell control
// operators (pipe |, redirect >, separators ; and newline, logical &&)
// outside quoted literal segments.
//
// Quoting rules implemented:
//   - single-quoted segments are fully literal (no escapes, POSIX)
//   - double-quoted segments are literal except for backslash escapes
//     and $( ) command substitution, whose body re-enables operator
//     detection (the inner pipeline really runs)
//   - outside quotes, a backslash escapes the next character
//     (so grep foo\|bar is a literal | in the pattern, not a pipe)
func carriesShellOperators(cmd string) bool {
	for i := 0; i < len(cmd); i++ {
		c := cmd[i]
		switch c {
		case '\\':
			i++ // escaped next char outside quotes
		case '\'':
			// Single-quoted segment is fully literal (POSIX, no escapes).
			j := strings.IndexByte(cmd[i+1:], '\'')
			if j < 0 {
				i = len(cmd) // unterminated quote: the rest is a literal
				break
			}
			i += j + 1 // resume right after the closing quote
		case '"':
			found, end := doubleQuotedOperatorAt(cmd, i+1)
			if found {
				return true
			}
			// end == len(cmd) means the quote never terminates and the
			// rest is a literal; either way resume after the segment.
			i = end
		case '|', '>', ';', '\n':
			return true
		case '&':
			if i+1 < len(cmd) && cmd[i+1] == '&' {
				return true
			}
		}
	}
	return false
}

// doubleQuotedOperatorAt scans the double-quoted segment starting at i
// (just past the opening quote). Quoted text is literal except for
// backslash escapes and $( ) command substitution, whose body re-enables
// operator detection (the inner pipeline really runs). It returns
// found=true as soon as a real operator is seen, or the index of the
// closing quote (end == len(cmd) when the segment never terminates).
func doubleQuotedOperatorAt(cmd string, i int) (found bool, end int) {
	subDepth := 0 // $( ) nesting depth
	for ; i < len(cmd); i++ {
		c := cmd[i]
		switch {
		case c == '\\' && i+1 < len(cmd):
			i++ // escaped char is literal inside double quotes
		case c == '"' && subDepth == 0:
			return false, i
		case c == '$' && i+1 < len(cmd) && cmd[i+1] == '(':
			subDepth++
			i++ // consume '('
		case c == ')' && subDepth > 0:
			subDepth--
		case subDepth > 0:
			// Inside $( ): operators are executed for real.
			if c == '|' || c == '>' || c == ';' || c == '\n' {
				return true, i
			}
			if c == '&' && i+1 < len(cmd) && cmd[i+1] == '&' {
				return true, i
			}
		}
	}
	return false, len(cmd)
}

func isTestWritingTask(userPrompt string) bool {
	lower := strings.ToLower(userPrompt)

	sourceWords := []string{"fix", "bug", "修复", "implement", "实现",
		"feature", "功能", "build", "构建", "refactor", "重构", "修改", "修复"}
	hasSourceWord := false
	for _, kw := range sourceWords {
		if strings.Contains(lower, kw) {
			hasSourceWord = true
			break
		}
	}
	if hasSourceWord {

		return false
	}

	for _, kw := range []string{"测试", "回归", "用例"} {
		if strings.Contains(lower, kw) {
			return true
		}
	}
	words := strings.FieldsFunc(lower, func(r rune) bool {
		return (r < 'a' || r > 'z') && (r < '0' || r > '9')
	})
	for _, w := range words {
		switch w {
		case "test", "tests", "testing", "unittest", "unittests",
			"spec", "specs", "specification", "specifications",
			"coverage":
			return true
		}
	}

	for _, kw := range []string{"测试", "回归", "用例", "单测", "单元测试"} {
		if strings.Contains(lower, kw) {
			return true
		}
	}
	return false
}

func isSkipMarkerRemovalCommand(cmd string) bool {
	lower := strings.ToLower(cmd)

	fields := strings.Fields(cmd)
	if len(fields) < 2 {
		return false
	}

	verb := filepath.Base(fields[0])
	if verb != "sed" && verb != "awk" {
		return false
	}

	if verb == "sed" {
		return isSedSkipRemoval(cmd)
	}
	return isAwkSkipRemoval(lower)
}

func sedNormalizeDelimiters(cmd string) string {
	const delimClass = "|,#:;%!~@^_"
	var b strings.Builder
	i := 0
	for i < len(cmd) {
		c := cmd[i]

		if c == 's' && i+1 < len(cmd) && strings.IndexByte(delimClass, cmd[i+1]) >= 0 {
			prevOK := i == 0 || cmd[i-1] == ' ' || cmd[i-1] == '\t' || cmd[i-1] == '\'' || cmd[i-1] == '"' || cmd[i-1] == ';'
			if prevOK {
				delim := cmd[i+1]

				p1 := strings.IndexByte(cmd[i+2:], delim)
				if p1 >= 0 {
					p2 := strings.IndexByte(cmd[i+2+p1+1:], delim)
					if p2 >= 0 {
						f1 := cmd[i+2 : i+2+p1]
						f2 := cmd[i+2+p1+1 : i+2+p1+1+p2]
						rest := cmd[i+2+p1+1+p2+1:]

						if !strings.Contains(f1, "/") && !strings.Contains(f2, "/") {
							b.WriteString("s/" + f1 + "/" + f2 + "/" + rest)
							i = len(cmd)
							continue
						}
					}
				}
			}
		}
		b.WriteByte(c)
		i++
	}
	return b.String()
}
func isSedSkipRemoval(cmd string) bool {

	normalized := sedNormalizeDelimiters(cmd)
	parts := strings.Split(normalized, "'s/")
	if len(parts) < 2 {
		parts = strings.Split(normalized, "\"s/")
	}
	if len(parts) < 2 {
		parts = strings.Split(normalized, " s/")
	}
	if len(parts) < 2 {
		return false
	}

	sawLegitRemoval := false
	for _, rawExpr := range strings.Split(parts[1], ";") {

		expr := strings.TrimSpace(rawExpr)
		if strings.HasPrefix(expr, "s/") {

			expr = expr[2:]
		}
		replacementParts := strings.Split(expr, "/")
		if len(replacementParts) < 2 {
			continue
		}
		pattern := strings.ToLower(replacementParts[0])
		replacement := strings.ToLower(replacementParts[1])
		if containsAnySkipMarker(replacement) {
			return false
		}
		if containsAnySkipMarker(pattern) {
			sawLegitRemoval = true
		}
	}
	return sawLegitRemoval
}

func isAwkSkipRemoval(lower string) bool {
	if !strings.Contains(lower, "gsub(") {
		return false
	}

	normalized := strings.ReplaceAll(lower, "\\", "")

	start := 0
	sawAny := false
	for {
		idx := strings.Index(normalized[start:], "gsub(")
		if idx < 0 {
			break
		}
		idx += start
		args := normalized[idx+len("gsub("):]
		pattern, replacement := args, ""
		if q := strings.Index(args, "/"); q >= 0 {
			if q2 := strings.Index(args[q+1:], "/"); q2 >= 0 {
				end := q + 1 + q2
				pattern = args[:end+1]
				rest := strings.TrimLeft(args[end+1:], " ")
				if strings.HasPrefix(rest, ",") {
					replacement = rest[1:]
				}
			}
		} else if c := strings.Index(args, ","); c >= 0 {
			pattern = args[:c]
			replacement = args[c+1:]
		}
		if !containsAnySkipMarker(pattern) || containsAnySkipMarker(replacement) {

			return false
		}
		sawAny = true
		start = idx + len("gsub(")
	}
	return sawAny
}

func containsAnySkipMarker(s string) bool {

	lower := strings.ToLower(s)
	if containsBoundarySkipMarker(lower) {
		return true
	}
	for _, marker := range skipMarkers {
		if strings.Contains(lower, strings.ToLower(marker)) {
			return true
		}
	}
	return false
}

func hasSkipMarkersInCommands(commands []string) bool {
	for _, cmd := range commands {

		if isSkipMarkerRemovalCommand(cmd) || isReadOnlySearchCommand(cmd) {
			continue
		}
		// #5101: for REPORT verbs (gh/hub/glab/curl...) markers merely QUOTED
		// into message bodies (gh issue create --body "... @pytest.mark.skip
		// ...") are references, not additions - blank quoted segments before
		// the substring checks. All other verbs keep the legacy raw scan:
		// write-shaped tools (echo/printf/sed -i/tee) can introduce real
		// markers with quoted text, and only shell-level `>` is not the only
		// write channel (sed -i writes via args).
		effective := cmd
		if leadingReportVerb(cmd) {
			effective = stripQuotedReferences(cmd)
		}
		lower := strings.ToLower(effective)
		if containsBoundarySkipMarker(lower) {
			return true
		}
		for _, marker := range skipMarkers {

			markerLower := strings.ToLower(marker)
			if strings.Contains(lower, markerLower) {
				return true
			}

			escapedMarker := strings.ReplaceAll(marker, "(", "\\(")
			escapedMarker = strings.ReplaceAll(escapedMarker, ".", "\\.")
			if strings.Contains(effective, escapedMarker) {
				return true
			}
		}
	}
	return false
}

// reportQuotedVerbs: commands whose quoted arguments are messages/
// payloads sent to external services (issues, PRs, HTTP bodies) - markers
// quoted there are references to skip markers, not additions to code.
var reportQuotedVerbs = map[string]bool{
	"curl": true, "wget": true,
}

// apiPassthroughReportCLIs: wrapper CLIs whose report-shaped subcommands
// (issue/pr/release/comment) deliver quoted text as external-service
// payloads, but which ALSO expose a raw API passthrough subcommand (gh api
// / glab api / hub api) that can commit file content straight into the
// repo via the contents/git-data endpoints - so only non-api subcommands
// get the quoted-reference exemption (#5101 review residual).
var apiPassthroughReportCLIs = map[string]bool{
	"gh": true, "hub": true, "glab": true, "tea": true, "gitea": true,
}

func leadingReportVerb(cmd string) bool {
	fields := strings.Fields(cmd)
	i := 0
	for i < len(fields) && (strings.Contains(fields[i], "=") || fields[i] == "sudo" || fields[i] == "command" || fields[i] == "env") {
		i++
	}
	if i >= len(fields) {
		return false
	}
	verb := strings.ToLower(filepath.Base(fields[i]))
	if reportQuotedVerbs[verb] {
		return true
	}
	if apiPassthroughReportCLIs[verb] {
		// the passthrough can appear after global flags (gh --repo r api);
		// deny the exemption when any bare token after the CLI is the
		// passthrough. Denial is conservative: a stray unquoted "api" word
		// in another argument just restores the legacy raw scan.
		for _, f := range fields[i+1:] {
			if sub := strings.ToLower(f); sub == "api" || sub == "gql" {
				return false
			}
		}
		return true
	}
	return false
}

// stripQuotedReferences (#5101) replaces quoted literal segments with
// same-length space filler so position-sensitive checks keep alignment,
// except: (a) segments that are the target of a shell redirection - the
// shell writes that text into a file, a real marker injection; (b) $( )
// command-substitution bodies inside double quotes - executed content,
// kept conservatively so the detector cannot be blinded by nesting.
// Unterminated quotes treat the rest as literal (consistent with the
// #5094 scan: malformed input degrades to no filtering).
func stripQuotedReferences(cmd string) string {
	var b strings.Builder
	b.Grow(len(cmd))

	isRedirectTarget := func(end int) bool {
		// #5101 heuristic: a '>' redirection AFTER the segment within the
		// same command (echo/printf '...' > file.go) makes the quoted text
		// write-shaped - the shell writes it into the target file. Scan
		// stops at command boundaries (; | & newline). Conservative
		// residual: "gh ... --body '...' > /dev/null" also fires.
		for k := end; k < len(cmd); k++ {
			switch cmd[k] {
			case ' ', '\t':
				continue
			case '>':
				return true
			default:
				return false
			}
		}
		return false
	}

	i := 0
	for i < len(cmd) {
		c := cmd[i]
		if c == '\\' && i+1 < len(cmd) {
			b.WriteString(cmd[i : i+2])
			i += 2
			continue
		}
		if c != '\'' && c != '"' {
			b.WriteByte(c)
			i++
			continue
		}
		start := i
		end := quotedSegmentEnd(cmd, i)
		if isRedirectTarget(end + 1) {
			b.WriteString(cmd[start:min(end+1, len(cmd))])
		} else {
			for k := start; k <= end && k < len(cmd); k++ {
				b.WriteByte(' ')
			}
		}
		i = end + 1
	}
	return b.String()
}

// quotedSegmentEnd returns the index of the closing quote for the segment
// opening at start; len(cmd) when unterminated (the rest is literal).
// Inside double quotes, backslash escapes are honored and $( ... )
// command-substitution bodies (which may themselves contain quotes) are
// skipped as a unit so their inner quotes cannot end the segment early.
func quotedSegmentEnd(cmd string, start int) int {
	quote := cmd[start]
	for j := start + 1; j < len(cmd); j++ {
		if quote == '\'' {
			if cmd[j] == '\'' {
				return j
			}
			continue
		}
		if cmd[j] == '\\' && j+1 < len(cmd) {
			j++
			continue
		}
		if cmd[j] == '"' {
			return j
		}
		if cmd[j] == '$' && j+1 < len(cmd) && cmd[j+1] == '(' {
			j = skipDollarParen(cmd, j+1)
		}
	}
	return len(cmd)
}

// skipDollarParen returns the index of the ')' matching the '(' at open,
// or len(cmd) when unterminated.
func skipDollarParen(cmd string, open int) int {
	depth := 1
	k := open + 1
	for k < len(cmd) && depth > 0 {
		if cmd[k] == '(' {
			depth++
		} else if cmd[k] == ')' {
			depth--
		}
		k++
	}
	if depth > 0 {
		return len(cmd)
	}
	return k - 1 // index of the closing ')'
}

func hasSkipMarkersInEdits(contents []string) bool {
	for _, text := range contents {
		lower := strings.ToLower(text)
		if containsBoundarySkipMarker(lower) {
			return true
		}
		for _, marker := range skipMarkers {
			markerLower := strings.ToLower(marker)
			if strings.Contains(lower, markerLower) {
				return true
			}
			escapedMarker := strings.ReplaceAll(marker, "(", "\\(")
			escapedMarker = strings.ReplaceAll(escapedMarker, ".", "\\.")
			if strings.Contains(text, escapedMarker) {
				return true
			}
		}
	}
	return false
}

func isCIRelatedTask(userPrompt string) bool {
	lower := strings.ToLower(userPrompt)

	words := strings.FieldsFunc(lower, func(r rune) bool {
		return (r < 'a' || r > 'z') && (r < '0' || r > '9')
	})
	for _, w := range words {
		if w == "ci" {
			return true
		}
	}
	ciKeywords := []string{
		"pipeline", "workflow", "github action", "gitlab ci",
		"jenkins", "circleci", "build config", "makefile", "test config",
		"pytest.ini", "conftest", "jest.config",
	}
	for _, kw := range ciKeywords {
		if strings.Contains(lower, kw) {
			return true
		}
	}
	return false
}

func (a *Agent) checkSpecGaming(stats *RunStats, userPrompt string) string {
	if a.specGaming.fired {
		return ""
	}

	var warnings []string

	testFiles := []string{}
	sourceFiles := []string{}
	for _, f := range stats.FilesEdited {
		if specGamingIsTestFile(f) {
			testFiles = append(testFiles, f)
		} else if !isConfigOrLockFile(f) {
			sourceFiles = append(sourceFiles, f)
		}
	}

	if len(testFiles) > 0 && len(sourceFiles) == 0 && !isTestWritingTask(userPrompt) {

		sourceExists := false
		for _, tf := range testFiles {
			if src := stripTestSuffix(tf); src != tf {
				if _, err := os.Stat(src); err == nil {
					sourceExists = true
					break
				}
			}
		}
		if !sourceExists {
			warnings = append(warnings, fmt.Sprintf(
				"Only test files were modified (%s) but no source files. "+
					"Ensure you are fixing the actual code, not just modifying tests to pass.",
				strings.Join(testFiles, ", ")))
		}
	}

	if hasSkipMarkersInCommands(stats.CommandsRun) || hasSkipMarkersInEdits(stats.EditContents) {
		warnings = append(warnings,
			"Test skip/ignore markers detected in commands or edits. "+
				"Skipping failing tests is not a fix -- address the root cause in source code.")
	}

	if !isCIRelatedTask(userPrompt) {
		for _, f := range stats.FilesEdited {
			if isCIConfigPath(f) {
				warnings = append(warnings, fmt.Sprintf(
					"Verification configuration file '%s' was modified during a non-CI task. "+
						"Ensure this change is necessary for the task, not to suppress test failures.", f))
				break
			}

			if isMakefileName(filepath.Base(f)) && hasMakefileTampering(f) {
				warnings = append(warnings, fmt.Sprintf(
					"Makefile verification target '%s' appears neutered (no-op commands or deleted test target). "+
						"Ensure the verification still actually runs the tests.", f))
				break
			}
		}
	}

	if len(warnings) == 0 {
		return ""
	}

	a.specGaming.fired = true
	debug.Log("specgaming", "specification gaming detected: %d warning(s)", len(warnings))

	msg := "[spec-gaming] Potential specification gaming detected.\n" +
		"Research shows 30%%+ of agent runs involve reward hacking (METR 2025) -- " +
		"agents gaming verification instead of solving the problem.\n\n"
	for i, w := range warnings {
		msg += fmt.Sprintf("%d. %s\n", i+1, w)
	}
	msg += "\nVerify that your changes fix the ROOT CAUSE, not just the verification signal."

	return msg
}
