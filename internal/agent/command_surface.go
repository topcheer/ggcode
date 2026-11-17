package agent

import (
	"encoding/json"
	"strings"
)

// #5744: when a run_command times out, the [non-atomic] banner used to
// unconditionally declare side effects UNKNOWN and demand a state
// reconciliation round-trip before any re-issue. For provably read-only
// commands (gh api GET reads, grep, ls, git log ...) that ritual is pure
// overhead: a re-run can never double-apply anything.
//
// shellLineReadOnly performs conservative static analysis of one shell
// command line: the line is read-only only when every pipeline segment is
// an allowlisted read-only program with safe arguments and no file-writing
// redirection. Anything the analyzer cannot prove (command substitution,
// unknown binaries, ambiguous flags, unmatched quotes or heredocs) reports
// false, so the pre-existing UNKNOWN banner keeps firing - the classifier
// may only shrink the warning, never silence a genuine write surface.

const shellSurfaceMaxDepth = 3

// readOnlyShellPrograms lists programs with no persistent write surface for
// any arguments. Keep this a data table (per the #5744 review note, same
// pattern as the reread-breaker tool tables) so the classification stays
// auditable and testable in isolation.
var readOnlyShellPrograms = map[string]bool{
	// text filters / cat family
	"cat": true, "head": true, "tail": true, "less": true, "more": true,
	"wc": true, "nl": true, "tac": true, "rev": true, "sort": true,
	"uniq": true, "cut": true, "paste": true, "column": true, "tr": true,
	"grep": true, "egrep": true, "fgrep": true, "zgrep": true, "zcat": true,
	"zless": true, "jq": true, "diff": true, "cmp": true, "xxd": true,
	"hexdump": true, "od": true, "cksum": true, "md5": true,
	"md5sum": true, "sha1sum": true, "sha256sum": true, "sha512sum": true,
	"shasum": true,
	// filesystem / system inspection
	"ls": true, "stat": true, "file": true, "du": true, "df": true,
	"tree": true, "pwd": true, "whoami": true, "id": true, "groups": true,
	"uname": true, "hostname": true, "uptime": true, "ps": true, "lsof": true,
	"printenv": true, "which": true, "type": true, "basename": true,
	"dirname": true, "realpath": true, "readlink": true, "sw_vers": true,
	// stream producers and shell builtins with no persistent effect on a
	// fresh per-invocation shell
	"echo": true, "printf": true, "true": true, "false": true,
	"test": true, "[": true, "seq": true, "sleep": true, "cd": true,
	"export": true, "unset": true, "set": true, "shift": true, "wait": true,
	":": true,
}

// guardedReadOnlyShellPrograms lists read-only programs whose ARGUMENTS can
// flip them into writers (sed -i, find -exec, gh api --method POST ...).
// Each predicate receives the words after the program plus the raw segment
// text and returns true when the invocation stays read-only.
var guardedReadOnlyShellPrograms = map[string]func(rest []string, raw string) bool{
	"awk":    awkArgsReadOnly,
	"curl":   curlArgsReadOnly,
	"date":   dateArgsReadOnly,
	"find":   findArgsReadOnly,
	"gawk":   awkArgsReadOnly,
	"gh":     ghArgsReadOnly,
	"git":    gitArgsReadOnly,
	"mawk":   awkArgsReadOnly,
	"rg":     rgArgsReadOnly,
	"sed":    sedArgsReadOnly,
	"sysctl": sysctlArgsReadOnly,
}

// shellReadOnlyGitSubcommands are git subcommands that cannot change any
// state for any arguments. This is deliberately stricter than
// treePreservingGitSubcommands in wt_invalidation_check.go: "safe to
// re-run" means no side effects at all, and tree-preserving writers such
// as commit/push/fetch still need the UNKNOWN reconciliation ritual.
var shellReadOnlyGitSubcommands = map[string]bool{
	"annotate": true, "blame": true, "cat-file": true, "check-attr": true,
	"check-ignore": true, "check-mailmap": true, "cherry": true,
	"count-objects": true, "describe": true, "diff": true, "diff-tree": true,
	"for-each-ref": true, "fsck": true, "grep": true, "log": true,
	"ls-files": true, "ls-remote": true, "ls-tree": true, "merge-base": true,
	"name-rev": true, "reflog": true, "rev-parse": true, "shortlog": true,
	"show": true, "show-branch": true, "status": true, "var": true,
	"verify-commit": true, "verify-tag": true, "whatchanged": true,
}

// runCommandArgsProvenReadOnly reports whether run_command tool-call
// arguments carry a command line that static analysis can prove read-only.
// Unparseable or empty arguments are never classified read-only.
func runCommandArgsProvenReadOnly(args []byte) bool {
	var parsed struct {
		Command string `json:"command"`
	}
	if err := json.Unmarshal(args, &parsed); err != nil {
		return false
	}
	if strings.TrimSpace(parsed.Command) == "" {
		return false
	}
	return shellLineReadOnly(parsed.Command, 0)
}

// shellLineReadOnly analyzes a full shell command line: every segment of
// every pipeline (;, &&, ||, |, |& and newline separated) must be
// read-only. depth bounds recursion through subshells and process
// substitutions.
func shellLineReadOnly(line string, depth int) bool {
	if depth > shellSurfaceMaxDepth {
		return false
	}
	if strings.TrimSpace(line) == "" {
		return true
	}
	// Command substitution and backticks execute text we cannot inspect.
	if strings.Contains(line, "$(") || strings.Contains(line, "`") {
		return false
	}

	ok := true
	var seg strings.Builder
	flush := func() {
		if !segmentReadOnly(seg.String(), depth) {
			ok = false
		}
		seg.Reset()
	}

	pendingHeredoc := ""
	pendingDash := false
	inSingle, inDouble := false, false
	i, n := 0, len(line)
	for i < n {
		c := line[i]
		if inSingle {
			if c == '\'' {
				inSingle = false
			}
			seg.WriteByte(c)
			i++
			continue
		}
		if inDouble {
			if c == '\\' && i+1 < n {
				seg.WriteByte(c)
				seg.WriteByte(line[i+1])
				i += 2
				continue
			}
			if c == '"' {
				inDouble = false
			}
			seg.WriteByte(c)
			i++
			continue
		}
		switch {
		case c == '\\':
			if i+1 >= n {
				return false
			}
			seg.WriteByte(c)
			seg.WriteByte(line[i+1])
			i += 2
		case c == '\'':
			inSingle = true
			seg.WriteByte(c)
			i++
		case c == '"':
			inDouble = true
			seg.WriteByte(c)
			i++
		case c == ';' || c == '\n':
			flush()
			i++
			if pendingHeredoc != "" {
				next, terminated := skipHeredocBody(line, i, pendingHeredoc, pendingDash)
				if !terminated {
					return false
				}
				pendingHeredoc = ""
				i = next
			}
		case c == '&' && i+1 < n && line[i+1] == '&':
			flush()
			i += 2
		case c == '&' && i+1 < n && line[i+1] == '>':
			end := i + 1
			if end+1 < n && line[end+1] == '>' {
				end++
			}
			if !redirectTargetIsNull(line, end+1) {
				ok = false
			}
			i = end + 1
		case c == '&':
			flush()
			i++
		case c == '|' && i+1 < n && (line[i+1] == '|' || line[i+1] == '&'):
			flush()
			i += 2
		case c == '|':
			flush()
			i++
		case c == '(':
			inner, next, matched := matchParens(line, i)
			if !matched {
				return false
			}
			if !shellLineReadOnly(inner, depth+1) {
				ok = false
			}
			i = next
		case c == ')':
			return false
		case c == '<' && i+1 < n && line[i+1] == '<':
			if i+2 < n && line[i+2] == '<' {
				i += 3 // here-string: the following word is data
				continue
			}
			delim, dash, next, provable := parseHeredocStart(line, i)
			if !provable {
				return false
			}
			pendingHeredoc = delim
			pendingDash = dash
			i = next
		case c == '<':
			if i+1 < n && line[i+1] == '(' {
				inner, next, matched := matchParens(line, i+1)
				if !matched {
					return false
				}
				if !shellLineReadOnly(inner, depth+1) {
					ok = false
				}
				i = next
				continue
			}
			if i+1 < n && line[i+1] == '>' {
				ok = false // <> opens the target read-write
				i += 2
				continue
			}
			i++
		case c == '>':
			if i+1 < n && line[i+1] == '(' {
				inner, next, matched := matchParens(line, i+1)
				if !matched {
					return false
				}
				if !shellLineReadOnly(inner, depth+1) {
					ok = false
				}
				i = next
				continue
			}
			if i+1 < n && line[i+1] == '&' {
				if i+2 < n && line[i+2] >= '0' && line[i+2] <= '9' {
					j := i + 2
					for j < n && line[j] >= '0' && line[j] <= '9' {
						j++
					}
					i = j // fd duplication such as 2>&1: not a file write
					continue
				}
				if !redirectTargetIsNull(line, i+2) {
					ok = false // >&file behaves like >file 2>&1
				}
				i++
				continue
			}
			if i+1 < n && line[i+1] == '>' {
				if !redirectTargetIsNull(line, i+2) {
					ok = false
				}
				i += 2
				continue
			}
			if !redirectTargetIsNull(line, i+1) {
				ok = false
			}
			i++
		default:
			seg.WriteByte(c)
			i++
		}
	}
	if inSingle || inDouble || pendingHeredoc != "" {
		return false
	}
	flush()
	return ok
}

// segmentReadOnly classifies one pipeline segment. Prefixes (env
// assignments, env/timeout/command/nice) are transparent: the real program
// behind them is what gets classified.
func segmentReadOnly(seg string, depth int) bool {
	words, splitOK := shellSplitWords(seg)
	if !splitOK {
		return false
	}
	if len(words) == 0 {
		return true
	}
	idx := 0
	program := ""
	for idx < len(words) {
		w := words[idx]
		if isShellEnvAssignment(w) {
			idx++
			continue
		}
		if w == "env" {
			idx++
			continue
		}
		if w == "timeout" {
			idx++
			for idx < len(words) && strings.HasPrefix(words[idx], "-") && words[idx] != "--" {
				switch words[idx] {
				case "-k", "-s", "--kill-after", "--signal":
					idx += 2
				default:
					idx++
				}
			}
			if idx < len(words) && words[idx] == "--" {
				idx++
			}
			if idx < len(words) && isShellDuration(words[idx]) {
				idx++
			}
			continue
		}
		if w == "command" || w == "nice" {
			idx++
			for idx < len(words) && strings.HasPrefix(words[idx], "-") && words[idx] != "--" {
				if w == "nice" && (words[idx] == "-n" || words[idx] == "--adjustment") {
					idx += 2
					continue
				}
				idx++
			}
			if idx < len(words) && words[idx] == "--" {
				idx++
			}
			continue
		}
		program = w
		break
	}
	if program == "" {
		return true
	}
	if i := strings.LastIndexByte(program, '/'); i >= 0 {
		program = program[i+1:]
	}
	rest := words[idx+1:]
	if guard, ok := guardedReadOnlyShellPrograms[program]; ok {
		return guard(rest, seg)
	}
	return readOnlyShellPrograms[program]
}

// shellSplitWords splits a shell fragment into words, honoring single
// quotes, double quotes and backslash escapes. The boolean is false for
// unterminated quoting, which callers treat as not provable (an empty
// fragment is valid and yields no words).
func shellSplitWords(s string) ([]string, bool) {
	var words []string
	var cur strings.Builder
	inWord := false
	i := 0
	for i < len(s) {
		c := s[i]
		switch {
		case c == '\'':
			j := strings.IndexByte(s[i+1:], '\'')
			if j < 0 {
				return nil, false
			}
			inWord = true
			cur.WriteString(s[i+1 : i+1+j])
			i += j + 2
		case c == '"':
			inWord = true
			i++
			for i < len(s) && s[i] != '"' {
				if s[i] == '\\' && i+1 < len(s) && strings.ContainsRune("\"\\$`", rune(s[i+1])) {
					i++
				}
				cur.WriteByte(s[i])
				i++
			}
			if i >= len(s) {
				return nil, false
			}
			i++
		case c == '\\':
			if i+1 >= len(s) {
				return nil, false
			}
			inWord = true
			cur.WriteByte(s[i+1])
			i += 2
		case c == ' ' || c == '\t' || c == '\n' || c == '\r':
			if inWord {
				words = append(words, cur.String())
				cur.Reset()
				inWord = false
			}
			i++
		default:
			inWord = true
			cur.WriteByte(c)
			i++
		}
	}
	if inWord {
		words = append(words, cur.String())
	}
	return words, true
}

func isShellEnvAssignment(w string) bool {
	eq := strings.IndexByte(w, '=')
	if eq <= 0 {
		return false
	}
	for i := 0; i < eq; i++ {
		c := w[i]
		valid := c == '_' || (c >= 'a' && c <= 'z') || (c >= 'A' && c <= 'Z') || (i > 0 && c >= '0' && c <= '9')
		if !valid {
			return false
		}
	}
	return true
}

func isShellDuration(w string) bool {
	if w == "" {
		return false
	}
	i := 0
	for i < len(w) && w[i] >= '0' && w[i] <= '9' {
		i++
	}
	if i == 0 {
		return false
	}
	if i < len(w) && w[i] == '.' {
		i++
		frac := i
		for i < len(w) && w[i] >= '0' && w[i] <= '9' {
			i++
		}
		if i == frac {
			return false
		}
	}
	if i < len(w) {
		switch w[i] {
		case 's', 'm', 'h', 'd':
			i++
		default:
			return false
		}
	}
	return i == len(w)
}

// matchParens finds the parenthesis group opening at open and returns its
// inner text plus the index just past its closing parenthesis.
func matchParens(line string, open int) (inner string, next int, matched bool) {
	depth := 0
	inSingle, inDouble := false, false
	for i := open; i < len(line); i++ {
		c := line[i]
		if inSingle {
			if c == '\'' {
				inSingle = false
			}
			continue
		}
		if inDouble {
			if c == '\\' {
				i++
				continue
			}
			if c == '"' {
				inDouble = false
			}
			continue
		}
		switch c {
		case '\'':
			inSingle = true
		case '"':
			inDouble = true
		case '\\':
			i++
		case '(':
			depth++
		case ')':
			depth--
			if depth == 0 {
				return line[open+1 : i], i + 1, true
			}
		}
	}
	return "", 0, false
}

// redirectTargetIsNull reports whether the word starting at or after j is
// exactly /dev/null, the one redirect target that writes nothing.
func redirectTargetIsNull(line string, j int) bool {
	for j < len(line) && (line[j] == ' ' || line[j] == '\t') {
		j++
	}
	const nullDevice = "/dev/null"
	if !strings.HasPrefix(line[j:], nullDevice) {
		return false
	}
	if len(line[j:]) == len(nullDevice) {
		return true
	}
	switch line[j+len(nullDevice)] {
	case ' ', '\t', '\n', '\r', '>', '<', ';', '|', '&', ')':
		return true
	}
	return false
}

// parseHeredocStart parses <<DELIM / <<-'DELIM' starting at line[i] == '<'
// and returns the unquoted delimiter plus the index where the rest of the
// introducing line continues.
func parseHeredocStart(line string, i int) (delim string, dash bool, next int, provable bool) {
	n := len(line)
	j := i + 2
	if j < n && line[j] == '-' {
		dash = true
		j++
	}
	for j < n && (line[j] == ' ' || line[j] == '\t') {
		j++
	}
	var d strings.Builder
	for j < n {
		ch := line[j]
		if ch == '\'' || ch == '"' {
			quote := ch
			j++
			for j < n && line[j] != quote {
				d.WriteByte(line[j])
				j++
			}
			if j >= n {
				return "", false, 0, false
			}
			j++
			continue
		}
		if ch == ' ' || ch == '\t' || ch == '\n' {
			break
		}
		if ch == '\\' && j+1 < n {
			j++
			d.WriteByte(line[j])
			j++
			continue
		}
		d.WriteByte(ch)
		j++
	}
	if d.Len() == 0 {
		return "", false, 0, false
	}
	return d.String(), dash, j, true
}

// skipHeredocBody advances past the heredoc body: everything up to (but not
// including the newline after) the line that equals the delimiter. The body
// is stdin data, never executed, so it is not analyzed.
func skipHeredocBody(line string, i int, delim string, dash bool) (next int, terminated bool) {
	for i <= len(line) {
		end := strings.IndexByte(line[i:], '\n')
		var content string
		if end < 0 {
			content = line[i:]
		} else {
			content = line[i : i+end]
		}
		c := content
		if dash {
			c = strings.TrimLeft(c, "\t")
		}
		if c == delim {
			if end < 0 {
				return len(line), true
			}
			return i + end, true
		}
		if end < 0 {
			return 0, false
		}
		i += end + 1
	}
	return 0, false
}

func sedArgsReadOnly(rest []string, raw string) bool {
	for _, t := range rest {
		if t == "--in-place" || strings.HasPrefix(t, "--in-place=") {
			return false
		}
		if strings.HasPrefix(t, "-") && !strings.HasPrefix(t, "--") && strings.Contains(t, "i") {
			return false
		}
	}
	return true
}

func awkArgsReadOnly(rest []string, raw string) bool {
	for _, bad := range []string{">", "|", "system(", "getline", "|&"} {
		if strings.Contains(raw, bad) {
			return false
		}
	}
	return true
}

func findArgsReadOnly(rest []string, raw string) bool {
	for _, t := range rest {
		switch {
		case t == "-delete" || t == "-exec" || t == "-execdir" || t == "-ok" || t == "-okdir":
			return false
		case strings.HasPrefix(t, "-fprint") || strings.HasPrefix(t, "-fls"):
			return false
		}
	}
	return true
}

func dateArgsReadOnly(rest []string, raw string) bool {
	for _, t := range rest {
		if t == "--set" || strings.HasPrefix(t, "--set=") {
			return false
		}
		if strings.HasPrefix(t, "-") && !strings.HasPrefix(t, "--") && strings.Contains(t, "s") {
			return false
		}
	}
	return true
}

func sysctlArgsReadOnly(rest []string, raw string) bool {
	for _, t := range rest {
		if t == "--write" {
			return false
		}
		if strings.HasPrefix(t, "-") && !strings.HasPrefix(t, "--") && strings.Contains(t, "w") {
			return false
		}
	}
	return true
}

func rgArgsReadOnly(rest []string, raw string) bool {
	for _, t := range rest {
		if t == "--pre" || t == "--pre-guest" || t == "--pre-bind" || strings.HasPrefix(t, "--pre=") {
			return false
		}
	}
	return true
}

func curlArgsReadOnly(rest []string, raw string) bool {
	for i := 0; i < len(rest); i++ {
		t := rest[i]
		switch {
		case t == "-X" || t == "--request":
			if i+1 >= len(rest) {
				return false
			}
			switch strings.ToUpper(rest[i+1]) {
			case "GET", "HEAD":
			default:
				return false
			}
			i++
		case strings.HasPrefix(t, "--request="):
			switch strings.ToUpper(strings.TrimPrefix(t, "--request=")) {
			case "GET", "HEAD":
			default:
				return false
			}
		case strings.HasPrefix(t, "-X") && len(t) > 2:
			switch strings.ToUpper(t[2:]) {
			case "GET", "HEAD":
			default:
				return false
			}
		case strings.HasPrefix(t, "-") && !strings.HasPrefix(t, "--") && strings.ContainsAny(t[1:], "acdFJTDO"):
			// short-flag clusters carrying a write-capable flag: o/O
			// output, d/D data or dump-header, F/J form, T upload, c
			// cookie-jar, a append.
			return false
		case t == "--data" || t == "--form" || t == "--output" || t == "--output-document" ||
			t == "--upload-file" || t == "--form-string" || t == "--dump-header" ||
			strings.HasPrefix(t, "--data") || strings.HasPrefix(t, "--form") ||
			strings.HasPrefix(t, "--trace") || strings.HasPrefix(t, "--output") ||
			strings.HasPrefix(t, "--upload") || strings.HasPrefix(t, "-T") || t == "-o" ||
			t == "-d" || t == "-F" || t == "-c" || t == "-a" || t == "-D":
			return false
		}
	}
	return true
}

func gitArgsReadOnly(rest []string, raw string) bool {
	if len(rest) == 0 {
		return true
	}
	switch rest[0] {
	case "branch", "remote", "tag":
		// list forms take flags only; any operand creates, deletes or
		// rewrites (git branch <name>, git remote add ...)
		for _, t := range rest[1:] {
			if !strings.HasPrefix(t, "-") {
				return false
			}
		}
		return true
	case "config":
		return gitConfigArgsReadOnly(rest[1:])
	case "hash-object":
		for _, t := range rest[1:] {
			if t == "-w" {
				return false
			}
		}
		return true
	case "stash":
		return len(rest) >= 2 && (rest[1] == "list" || rest[1] == "show")
	case "symbolic-ref":
		for _, t := range rest[1:] {
			if t == "-d" || t == "--delete" {
				return false
			}
		}
		return true
	case "worktree":
		return len(rest) >= 2 && rest[1] == "list"
	case "archive":
		for _, t := range rest[1:] {
			if t == "--output" || strings.HasPrefix(t, "--output=") {
				return false
			}
		}
		return true
	}
	return shellReadOnlyGitSubcommands[rest[0]]
}

func gitConfigArgsReadOnly(args []string) bool {
	operands := 0
	sawReadFlag := false
	for _, t := range args {
		switch {
		case strings.HasPrefix(t, "--get"):
			sawReadFlag = true
		case t == "--list" || t == "-l":
			sawReadFlag = true
		case t == "--add" || t == "--edit" || t == "-e" || t == "--remove-section" ||
			t == "--rename-section" || t == "--replace-all" || t == "--unset" || t == "--unset-all":
			return false
		case strings.HasPrefix(t, "-"):
		default:
			operands++
		}
	}
	return sawReadFlag || operands == 1
}

var ghReadOnlyVerbs = map[string]bool{
	"checks": true, "diff": true, "list": true, "search": true,
	"status": true, "view": true, "watch": true,
}

var ghReadOnlyScopes = map[string]bool{
	"actions": true, "gist": true, "gists": true, "issue": true, "issues": true,
	"label": true, "labels": true, "pr": true, "project": true, "projects": true,
	"release": true, "releases": true, "repo": true, "repos": true,
	"run": true, "runs": true, "workflow": true, "workflows": true,
}

func ghArgsReadOnly(rest []string, raw string) bool {
	if len(rest) == 0 {
		return true
	}
	switch rest[0] {
	case "api":
		return ghApiArgsReadOnly(rest[1:], raw)
	case "auth":
		return len(rest) >= 2 && rest[1] == "status"
	}
	if ghReadOnlyVerbs[rest[0]] {
		return true
	}
	if ghReadOnlyScopes[rest[0]] && len(rest) >= 2 && ghReadOnlyVerbs[rest[1]] {
		return true
	}
	return false
}

func ghApiArgsReadOnly(rest []string, raw string) bool {
	if strings.Contains(strings.Join(rest, " "), "graphql") {
		// gh api graphql always POSTs; the request is read-only exactly
		// when the query text is not a mutation.
		return !strings.Contains(raw, "mutation")
	}
	method := ""
	for i := 0; i < len(rest); i++ {
		t := rest[i]
		switch {
		case t == "--method" || t == "-X":
			if i+1 < len(rest) {
				method = rest[i+1]
			}
		case strings.HasPrefix(t, "--method="):
			method = strings.TrimPrefix(t, "--method=")
		case strings.HasPrefix(t, "-X") && len(t) > 2:
			method = t[2:]
		}
	}
	if method == "" {
		return true // gh api defaults to GET
	}
	switch strings.ToUpper(method) {
	case "GET", "HEAD":
		return true
	}
	return false
}
