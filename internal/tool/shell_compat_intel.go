package tool

// Shell Compatibility Intelligence -- BSD vs GNU Command Incompatibility Detection
//
// Research basis: AI agents frequently generate shell commands trained on Linux
// (GNU coreutils), which fail on macOS (BSD coreutils). Common examples:
//   - sed -i 's/x/y/' file     -> BSD needs backup suffix: sed -i '' 's/x/y/'
//   - readlink -f path         -> BSD doesn't support -f; use realpath or python
//   - grep -P 'pattern'        -> BSD grep lacks PCRE; use grep -E or rg
//   - date -d 'yesterday'      -> BSD uses date -v-1d
//   - stat -c '%s' file        -> BSD uses stat -f '%z'
//   - find . -printf '%p\n'    -> BSD doesn't support -printf
//   - head -n -1 file          -> BSD doesn't support negative offsets
//   - timeout 5 cmd            -> not available on macOS; use gtimeout or perl
//
// Competitor analysis:
//   - Claude Code: no platform-aware shell diagnostics
//   - Cursor: no detection of BSD/GNU differences
//   - Cline/OpenHands: raw error output, no compatibility hints
//   - Aider: no platform-awareness
//
// Gap: When a command fails due to BSD/GNU incompatibility, the error message
// is often cryptic ("illegal option", "extra characters", etc.) and the agent
// wastes multiple iterations debugging instead of switching to a compatible
// command.
//
// Design:
//   - Zero LLM cost (deterministic pattern matching)
//   - Produces a "[Shell Compat]" diagnostic with the specific fix
//   - Only fires when a known incompatibility pattern is detected
//   - Includes the corrected command as an actionable suggestion
//   - Detects from BOTH the command itself (proactive) and error output (reactive)

import (
	"runtime"
	"strings"
)

// shellCompatPattern matches a known BSD/GNU incompatibility in command output
// or command syntax, providing the fix.
type shellCompatPattern struct {
	// match function checks if this pattern applies
	match func(command, combinedOutput string) bool
	// fix describes the correct cross-platform approach
	fix string
}

// shellCompatPatterns is the ordered list of incompatibility detectors.
// Order matters: more specific patterns first.
var shellCompatPatterns = []shellCompatPattern{
	// --- sed -i without backup suffix (GNU) vs BSD ---
	{
		match: func(cmd, out string) bool {
			// BSD sed error when -i is used without backup suffix argument
			return strings.Contains(out, "extra characters at the end of") ||
				strings.Contains(out, "command a expects") ||
				(strings.Contains(out, "sed:") && strings.Contains(out, "-i"))
		},
		fix: "BSD sed requires a backup suffix after -i. Use: sed -i '' 's/old/new/' file  (macOS/BSD) instead of sed -i 's/old/new/' file  (GNU)",
	},
	// --- readlink -f (GNU only) ---
	{
		match: func(cmd, out string) bool {
			return (strings.HasPrefix(strings.TrimSpace(cmd), "readlink") && strings.Contains(cmd, " -f")) ||
				strings.Contains(out, "readlink: illegal option") ||
				strings.Contains(out, "readlink: invalid option -- 'f'")
		},
		fix: "readlink -f is GNU-only. On macOS/BSD use: realpath file  or  python3 -c \"import os; print(os.path.realpath('file'))\"",
	},
	// --- grep -P (GNU PCRE) ---
	{
		match: func(cmd, out string) bool {
			return (strings.HasPrefix(strings.TrimSpace(cmd), "grep") && strings.Contains(cmd, " -p")) ||
				strings.Contains(out, "grep: option requires an argument") ||
				(strings.Contains(out, "grep:") && strings.Contains(out, "invalid option"))
		},
		fix: "grep -P (PCRE) is GNU-only. Use: grep -E 'pattern' (ERE, cross-platform) or rg 'pattern' (ripgrep, if installed)",
	},
	// --- date -d (GNU) ---
	{
		match: func(cmd, out string) bool {
			return (strings.HasPrefix(strings.TrimSpace(cmd), "date") && strings.Contains(cmd, " -d")) ||
				strings.Contains(out, "date: illegal option") ||
				strings.Contains(out, "date: invalid option -- 'd'")
		},
		fix: "date -d is GNU-only. On macOS/BSD use: date -v-1d (yesterday) or date -v+1d (tomorrow). For parsing use: python3 -c \"from datetime import datetime; print(datetime.strptime('2024-01-01', '%Y-%m-%d'))\"",
	},
	// --- stat -c (GNU) vs stat -f (BSD) ---
	{
		match: func(cmd, out string) bool {
			return (strings.HasPrefix(strings.TrimSpace(cmd), "stat") && strings.Contains(cmd, " -c")) ||
				strings.Contains(out, "stat: illegal option") ||
				strings.Contains(out, "stat: invalid option -- 'c'")
		},
		fix: "stat -c is GNU-only. On macOS/BSD use: stat -f '%z' file (file size), stat -f '%m' file (mtime). Or use: wc -c < file for size",
	},
	// --- find -printf (GNU only) ---
	{
		match: func(cmd, out string) bool {
			// #1342/#868: anchor to the find command itself - a bare Contains
			// matches any 'find' substring (refind-printf, ./findings/...).
			isFind := strings.HasPrefix(cmd, "find ") || strings.Contains(cmd, " find ") || cmd == "find"
			return (isFind && strings.Contains(cmd, "-printf")) ||
				strings.Contains(out, "find: -printf: unknown primary") ||
				strings.Contains(out, "find: unknown predicate `-printf'")
		},
		fix: "find -printf is GNU-only. On macOS/BSD use: find . -name '*.go' | xargs -I{} echo {}  or  find . -name '*.go' -exec echo {} \\;",
	},
	// --- head -n -N (GNU negative offset) ---
	{
		match: func(cmd, out string) bool {
			// #1342/#868: anchor to the head command (forehead, path
			// components containing 'head' must not trigger).
			isHead := strings.HasPrefix(cmd, "head ") || strings.Contains(cmd, " head ") || cmd == "head"
			return strings.Contains(out, "head: illegal line count -- -") ||
				(isHead && strings.Contains(cmd, "-n -"))
		},
		fix: "head -n -N (drop last N lines) is GNU-only. On macOS/BSD use: head -n $(( $(wc -l < file) - N )) file  or  sed '$d' file (drop last line)",
	},
	// --- timeout command (GNU coreutils) ---
	{
		match: func(cmd, out string) bool {
			trimmed := strings.TrimSpace(cmd)
			return strings.HasPrefix(trimmed, "timeout ") ||
				(strings.Contains(out, "command not found") && strings.Contains(out, "timeout"))
		},
		fix: "timeout is GNU coreutils, not on macOS by default. Install with: brew install coreutils (provides gtimeout), or use: perl -e 'alarm shift; exec @ARGV' 5 cmd",
	},
	// --- sort -V (GNU version sort) ---
	{
		match: func(cmd, out string) bool {
			return (strings.HasPrefix(strings.TrimSpace(cmd), "sort") && strings.Contains(cmd, "-v")) ||
				(strings.Contains(out, "sort: unrecognized option") && strings.Contains(out, "v"))
		},
		fix: "sort -V (version sort) is GNU-only. On macOS/BSD use: sort -t. -k1,1n  or install coreutils: brew install coreutils (provides gsort -V)",
	},
	// --- du --max-depth (GNU) ---
	{
		match: func(cmd, out string) bool {
			// #868: anchor to the du COMMAND (not substrings like "gradle",
			// "tools/") - same token-anchoring style as sibling patterns.
			isDu := strings.HasPrefix(cmd, "du ") || strings.Contains(cmd, " du ") || cmd == "du"
			return isDu && strings.Contains(cmd, "--max-depth")
		},
		fix: "du --max-depth is GNU-only. On macOS/BSD use: du -d N -h (e.g., du -d 1 -h for depth 1)",
	},
	// --- ls with GNU long options ---
	{
		match: func(cmd, out string) bool {
			// #868: anchor to the ls command; tokens containing "ls" as a
			// substring (e.g. "tools", "curls") must not trigger.
			isLs := strings.HasPrefix(cmd, "ls ") || strings.Contains(cmd, " ls ") || cmd == "ls"
			return isLs && (strings.Contains(cmd, "--color") || strings.Contains(cmd, "--group-directories"))
		},
		fix: "ls --color and --group-directories are GNU-only. On macOS/BSD use: ls -G (color), CLICOLOR=1 ls, or exa/eza if installed",
	},
	// --- xargs -r / --no-run-if-empty (GNU) ---
	{
		match: func(cmd, out string) bool {
			return strings.Contains(cmd, "xargs") && (strings.Contains(cmd, " -r") || strings.Contains(cmd, "--no-run-if-empty"))
		},
		fix: "xargs -r/--no-run-if-empty is GNU-only. On macOS/BSD guard with: cmd | grep -q . && cmd | xargs ...  or use: if [ -s file ]; then ...",
	},
	// --- mktemp template differences ---
	{
		match: func(cmd, out string) bool {
			// #868: require mktemp-specific diagnostics, not any 'error' text:
			// failed commands routinely have 'error' in stderr, and BSD mktemp
			// ACCEPTS a template without -t (live-verified on macOS).
			if !strings.Contains(cmd, "mktemp") {
				return false
			}
			return strings.Contains(out, "mktemp: too few") ||
				strings.Contains(out, "mktemp: illegal option") ||
				strings.Contains(out, "mktemp: no such file")
		},
		fix: "GNU and BSD mktemp differ. Portable forms: mktemp (file), mktemp -d (directory), or mktemp /tmp/prefix.XXXXXX (template works on both GNU and macOS)",
	},
}

// diagnoseShellCompat detects BSD/GNU command incompatibilities and returns
// a concise diagnostic with the correct cross-platform alternative.
// Returns empty string if no known incompatibility is detected.
//
// The function examines both the command string (proactive detection of
// GNU-only flags) and the combined output (reactive detection from error
// messages). Only fires on macOS/BSD; on Linux all GNU commands work natively.
func diagnoseShellCompat(command, stdout, stderr string) string {
	// On Linux, GNU commands are native -- no compatibility issues expected.
	if runtime.GOOS == "linux" {
		return ""
	}

	combined := stdout + "\n" + stderr
	lowerCmd := strings.ToLower(command)

	for _, pat := range shellCompatPatterns {
		if pat.match(lowerCmd, strings.ToLower(combined)) {
			return "[Shell Compat] " + pat.fix
		}
	}

	return ""
}
