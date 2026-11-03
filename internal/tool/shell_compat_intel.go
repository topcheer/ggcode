package tool

import (
	"runtime"
	"strings"
)

type shellCompatPattern struct {
	match func(command, combinedOutput string) bool
	fix   string
}

var shellCompatPatterns = []shellCompatPattern{

	{
		match: func(cmd, out string) bool {

			return strings.Contains(out, "extra characters at the end of") ||
				strings.Contains(out, "command a expects") ||
				(strings.Contains(out, "sed:") && strings.Contains(out, "-i"))
		},
		fix: "BSD sed requires a backup suffix after -i. Use: sed -i '' 's/old/new/' file  (macOS/BSD) instead of sed -i 's/old/new/' file  (GNU)",
	},

	{
		match: func(cmd, out string) bool {

			if segmentHasFlagToken(cmd, "readlink", "-f", "--canonicalize") {
				return true
			}
			return strings.Contains(out, "readlink: illegal option") ||
				strings.Contains(out, "readlink: invalid option -- 'f'")
		},
		fix: "readlink -f is GNU-only. On macOS/BSD use: realpath file  or  python3 -c \"import os; print(os.path.realpath('file'))\"",
	},

	{
		match: func(cmd, out string) bool {

			if segmentHasFlagToken(cmd, "grep", "-p", "--perl-regexp") {
				return true
			}
			return strings.Contains(out, "grep: option requires an argument") ||
				(strings.Contains(out, "grep:") && strings.Contains(out, "invalid option"))
		},
		fix: "grep -P (PCRE) is GNU-only. Use: grep -E 'pattern' (ERE, cross-platform) or rg 'pattern' (ripgrep, if installed)",
	},

	{
		match: func(cmd, out string) bool {

			if segmentHasFlagToken(cmd, "date", "-d", "--date") {
				return true
			}
			return strings.Contains(out, "date: illegal option") ||
				strings.Contains(out, "date: invalid option -- 'd'")
		},
		fix: "date -d is GNU-only. On macOS/BSD use: date -v-1d (yesterday) or date -v+1d (tomorrow). For parsing use: python3 -c \"from datetime import datetime; print(datetime.strptime('2024-01-01', '%Y-%m-%d'))\"",
	},

	{
		match: func(cmd, out string) bool {

			if segmentHasFlagToken(cmd, "stat", "-c", "--format") {
				return true
			}
			return strings.Contains(out, "stat: illegal option") ||
				strings.Contains(out, "stat: invalid option -- 'c'")
		},
		fix: "stat -c is GNU-only. On macOS/BSD use: stat -f '%z' file (file size), stat -f '%m' file (mtime). Or use: wc -c < file for size",
	},

	{
		match: func(cmd, out string) bool {

			isFind := strings.HasPrefix(cmd, "find ") || strings.Contains(cmd, " find ") || cmd == "find"
			return (isFind && strings.Contains(cmd, "-printf")) ||
				strings.Contains(out, "find: -printf: unknown primary") ||
				strings.Contains(out, "find: unknown predicate `-printf'")
		},
		fix: "find -printf is GNU-only. On macOS/BSD use: find . -name '*.go' | xargs -I{} echo {}  or  find . -name '*.go' -exec echo {} \\;",
	},

	{
		match: func(cmd, out string) bool {

			isHead := strings.HasPrefix(cmd, "head ") || strings.Contains(cmd, " head ") || cmd == "head"
			return strings.Contains(out, "head: illegal line count -- -") ||
				(isHead && strings.Contains(cmd, "-n -"))
		},
		fix: "head -n -N (drop last N lines) is GNU-only. On macOS/BSD use: head -n $(( $(wc -l < file) - N )) file  or  sed '$d' file (drop last line)",
	},

	{
		match: func(cmd, out string) bool {
			trimmed := strings.TrimSpace(cmd)
			return strings.HasPrefix(trimmed, "timeout ") ||
				(strings.Contains(out, "command not found") && strings.Contains(out, "timeout"))
		},
		fix: "timeout is GNU coreutils, not on macOS by default. Install with: brew install coreutils (provides gtimeout), or use: perl -e 'alarm shift; exec @ARGV' 5 cmd",
	},

	{
		match: func(cmd, out string) bool {

			isDu := strings.HasPrefix(cmd, "du ") || strings.Contains(cmd, " du ") || cmd == "du"
			return isDu && strings.Contains(cmd, "--max-depth")
		},
		fix: "du --max-depth is GNU-only. On macOS/BSD use: du -d N -h (e.g., du -d 1 -h for depth 1)",
	},

	{
		match: func(cmd, out string) bool {

			isLs := strings.HasPrefix(cmd, "ls ") || strings.Contains(cmd, " ls ") || cmd == "ls"
			return isLs && (strings.Contains(cmd, "--color") || strings.Contains(cmd, "--group-directories"))
		},
		fix: "ls --color and --group-directories are GNU-only. On macOS/BSD use: ls -G (color), CLICOLOR=1 ls, or exa/eza if installed",
	},

	{
		match: func(cmd, out string) bool {

			for _, seg := range strings.Split(cmd, "|") {
				if !strings.Contains(seg, "xargs") {
					continue
				}
				for _, tok := range strings.Fields(seg) {
					if tok == "-r" || tok == "--no-run-if-empty" || tok == "xargs" || tok == "xargs\"" {
						if tok == "-r" || tok == "--no-run-if-empty" {
							return true
						}
					}
				}
			}
			return false
		},
		fix: "xargs -r/--no-run-if-empty is GNU-only. On macOS/BSD guard with: cmd | grep -q . && cmd | xargs ...  or use: if [ -s file ]; then ...",
	},

	{
		match: func(cmd, out string) bool {

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

func segmentHasFlagToken(cmd, name string, flags ...string) bool {
	flagSet := make(map[string]bool, len(flags))
	for _, f := range flags {
		flagSet[f] = true
	}
	for _, seg := range strings.FieldsFunc(cmd, func(r rune) bool {
		return r == '|' || r == '&' || r == ';'
	}) {
		seg = strings.TrimSpace(seg)
		if seg != name && !strings.HasPrefix(seg, name+" ") {
			continue
		}
		for _, tok := range strings.Fields(seg) {
			if flagSet[tok] {
				return true
			}
		}
	}
	return false
}

func diagnoseShellCompat(command, stdout, stderr string) string {

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
