package util

import (
	"path/filepath"
	"strings"
)

// HarnessStateDir is the directory the ggcode harness maintains inside a
// workspace as a side effect of running: checkpoints, auto memory
// (<dir>/memory/*.md, written and pruned by the memory curator), usage and
// guidance statistics, compaction facts, skills, worktrees. Its churn is
// never the user's task change, so every review surface (the "Modified
// files" prompt section, the self-review diff summary, the adversarial
// evaluator's diff, review_changes) leaves it out.
const HarnessStateDir = ".ggcode"

// HarnessStateExcludePathspec is the git pathspec that drops the harness
// directory from a diff, from any cwd inside the repository ("top" anchors
// it at the repo root; a bare ":!.ggcode" would be cwd-relative and miss
// the root directory when the working dir is a subdirectory).
const HarnessStateExcludePathspec = ":(exclude,top)" + HarnessStateDir

// IsHarnessStatePath reports whether path is the harness directory or
// anything inside it. Any path segment named .ggcode counts, so absolute
// tool-arg scopes (/work/.ggcode/memory/x.md) and repo-relative paths
// (.ggcode/memory/x.md) match alike. A sibling like .ggcode-backup does not.
func IsHarnessStatePath(path string) bool {
	// Backslashes are treated as separators on every OS: tool-arg scopes
	// recorded on Windows must match here too (the cost on unix is a
	// literal backslash in a filename, which is vanishingly rare).
	p := strings.ReplaceAll(filepath.ToSlash(strings.TrimSpace(path)), "\\", "/")
	p = strings.TrimPrefix(p, "./")
	if p == "" {
		return false
	}
	for _, seg := range strings.Split(p, "/") {
		if seg == HarnessStateDir {
			return true
		}
	}
	return false
}

// PorcelainPath returns the path of a `git status --porcelain` line
// ("XY path", "?? path", "R  old -> new"), or "" when the line carries no
// path. Renames yield the NEW path.
func PorcelainPath(line string) string {
	// "XY path": two status columns (either may be a space) + one space.
	// Never trim the leading space - it IS a status column.
	line = strings.TrimRight(line, "\r\n")
	if len(line) < 4 {
		return ""
	}
	p := strings.TrimSpace(line[3:])
	if p == "" {
		return ""
	}
	if i := strings.Index(p, " -> "); i >= 0 {
		p = strings.TrimSpace(p[i+len(" -> "):])
	}
	if len(p) >= 2 && p[0] == '"' && p[len(p)-1] == '"' {
		if unq, err := unquotePorcelain(p); err == nil {
			p = unq
		} else {
			p = p[1 : len(p)-1]
		}
	}
	return p
}

// IsHarnessStatePorcelainLine reports whether a porcelain line's path is
// harness state.
func IsHarnessStatePorcelainLine(line string) bool {
	return IsHarnessStatePath(PorcelainPath(line))
}

// unquotePorcelain undoes git's C-style quoting of unusual paths ("a b\tc"
// with octal escapes). Unknown escapes are kept verbatim.
func unquotePorcelain(s string) (string, error) {
	if len(s) < 2 || s[0] != '"' || s[len(s)-1] != '"' {
		return s, nil
	}
	body := s[1 : len(s)-1]
	var b strings.Builder
	for i := 0; i < len(body); i++ {
		c := body[i]
		if c != '\\' || i+1 >= len(body) {
			b.WriteByte(c)
			continue
		}
		i++
		switch e := body[i]; e {
		case 'n':
			b.WriteByte('\n')
		case 't':
			b.WriteByte('\t')
		case 'r':
			b.WriteByte('\r')
		case '"', '\\':
			b.WriteByte(e)
		default:
			// octal escape: up to 3 digits
			if e >= '0' && e <= '7' {
				v := int(e - '0')
				n := 1
				for n < 3 && i+1 < len(body) && body[i+1] >= '0' && body[i+1] <= '7' {
					i++
					v = v*8 + int(body[i]-'0')
					n++
				}
				b.WriteByte(byte(v))
			} else {
				b.WriteByte('\\')
				b.WriteByte(e)
			}
		}
	}
	return b.String(), nil
}

// FilterHarnessStatePaths drops harness-state entries from a path list
// (tool-arg scopes, edited-file lists). Returns nil when nothing is left.
func FilterHarnessStatePaths(paths []string) []string {
	out := make([]string, 0, len(paths))
	for _, p := range paths {
		if IsHarnessStatePath(p) {
			continue
		}
		out = append(out, p)
	}
	return out
}

// FilterHarnessStatePorcelainLines drops harness-state lines from
// `git status --porcelain` output.
func FilterHarnessStatePorcelainLines(lines []string) []string {
	out := make([]string, 0, len(lines))
	for _, l := range lines {
		if IsHarnessStatePorcelainLine(l) {
			continue
		}
		out = append(out, l)
	}
	return out
}
