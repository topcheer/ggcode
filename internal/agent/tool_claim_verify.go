package agent

// Tool Output Claim Verification
//
// Research basis: AgentRx (arXiv:2602.02475, Microsoft Research, Feb 2026)
// identifies "Misinterpretation of Tool Output" as one of 9 cross-domain agent
// failure categories. Agents read tool outputs incorrectly - claiming success
// when commands failed, claiming findings when searches returned nothing, or
// acting on stale "not found" messages. The paper's key insight: failure is
// often NOT in the tool result itself (IsError=true), but in the agent's
// interpretation of a nominally-successful result.
//
// This detector scans tool results for commonly misread failure signals that
// IsError does NOT capture:
//
//   - Commands that exit 0 but print errors (exit code in output, panics)
//   - "No such file" / "does not exist" / "not found" in results
//   - "0 matches" / "no results" in search output
//   - Build/test output containing "FAIL" or "panic:" despite exit 0
//
// When detected, it injects a brief, specific verification reminder appended
// to the tool result content. This ensures the LLM correctly interprets the
// result before making claims in its next response.
//
// Design: zero-LLM-cost, deterministic, non-blocking, capped at 3 injections
// per run to avoid noise.

import (
	"fmt"
	"strings"
	"unicode"

	"github.com/topcheer/ggcode/internal/debug"
)

// claimVerifyState tracks verification nudge injections for the current run.
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

// claimVerifyCommandTools are tools whose result content reflects the
// execution status of a command the agent ran (stdout/stderr of a shell
// command). For these tools, status patterns like "exit code: 1" are
// genuine execution-state signals and pattern scanning is appropriate.
var claimVerifyCommandTools = map[string]bool{
	"run_command":   true,
	"start_command": true,
}

// claimVerifyContentTools are content-bearing tools whose result.Content is
// arbitrary file content, match lines, or symbol listings — not the tool's
// own execution status. Running strings.Contains status patterns over such
// content produces false positives (issue #739): a successful grep for
// "does not exist", a read_file of test source containing t.Fatal, or a
// shell script containing the literal "exit code: 1" would each be injected
// with advisories contradicting the actual outcome.
//
// For these tools we only match the tool's OWN meta-status line: a short
// fixed status message the tool itself emits when it found nothing. The
// grep family returns exactly "No matches found." (optionally followed by a
// "Suggestions:" block) as the whole result on zero matches. We therefore
// require the trimmed content to START with the meta-status phrase — a
// successful grep whose first matched line merely mentions the phrase (the
// false-positive case) has match lines before/around it and will not match.
var claimVerifyContentTools = map[string]bool{
	"grep":            true,
	"search_files":    true,
	"glob":            true,
	"read_file":       true,
	"multi_file_read": true,
	"code_search":     true,
	"lsp_definition":  true,
	"lsp_references":  true,
	"lsp_symbols":     true,
	"lsp_hover":       true,
	"lsp_diagnostics": true,
}

// claimVerifyMetaStatusPrefix is the zero-result meta-status line emitted by
// the grep tool itself (internal/tool/grep.go formatGrepOutput) when the
// search succeeded but matched nothing. Matching on this prefix preserves
// the intended true positive — warning when a nominally-successful search
// actually found nothing — without treating user content that merely
// contains status-like wording as a failure signal.
// segHasDownstreamRetriever reports whether a command segment contains a
// content-retrieval command after the wrapper (#1695 case 1).
func segHasDownstreamRetriever(seg string) bool {
	f := strings.Fields(seg)
	for i := 0; i < len(f); i++ {
		switch f[i] {
		case "grep", "rg", "cat", "head", "tail":
			return true
		case "-exec":
			// find -exec grep ... \; / xargs-style suffix counts if a
			// retriever appears anywhere after -exec.
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

// claimVerifyMetaStatusPrefixes are the zero-result status prefixes various
// content tools render. The old single "no matches found." (with period)
// matched only grep's wording; search_files ("No matches found for pattern"),
// glob ("No files matched pattern") and friends never triggered the
// found-claim check (#1506).
var claimVerifyMetaStatusPrefixes = []string{
	"no matches found", "no files matched", "no results found",
	"no matches", "nothing found",
}

// claimVerifyPatterns are (pattern, message) pairs. Patterns are matched
// case-insensitively against the tool result content. Each pattern targets
// a specific misinterpretation risk identified in the AgentRx taxonomy.
type claimVerifyPattern struct {
	pattern string
	msg     string
}

var claimVerifyPatterns = []claimVerifyPattern{
	// Exit code failure masked in non-error output
	{"exit code: 1", "Command exited with code 1 (failure). Do not claim this command succeeded."},
	{"exit code 1", "Command exited with code 1 (failure). Do not claim this command succeeded."},
	{"exit status 1", "Command exited with status 1 (failure). Do not claim this command succeeded."},
	{"exit=1", "Command exited with code 1 (failure). Do not claim this command succeeded."},
	// Runtime crashes
	{"panic:", "Output contains a Go panic. This indicates a crash - do not claim success."},
	{"fatal error:", "Output contains a fatal error. Do not claim this operation succeeded."},
	// Build/test failures
	{"build failed", "Build failed. Do not claim the build passed."},
	{"compilation failed", "Compilation failed. Do not claim compilation succeeded."},
	{"fail:", "Test output contains FAIL. Do not claim all tests passed."},
	{"fail\t", "Test output contains FAIL. Do not claim all tests passed."},
	// File not found
	{"no such file or directory", "File/path does not exist. Do not claim you accessed it."},
	{"does not exist", "Path does not exist. Do not claim you found it."},
	{"not found in", "Item not found. Do not claim it exists."},
	// Empty search results
	{"0 matches", "Search returned 0 matches. Do not claim results were found."},
	{"no matches found", "Search returned no matches. Do not claim results were found."},
	{"no results", "Search returned no results. Do not claim something was found."},
}

// claimVerifyContentCmds lists shell commands whose stdout is file/data
// payload rather than execution status. When the command the agent ran is
// composed solely of these, its output is content by construction, so status
// patterns inside it are data mentions, not failure signals (issue #1207,
// extending the #739 semantic boundary to the command-tool path).
var claimVerifyContentCmds = map[string]bool{
	"grep": true, "egrep": true, "fgrep": true, "rg": true, "ag": true,
	"cat": true, "head": true, "tail": true, "less": true, "more": true,
	"bat": true, "type": true, "findstr": true, "awk": true, "sed": true,
	"cut": true, "sort": true, "uniq": true, "wc": true, "nl": true,
	"strings": true, "jq": true, "column": true, "fmt": true,
}

// isContentRetrievalCommand reports whether every stage of the command
// pipeline is a content-retrieval command. Compound commands with && || ;
// and pipes are split; if ANY segment runs a non-content command (go test,
// make, ...), the whole command is treated as status-bearing so genuine
// failure signals are still caught (conservative in the true-positive
// direction).
func isContentRetrievalCommand(cmd string) bool {
	if strings.TrimSpace(cmd) == "" {
		return false
	}
	// Split on pipeline and sequence operators.
	r := strings.NewReplacer("|", "\n", "&&", "\n", "||", "\n", ";", "\n")
	for _, seg := range strings.Split(r.Replace(cmd), "\n") {
		fields := strings.Fields(seg)
		if len(fields) == 0 {
			continue
		}
		name := fields[0]
		// #1506: git grep / git log -S / xargs grep / find -exec grep are
		// content-retrieval wrappers - their output is raw file content, not
		// command status. Without this, a successful 'git grep -n "fail:"'
		// search was condemned as a test failure (semantic reversal) on this
		// repo's most common workflow.
		if name == "git" && len(fields) > 1 {
			// #1695 case 2: skip leading GLOBAL options (-C /path, --no-
			// pager, --git-dir=...) so the common `git -C /path grep`
			// idiom reaches the subcommand instead of falling back to the
			// status-pattern scan (fields[1] == "-C" never matched).
			sub := 1
			for sub < len(fields) && strings.HasPrefix(fields[sub], "-") {
				// Value-taking global options (-C <path>, --git-dir=<v> is
				// self-contained but -C /path is not) consume the NEXT
				// field - without this the path was mistaken for the
				// subcommand and `git -C /repo grep` never exempted.
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
					continue // content-bearing git subcommand
				}
			}
		} else if name == "xargs" || name == "find" {
			// #1695 case 1: NOT unconditional - `xargs rm -rf` and
			// `find . -delete` are pure mutators whose output carries no
			// retrieved content; exempting them skipped the ENTIRE status
			// scan and soft failures (permission warnings, partial batch
			// errors) went unnoticed. Exempt only when the command line
			// also contains a downstream content retriever (grep/rg/cat/
			// find -exec ... grep), which is the case the wrapper
			// exemption exists for (#1506).
			if segHasDownstreamRetriever(seg) {
				continue
			}
		}
		// Strip env-var assignments (FOO=bar cmd) and path prefixes.
		for strings.Contains(name, "=") && len(fields) > 1 {
			fields = fields[1:]
			name = fields[0]
		}
		if i := strings.LastIndex(name, "/"); i >= 0 {
			name = name[i+1:]
		}
		if !claimVerifyContentCmds[name] {
			return false
		}
	}
	return true
}

// checkClaimVerify scans a tool result for commonly misinterpreted failure
// signals and returns guidance if a risk is detected. Returns "" if no issue
// or if the injection cap has been reached.
//
// Semantic boundary (issue #739): status patterns are only meaningful for
// command-execution tools, where Content is the command's own output. For
// content-bearing tools, Content is arbitrary user/file data, so only the
// tool's own zero-result meta-status line is checked.
//
// Command-tool boundary (issue #1207): for content-retrieval commands
// (grep/cat/rg/head/...), stdout IS file content — e.g. `grep -n 'fail:'
// foo_test.go` on a successful match prints source lines containing "fail:"
// and must not trigger a semantically inverted "Do not claim this command
// succeeded" advisory. Such commands skip the status-pattern scan entirely.
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

	// If the result is already flagged as an error, the agent is more likely
	// to interpret it correctly — skip to reduce noise.
	if isError {
		return ""
	}

	// Content-bearing tools: only the tool's own zero-result meta-status line
	// counts as a signal. Match lines / file content that merely CONTAIN the
	// phrase are payload, not status (fixes issue #739 false positives).
	if isContent {
		// #1695 case 3: meta-status prefixes are generic ("no matches") -
		// a MATCHED payload whose first line reads "no matches are
		// allowed..." would trip the advisory. The tool's own meta-status
		// line is the ENTIRE first line, so require the prefix to consume
		// it exactly (prefix + end, or prefix + space + qualifier).
		firstLine := strings.ToLower(strings.TrimSpace(content))
		if idx := strings.IndexByte(firstLine, '\n'); idx >= 0 {
			firstLine = strings.TrimSpace(firstLine[:idx])
		}
		// Longest-prefix-first: "no matches" would otherwise shadow
		// "no matches found" for the qualifier check below.
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
			// Short GENERIC prefixes ("no matches", "nothing found") stop
			// here: any continuation ("are allowed in strict mode") is a
			// matched payload sentence, not a meta qualifier. Longer
			// specific prefixes ("no matches found") accept qualifiers
			// ("for pattern: foo" - search_files wording).
			if prefix == "no matches found" || prefix == "no results found" ||
				prefix == "no files matched" {
				c.injections++
				debug.Log("claim_verify", "zero-result meta-status detected: tool=%s", toolName)
				return "[Verify] Search returned no matches. Do not claim results were found. Re-read the tool output carefully before proceeding."
			}
		}
		return ""
	}

	// Command-execution tools: content-retrieval pipelines (grep/cat/...) print
	// file data as stdout, so status patterns in the output are payload
	// mentions, not failure signals (#1207).
	if isContentRetrievalCommand(cmd) {
		return ""
	}

	// Command-execution tools: scan the command output for status patterns.
	lower := strings.ToLower(content)
	// Only scan the first 4000 chars to keep cost low on huge outputs.
	if len(lower) > 4000 {
		lower = lower[:4000]
	}

	for _, cvp := range claimVerifyPatterns {
		if strings.Contains(lower, cvp.pattern) {
			c.injections++
			debug.Log("claim_verify", "misinterpretation risk detected: tool=%s pattern=%q", toolName, cvp.pattern)
			return fmt.Sprintf("[Verify] %s Re-read the tool output carefully before proceeding.", cvp.msg)
		}
	}

	return ""
}

// trimNonPrint removes trailing non-printable noise for cleaner pattern matching.
func trimNonPrint(s string) string {
	return strings.TrimFunc(s, func(r rune) bool {
		return !unicode.IsPrint(r)
	})
}
