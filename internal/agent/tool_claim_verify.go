package agent

import (
	"fmt"
	"regexp"
	"strings"
	"unicode"

	"github.com/topcheer/ggcode/internal/debug"
)

type claimVerifyState struct {
	injections int
}

func newClaimVerifyState() *claimVerifyState {
	return &claimVerifyState{}
}

func (c *claimVerifyState) reset() {
	c.injections = 0
}

const claimVerifyMaxInjections = 3

var claimVerifyCommandTools = map[string]bool{
	"run_command":   true,
	"start_command": true,
}

var claimVerifyContentTools = map[string]bool{
	"grep":         true,
	"search_files": true,
	"glob":         true,

	"code_search":     true,
	"lsp_definition":  true,
	"lsp_references":  true,
	"lsp_symbols":     true,
	"lsp_hover":       true,
	"lsp_diagnostics": true,
}

func segHasDownstreamRetriever(seg string) bool {
	f := strings.Fields(seg)
	for i := 0; i < len(f); i++ {
		switch f[i] {
		case "grep", "rg", "cat", "head", "tail":
			return true
		case "-exec":

			for j := i + 1; j < len(f); j++ {
				switch f[j] {
				case "grep", "rg", "cat", "head", "tail":
					return true
				}
			}
			return false
		}
	}
	return false
}

var claimVerifyMetaStatusPrefixes = []string{
	"no matches found", "no files matched", "no results found",
	"no matches", "nothing found",
}

type claimVerifyPattern struct {
	pattern string
	msg     string
	hard    bool

	// anchored restricts matching to the start of a line. Verdict-family
	// patterns (exit status, panic, go-test FAIL lines) are anchored so
	// that failure text merely QUOTED inside a successful result (issue
	// bodies, diffs, review comments, grep output) no longer trips the
	// banner (#4990 mode 1). Real verdict lines are emitted by the tool
	// itself at column 0; quoted text almost always carries leading
	// whitespace or surrounding prose.
	anchored bool
}

var claimVerifyReCache = func() map[string]*regexp.Regexp {
	m := make(map[string]*regexp.Regexp)
	for _, cvp := range claimVerifyPatterns {
		if strings.HasPrefix(cvp.pattern, "re:") {
			body := strings.TrimPrefix(cvp.pattern, "re:")
			if re, err := regexp.Compile(body); err == nil {
				m[cvp.pattern] = re
			}
			if cvp.anchored {
				if re, err := regexp.Compile(`(?:^|\s)(?:` + body + `)`); err == nil {
					m[cvp.pattern+"|anchored"] = re
				}
			}
		}
	}
	return m
}()

func claimVerifyMatch(lower, pattern string) bool {
	if strings.HasPrefix(pattern, "re:") {
		if re := claimVerifyReCache[pattern]; re != nil {
			return re.MatchString(lower)
		}
		return false
	}
	return strings.Contains(lower, pattern)
}

var claimVerifyPatterns = []claimVerifyPattern{

	{"exit code: 1", "Command exited with code 1 (failure). Do not claim this command succeeded.", true, true},
	{"exit code 1", "Command exited with code 1 (failure). Do not claim this command succeeded.", true, true},
	{"exit status 1", "Command exited with status 1 (failure). Do not claim this command succeeded.", true, true},
	{"exit=1", "Command exited with code 1 (failure). Do not claim this command succeeded.", true, true},

	{"panic:", "Output contains a Go panic. This indicates a crash - do not claim success.", true, true},
	{"fatal error:", "Output contains a fatal error. Do not claim this operation succeeded.", true, true},

	{"build failed", "Build failed. Do not claim the build passed.", false, false},
	{"compilation failed", "Compilation failed. Do not claim compilation succeeded.", false, false},

	{"--- fail:", "Test output contains FAIL. Do not claim all tests passed.", true, true},
	{"re:fail:[1-9]", "Test output contains FAIL. Do not claim all tests passed.", true, true},
	{"re:fail\t[1-9]", "Test output contains FAIL. Do not claim all tests passed.", true, true},
	{"fail\t", "Test output contains FAIL. Do not claim all tests passed.", true, true},

	{"[build failed]", "Test output contains FAIL. Do not claim all tests passed.", true, true},

	{"no such file or directory", "File/path does not exist. Do not claim you accessed it.", false, false},
	{"does not exist", "Path does not exist. Do not claim you found it.", false, false},
	{"not found in", "Item not found. Do not claim it exists.", false, false},

	{"0 matches", "Search returned 0 matches. Do not claim results were found.", false, false},
	{"no matches found", "Search returned no matches. Do not claim results were found.", false, false},
	{"no results", "Search returned no results. Do not claim something was found.", false, false},
}

var claimVerifyContentCmds = map[string]bool{
	"grep": true, "egrep": true, "fgrep": true, "rg": true, "ag": true,
	"cat": true, "head": true, "tail": true, "less": true, "more": true,
	"bat": true, "type": true, "findstr": true, "awk": true, "sed": true,
	"cut": true, "sort": true, "uniq": true, "wc": true, "nl": true,
	"strings": true, "jq": true, "column": true, "fmt": true,
}

var claimVerifyNeutralCmds = map[string]bool{
	"echo": true, "printf": true, "true": true, "false": true,
	"cd": true, "pwd": true, "sleep": true, "wait": true,
	"test": true, "[": true, ":": true,
	"export": true, "set": true, "unset": true,
}

func classifyCommandSegments(cmd string) (hasContent, hasNeutral, hasStatus bool) {
	if strings.TrimSpace(cmd) == "" {
		return false, false, false
	}

	r := strings.NewReplacer("|", "\n", "&&", "\n", "||", "\n", ";", "\n")
	for _, seg := range strings.Split(r.Replace(cmd), "\n") {
		fields := strings.Fields(seg)
		if len(fields) == 0 {
			continue
		}
		name := fields[0]

		if name == "git" && len(fields) > 1 {

			sub := 1
			for sub < len(fields) && strings.HasPrefix(fields[sub], "-") {

				if fields[sub] == "-C" || fields[sub] == "--git-dir" ||
					fields[sub] == "--work-tree" || fields[sub] == "--namespace" {
					sub += 2
				} else {
					sub++
				}
			}
			if sub < len(fields) {
				switch fields[sub] {
				case "grep", "log", "show", "blame":
					hasContent = true
					continue
				}
			}
		} else if name == "xargs" || name == "find" {

			if segHasDownstreamRetriever(seg) {
				hasContent = true
				continue
			}
		}

		for strings.Contains(name, "=") && len(fields) > 1 {
			fields = fields[1:]
			name = fields[0]
		}
		if i := strings.LastIndex(name, "/"); i >= 0 {
			name = name[i+1:]
		}
		switch {
		case claimVerifyNeutralCmds[name]:
			hasNeutral = true
		case claimVerifyContentCmds[name]:
			hasContent = true
		default:
			hasStatus = true
		}
	}
	return hasContent, hasNeutral, hasStatus
}

func isContentRetrievalCommand(cmd string) bool {
	hasContent, hasNeutral, hasStatus := classifyCommandSegments(cmd)
	return !hasStatus && (hasContent || hasNeutral)
}

func (c *claimVerifyState) check(toolName, content string, isError bool, cmd string) string {
	if c.injections >= claimVerifyMaxInjections {
		return ""
	}
	isCommand := claimVerifyCommandTools[toolName]
	isContent := claimVerifyContentTools[toolName]
	if !isCommand && !isContent {
		return ""
	}
	if content == "" {
		return ""
	}

	if isError {
		return ""
	}

	if isContent {

		firstLine := strings.ToLower(strings.TrimSpace(content))
		if idx := strings.IndexByte(firstLine, '\n'); idx >= 0 {
			firstLine = strings.TrimSpace(firstLine[:idx])
		}

		for i := range claimVerifyMetaStatusPrefixes {
			for j := i + 1; j < len(claimVerifyMetaStatusPrefixes); j++ {
				if len(claimVerifyMetaStatusPrefixes[j]) > len(claimVerifyMetaStatusPrefixes[i]) {
					claimVerifyMetaStatusPrefixes[i], claimVerifyMetaStatusPrefixes[j] =
						claimVerifyMetaStatusPrefixes[j], claimVerifyMetaStatusPrefixes[i]
				}
			}
		}
		for _, prefix := range claimVerifyMetaStatusPrefixes {
			if len(firstLine) < len(prefix) || !strings.HasPrefix(firstLine, prefix) {
				continue
			}
			rest := firstLine[len(prefix):]
			if rest == "" || strings.HasPrefix(rest, ":") || strings.HasPrefix(rest, ".") {
				c.injections++
				debug.Log("claim_verify", "zero-result meta-status detected: tool=%s", toolName)
				return "[Verify] Search returned no matches. Do not claim results were found. Re-read the tool output carefully before proceeding."
			}

			if prefix == "no matches found" || prefix == "no results found" ||
				prefix == "no files matched" {
				c.injections++
				debug.Log("claim_verify", "zero-result meta-status detected: tool=%s", toolName)
				return "[Verify] Search returned no matches. Do not claim results were found. Re-read the tool output carefully before proceeding."
			}
		}
		return ""
	}

	hasContent, hasNeutral, hasStatus := classifyCommandSegments(cmd)
	if !hasStatus && (hasContent || hasNeutral) {
		return ""
	}

	mixed := hasStatus && hasContent

	lower := strings.ToLower(content)

	if len(lower) > 8000 {
		lower = lower[:4000] + "\n" + lower[len(lower)-4000:]
	}

	for _, cvp := range claimVerifyPatterns {

		if mixed && !cvp.hard {
			continue
		}
		if cvp.anchored {
			if !claimVerifyMatchAnchored(lower, cvp.pattern) {
				continue
			}
		} else if !claimVerifyMatch(lower, cvp.pattern) {
			continue
		}
		c.injections++
		debug.Log("claim_verify", "misinterpretation risk detected: tool=%s pattern=%q", toolName, cvp.pattern)
		return fmt.Sprintf("[Verify] %s Re-read the tool output carefully before proceeding.", cvp.msg)
	}

	return ""
}

// claimVerifyMatchAnchored reports whether any line of the (already
// lowercased) content starts with the pattern. Verdict lines emitted by
// tools sit at column 0; text merely quoting those verdicts is almost
// always indented or wrapped in prose (#4990 mode 1).
func claimVerifyMatchAnchored(lower, pattern string) bool {
	if strings.HasPrefix(pattern, "re:") {
		re := claimVerifyReCache[pattern+"|anchored"]
		if re == nil {
			return false
		}
		for _, line := range strings.Split(lower, "\n") {
			if re.MatchString(strings.TrimRight(line, "\r")) {
				return true
			}
		}
		return false
	}
	for _, line := range strings.Split(lower, "\n") {
		if strings.HasPrefix(strings.TrimRight(line, "\r"), pattern) {
			return true
		}
	}
	return false
}

func trimNonPrint(s string) string {
	return strings.TrimFunc(s, func(r rune) bool {
		return !unicode.IsPrint(r)
	})
}
