package tool

import (
	"bytes"
	"context"
	"os"
	"os/exec"
	"strings"
	"unicode/utf8"
)

func truncateUTF8Safe(s string, maxBytes int) string {
	if len(s) <= maxBytes {
		return s
	}

	for maxBytes > 0 && !utf8.RuneStart(s[maxBytes]) {
		maxBytes--
	}
	return s[:maxBytes]
}

const coAuthorTrailer = "Co-Authored-By: ggcode <noreply@ggcode.dev>"

func coAuthorInjectionDisabled() bool {
	return os.Getenv("GGCODE_NO_COAUTHOR") != ""
}

func resolveDir(paths ...string) string {
	for _, p := range paths {
		if p != "" {
			return p
		}
	}
	return ""
}

func gitCommand(ctx context.Context, args ...string) *exec.Cmd {
	cmd := exec.CommandContext(ctx, "git", args...)
	cmd.Env = append(os.Environ(), "GIT_PAGER=cat")
	return cmd
}

func gitTrackedFiles(ctx context.Context, dir string) map[string]struct{} {
	cmd := gitCommand(ctx, "ls-files", "-z", "--cached", "--others", "--exclude-standard")
	cmd.Dir = dir
	out, err := cmd.Output()
	if err != nil {
		return nil
	}
	if len(out) == 0 {
		return map[string]struct{}{}
	}
	files := bytes.Split(out, []byte{0})
	result := make(map[string]struct{}, len(files))
	for _, f := range files {
		s := string(f)
		if s != "" {
			result[s] = struct{}{}
		}
	}
	return result
}

func isGitCommitCommand(cmd string) bool {

	s := cmd
	for {
		idx := strings.Index(s, "=")
		if idx < 0 {
			break
		}
		spaceIdx := strings.Index(s[:idx], " ")
		if spaceIdx >= 0 {
			break
		}

		after := s[idx+1:]
		if spIdx := strings.Index(after, " "); spIdx >= 0 {
			s = after[spIdx+1:]
		} else {
			break
		}
	}
	s = strings.TrimSpace(s)

	if !strings.HasPrefix(s, "git ") && s != "git" {
		return false
	}
	rest := strings.TrimSpace(s[3:])

	for {
		rest = strings.TrimSpace(rest)
		if !strings.HasPrefix(rest, "-") {
			break
		}

		spIdx := strings.Index(rest, " ")
		if spIdx < 0 {
			return false
		}
		rest = strings.TrimSpace(rest[spIdx+1:])

		if rest != "" && !strings.HasPrefix(rest, "-") && !strings.HasPrefix(rest, "commit") {

			spIdx = strings.Index(rest, " ")
			if spIdx < 0 {
				return false
			}
			rest = strings.TrimSpace(rest[spIdx+1:])
		}
	}
	return strings.HasPrefix(rest, "commit")
}

func injectCoAuthorTrailer(cmd string) string {
	if coAuthorInjectionDisabled() {
		return cmd
	}
	if strings.Contains(cmd, coAuthorTrailer) {
		return cmd
	}

	if idx := findLastMFlag(cmd); idx >= 0 {
		return injectIntoMFlag(cmd, idx)
	}

	return cmd + " --trailer \"" + coAuthorTrailer + "\""
}

func findLastMFlag(cmd string) int {
	idx := -1
	searchFrom := 0
	for {
		found := strings.Index(cmd[searchFrom:], "-m")
		if found < 0 {
			break
		}
		absIdx := searchFrom + found

		before := ""
		if absIdx > 0 {
			before = string(cmd[absIdx-1])
		}
		after := ""
		if absIdx+2 < len(cmd) {
			after = string(cmd[absIdx+2])
		}
		if (before == " " || before == "" || before == "'") && (after == " " || after == "'" || after == "\"") {
			idx = absIdx
		}
		searchFrom = absIdx + 2
		if searchFrom >= len(cmd) {
			break
		}
	}
	return idx
}

func injectIntoMFlag(cmd string, mIdx int) string {

	pos := mIdx + 2

	for pos < len(cmd) && cmd[pos] == ' ' {
		pos++
	}
	if pos >= len(cmd) {
		return cmd + " --trailer \"" + coAuthorTrailer + "\""
	}

	quote := cmd[pos]
	if quote != '"' && quote != '\'' {

		return cmd + " --trailer \"" + coAuthorTrailer + "\""
	}

	pos++
	var absClose = -1
	for i := pos; i < len(cmd); i++ {
		if cmd[i] == '\\' {
			i++
			continue
		}
		if cmd[i] == quote {
			absClose = i
			break
		}
	}
	if absClose < 0 {
		return cmd + " --trailer \"" + coAuthorTrailer + "\""
	}
	msg := cmd[pos:absClose]
	newMsg := msg + "\n\n" + coAuthorTrailer
	return cmd[:pos] + newMsg + cmd[absClose:]
}

func isBinaryFile(path string) bool {
	f, err := os.Open(path)
	if err != nil {
		return true
	}
	defer f.Close()
	buf := make([]byte, 8192)
	n, err := f.Read(buf)
	if err != nil {
		return true
	}
	for i := 0; i < n; i++ {
		if buf[i] == 0 {
			return true
		}
	}
	return false
}

var skipDirs = map[string]bool{
	".git":         true,
	"node_modules": true,
	"vendor":       true,
	"__pycache__":  true,
}

func isGitCommand(cmd string) bool {
	s := strings.TrimSpace(cmd)

	for strings.Contains(s, "=") && strings.Index(s, "=") < strings.Index(s, " ") {
		if spIdx := strings.Index(s, " "); spIdx >= 0 {
			s = strings.TrimSpace(s[spIdx+1:])
		} else {
			break
		}
	}
	return strings.HasPrefix(strings.TrimSpace(s), "git ")
}
